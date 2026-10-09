// file_index_search_test.go — file_index 搜索功能测试。
//
// 发现背景：feat/file-index-search。file_index 此前只有按 hash 精确查（GetFileIndex）
// 与全量分页列（ListFileIndex），运维者/前端要找一个文件只能翻页翻完全量清单。
// 本文件把 SearchFileIndex 的匹配语义钉死：子串、大小写、LIKE 元字符转义、
// size 区间、分页 clamp、deleted 排除——这些语义全靠 SQL 字符串拼出来，
// 没有表驱动测试就会静默退化。
//
// 为什么用 initTestDB(t) 而不是 :memory:：initTestDB 走 t.TempDir() 文件库，
// 覆盖「表真正落在磁盘上」的路径，且在 Windows 上注册 CloseDB 清理（见 db_test.go）。

package repository

import (
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedRow 一条待灌入的索引行。hash 留空时按序号生成（避免手写 64hex 出错）。
type seedRow struct {
	hash, path, name string
	size             int64
	deleted          bool
}

// seedRows 用真实的 UpsertFileIndex 灌入测试数据，返回 hash 列表（插入顺序）。
// 用真实 upsert 而非裸 INSERT：seq 单调性、DEFAULT 值、索引建立都一起被覆盖。
func seedRows(t *testing.T, rows []seedRow) []string {
	t.Helper()
	hashes := make([]string, 0, len(rows))
	for i, r := range rows {
		hash := r.hash
		if hash == "" {
			hash = fmt.Sprintf("%064x", uint64(i+1))
		}
		_, err := UpsertFileIndex(hash, r.path, r.name, r.size, r.deleted)
		require.NoError(t, err)
		hashes = append(hashes, hash)
	}
	return hashes
}

func i64p(v int64) *int64 { return &v }

// testDataset 搜索测试共享数据集（7 行，1 行已删除）。
// 覆盖：同前缀多个命中、只出现在 path 不在 name 的关键词、空文件、大小写差异、
// 括号（非 LIKE 元字符）与连字符。
func testDataset() []seedRow {
	return []seedRow{
		{path: "/data/media/music/album01.mp3", name: "album01.mp3", size: 100},
		{path: "/data/media/music/album02.mp3", name: "album02.mp3", size: 200},
		{path: "/data/media/videos/film.mov", name: "film.mov", size: 1000},
		{path: "/data/media/videos/clip-2.mov", name: "clip-2.mov", size: 1000},
		{path: "/data/docs/Report (2026).pdf", name: "Report (2026).pdf", size: 50},
		{path: "/data/docs/README.txt", name: "README.txt", size: 10},
		{path: "/data/media/hidden.dat", name: "hidden.dat", size: 500, deleted: true},
	}
}

// namesOf 把命中行按 name 排好序返回，便于断言集合而不依赖 SQL 返回顺序。
func namesOf(fs []FileIndex) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Name)
	}
	sort.Strings(out)
	return out
}

// TestSearchFileIndex_Substring 子串命中：name 与 path 各自都能命中。
func TestSearchFileIndex_Substring(t *testing.T) {
	initTestDB(t)
	seedRows(t, testDataset())

	// name 子串（前缀命中多行）
	fs, total, err := SearchFileIndex(SearchQuery{Q: "album"})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Equal(t, []string{"album01.mp3", "album02.mp3"}, namesOf(fs))

	// name 中缀（不是前缀）也要命中
	fs, total, err = SearchFileIndex(SearchQuery{Q: "lbu"})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Equal(t, []string{"album01.mp3", "album02.mp3"}, namesOf(fs))

	// 只在 path 里的关键词：name 全部不含 "media"（basename 里没有），
	// 只有 path 的目录段含它 —— 所以这命中纯粹靠 path 那一半的 LIKE。
	// 数据集里 6 条未删除行中有 4 条在 /data/media/ 下（另 2 条在 /data/docs/）。
	fs, total, err = SearchFileIndex(SearchQuery{Q: "media"})
	require.NoError(t, err)
	assert.Equal(t, int64(4), total, "media 只出现在 path，命中 /data/media 下的 4 行")

	// 目录级命中（path 前缀语义由子串自然实现）
	fs, total, err = SearchFileIndex(SearchQuery{Q: "/data/media/videos"})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Equal(t, []string{"clip-2.mov", "film.mov"}, namesOf(fs))
}

// TestSearchFileIndex_MissAndEmpty 未命中与空查询。
func TestSearchFileIndex_MissAndEmpty(t *testing.T) {
	initTestDB(t)
	seedRows(t, testDataset())

	// 未命中：files 为 nil（不是空切片）、total 为 0
	fs, total, err := SearchFileIndex(SearchQuery{Q: "album99"})
	require.NoError(t, err)
	assert.Nil(t, fs)
	assert.Equal(t, int64(0), total)

	// 空串 = 不按名称过滤，返回分页窗口内的全部未删除行
	fs, total, err = SearchFileIndex(SearchQuery{})
	require.NoError(t, err)
	assert.Equal(t, int64(6), total)
	assert.Len(t, fs, 6)

	// 纯空白同样视为空查询（不施加条件）
	fs, total, err = SearchFileIndex(SearchQuery{Q: "   "})
	require.NoError(t, err)
	assert.Equal(t, int64(6), total)
	assert.Len(t, fs, 6)
}

// TestSearchFileIndex_EmptyDB 空库上的空查询：零行不报错。
//
// 为什么要单独一个测试而不在 MissAndEmpty 里再 initTestDB 一次：initTestDB 用的是
// t.TempDir() **文件**库并注册 t.Cleanup(CloseDB)。同一个测试里调第二次，第一个
// 库的句柄会被第二次 InitDB 顶掉，于是它的 cleanup 关的不是当前 DB——Windows 上
// TempDir 的 RemoveAll 就报 "The process cannot access the file because it is being
// used by another process"。Linux 上 unlink 打开中的文件是允许的，所以本地永远绿、
// 只有 Windows CI 红（这正是 db_test.go 注释里说的那类坑）。一个测试一个库。
func TestSearchFileIndex_EmptyDB(t *testing.T) {
	initTestDB(t)

	fs, total, err := SearchFileIndex(SearchQuery{})
	require.NoError(t, err)
	assert.Nil(t, fs)
	assert.Equal(t, int64(0), total)

	// 带 q 的空查询同样不报错
	fs, total, err = SearchFileIndex(SearchQuery{Q: "anything"})
	require.NoError(t, err)
	assert.Nil(t, fs)
	assert.Equal(t, int64(0), total)
}

// TestSearchFileIndex_DeletedExcluded 已删除行不返回（tombstone 只经 SyncSince 暴露）。
func TestSearchFileIndex_DeletedExcluded(t *testing.T) {
	initTestDB(t)
	seedRows(t, testDataset())

	fs, total, err := SearchFileIndex(SearchQuery{Q: "hidden"})
	require.NoError(t, err)
	assert.Nil(t, fs)
	assert.Equal(t, int64(0), total, "deleted=1 的行不得被搜索到")

	// 注意关键词选 "n.dat" 而不是 "dat"：数据集里每条 path 都以 "/data/" 开头，
	// 裸 "dat" 会命中全部 6 行，测不出 deleted 过滤是否生效（第一版就踩了这个）。
	fs, total, err = SearchFileIndex(SearchQuery{Q: "n.dat"})
	require.NoError(t, err)
	assert.Nil(t, fs, "只剩被删除的 hidden.dat 命中 name 时必须仍返回空")
	assert.Equal(t, int64(0), total)
}

// TestSearchFileIndex_CaseInsensitive ASCII 大小写不敏感（SQLite LIKE 默认行为）。
func TestSearchFileIndex_CaseInsensitive(t *testing.T) {
	initTestDB(t)
	seedRows(t, testDataset())

	for _, q := range []string{"ALBUM", "Album", "aLbUm"} {
		fs, total, err := SearchFileIndex(SearchQuery{Q: q})
		require.NoError(t, err, "q=%q", q)
		assert.Equal(t, int64(2), total, "q=%q", q)
		assert.Equal(t, []string{"album01.mp3", "album02.mp3"}, namesOf(fs), "q=%q", q)
	}

	// 大写 name 用小写搜
	fs, total, err := SearchFileIndex(SearchQuery{Q: "readme"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, []string{"README.txt"}, namesOf(fs))

	// 大小写混合输入同样匹配（Report (2026).pdf）
	fs, total, err = SearchFileIndex(SearchQuery{Q: "report (2026)"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, []string{"Report (2026).pdf"}, namesOf(fs))
}

// TestSearchFileIndex_LIKEMetaChars % _ \ 必须按字面量匹配，不当通配符。
//
// 这是最关键的一组语义：不转义的话，搜 "%" 会变成「匹配一切」——既是 bug
// （点一个文件按钮列出全库），也是信息泄露面。
func TestSearchFileIndex_LIKEMetaChars(t *testing.T) {
	initTestDB(t)
	seedRows(t, testDataset())

	cases := []struct {
		name     string
		q        string
		wantName []string
	}{
		{"百分号是字面量", "%", nil},
		{"下划线是字面量", "_", nil},
		{"反斜杠是字面量", `\`, nil},
		{"百分号在前缀", "%album", nil},
		{"下划线当通配符的旧行为必须消失", "lbu?", nil},
		{"问号不是元字符", "?", nil},
		{"连字符非元字符", "clip-2", []string{"clip-2.mov"}},
		{"括号非元字符", "Report (2026)", []string{"Report (2026).pdf"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs, total, err := SearchFileIndex(SearchQuery{Q: tc.q})
			require.NoError(t, err)
			if tc.wantName == nil {
				assert.Nil(t, fs, "q=%q", tc.q)
				assert.Equal(t, int64(0), total, "q=%q", tc.q)
				return
			}
			assert.Equal(t, int64(int64(len(tc.wantName))), total)
			assert.Equal(t, tc.wantName, namesOf(fs))
		})
	}

	// 数据库里真的有含 % 的文件名时，字面量搜索必须能精确命中它
	_, err := UpsertFileIndex("ff00000000000000000000000000000000000000000000000000000000000000",
		"/data/media/100%.mp4", "100%.mp4", 777, false)
	require.NoError(t, err)

	fs, total, err := SearchFileIndex(SearchQuery{Q: "100%"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total, "字面量 % 必须只命中它自己")
	assert.Equal(t, []string{"100%.mp4"}, namesOf(fs))

	// 同一个文件用不带 % 的前缀也能搜到
	fs, total, err = SearchFileIndex(SearchQuery{Q: "100"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
}

// TestSearchFileIndex_SizeFilters size 区间过滤（含边界、开区间为空、nil 不约束）。
func TestSearchFileIndex_SizeFilters(t *testing.T) {
	initTestDB(t)
	seedRows(t, testDataset())

	cases := []struct {
		name      string
		min, max  *int64
		wantTotal int64
		wantNames []string
	}{
		{"仅下界含边界", i64p(1000), nil, 2, []string{"clip-2.mov", "film.mov"}},
		{"仅上界含边界", nil, i64p(200), 4, []string{"README.txt", "Report (2026).pdf", "album01.mp3", "album02.mp3"}},
		{"上界不含 200", nil, i64p(199), 3, []string{"README.txt", "Report (2026).pdf", "album01.mp3"}},
		{"精确单点", i64p(100), i64p(100), 1, []string{"album01.mp3"}},
		{"空区间", i64p(1000), i64p(200), 0, nil},
		{"零字节过滤", i64p(0), i64p(0), 0, nil},
		{"无约束等于全量", nil, nil, 6, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs, total, err := SearchFileIndex(SearchQuery{MinSize: tc.min, MaxSize: tc.max})
			require.NoError(t, err)
			assert.Equal(t, tc.wantTotal, total)
			if tc.wantNames == nil {
				if tc.wantTotal == 0 {
					assert.Nil(t, fs)
				} else {
					assert.Len(t, fs, int(tc.wantTotal))
				}
				return
			}
			assert.Equal(t, tc.wantNames, namesOf(fs))
		})
	}

	// size 过滤与 q 可叠加
	fs, total, err := SearchFileIndex(SearchQuery{Q: "album", MinSize: i64p(150)})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, []string{"album02.mp3"}, namesOf(fs))
}

// TestSearchFileIndex_Pagination 分页边界：offset/limit clamp 与越界行为。
func TestSearchFileIndex_Pagination(t *testing.T) {
	initTestDB(t)
	seedRows(t, testDataset()) // 6 条未删除

	// 正常分页：两页各 2 条，total 恒为命中总数（不是当前页数）
	fs, total, err := SearchFileIndex(SearchQuery{Q: "album", Offset: 0, Limit: 1})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total, "total 是命中总数，不是当前页长度")
	assert.Len(t, fs, 1)
	fs2, _, err := SearchFileIndex(SearchQuery{Q: "album", Offset: 1, Limit: 1})
	require.NoError(t, err)
	assert.Len(t, fs2, 1)
	assert.NotEqual(t, fs[0].Hash, fs2[0].Hash, "两页不能重叠")

	// 排序稳定：seq DESC = 后插入的先出，两页合起来覆盖全部命中
	all, _, err := SearchFileIndex(SearchQuery{Q: "album", Offset: 0, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, all[0].Seq, fs[0].Seq)

	// offset 越过末尾：空结果但 total 仍有值
	fs, total, err = SearchFileIndex(SearchQuery{Offset: 10, Limit: 5})
	require.NoError(t, err)
	assert.Nil(t, fs)
	assert.Equal(t, int64(6), total)

	// limit<=0 走默认 100（这里数据少，等于全量）
	fs, _, err = SearchFileIndex(SearchQuery{Limit: 0})
	require.NoError(t, err)
	assert.Len(t, fs, 6)
	fs, _, err = SearchFileIndex(SearchQuery{Limit: -5})
	require.NoError(t, err)
	assert.Len(t, fs, 6)

	// limit 超上限被 clamp：不可信的远端值不能把全表物化
	fs, _, err = SearchFileIndex(SearchQuery{Limit: 1 << 20})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(fs), SearchMaxLimit)

	// offset 负数归零（SQLite 会把负 OFFSET 当 0，这里显式归零保证语义一致）
	fs, _, err = SearchFileIndex(SearchQuery{Offset: -3, Limit: 2})
	require.NoError(t, err)
	first, _, err := SearchFileIndex(SearchQuery{Offset: 0, Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, first[0].Hash, fs[0].Hash, "offset<0 应与 offset=0 相同")

	// 分页边界恰好落在数据末尾
	fs, _, err = SearchFileIndex(SearchQuery{Offset: 4, Limit: 2})
	require.NoError(t, err)
	assert.Len(t, fs, 2)
	fs, _, err = SearchFileIndex(SearchQuery{Offset: 5, Limit: 2})
	require.NoError(t, err)
	assert.Len(t, fs, 1)
}

// TestSearchFileIndex_EscapeLikePattern 转义函数的单测（纯函数，直接表驱动）。
func TestSearchFileIndex_EscapeLikePattern(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"   ", ""},
		{"album", "%album%"},
		{"%", "%\\%%"},
		{"_", "%\\_%"},
		{"\\", "%\\\\%"},
		{"a%b_c\\d", "%a\\%b\\_c\\\\d%"},
		{"100%.mp4", "%100\\%.mp4%"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, escapeLikePattern(tc.in), "in=%q", tc.in)
	}
}

// TestSearchFileIndex_ZeroByteSize size=0 是合法值（空文件在索引里就是 0），
// 所以过滤参数必须用指针而不是 0 值哨兵——否则「不限制」和「限定为 0 字节」无法区分。
func TestSearchFileIndex_ZeroByteSize(t *testing.T) {
	initTestDB(t)
	seedRows(t, []seedRow{
		{path: "/data/empty.bin", name: "empty.bin", size: 0},
		{path: "/data/full.bin", name: "full.bin", size: 1234},
	})

	// 精确筛 0 字节
	fs, total, err := SearchFileIndex(SearchQuery{MinSize: i64p(0), MaxSize: i64p(0)})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, []string{"empty.bin"}, namesOf(fs))

	// 排除 0 字节
	fs, total, err = SearchFileIndex(SearchQuery{MinSize: i64p(1)})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, []string{"full.bin"}, namesOf(fs))

	// nil = 不约束（两个字段都 nil 才等于全量）
	fs, total, err = SearchFileIndex(SearchQuery{})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, fs, 2)
}

// TestSearchFileIndex_IndexColumn 搜索索引存在（idx_file_index_name）。
//
// 索引不建不会让查询失败——只会变慢。这里显式断言它被建出来，
// 防止未来有人「清理无用索引」时把它删掉。
func TestSearchFileIndex_IndexColumn(t *testing.T) {
	initTestDB(t)
	seedRows(t, testDataset())

	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master
		WHERE type='index' AND name='idx_file_index_name'`).Scan(&n)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "idx_file_index_name 必须存在")
}
