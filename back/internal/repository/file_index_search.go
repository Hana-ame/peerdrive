// Package repository — file_index 搜索（file_index_search.go）。
//
// 设计取舍（为什么这么定，写下来免得下次重新论证）：
//
//   1. **不加列**。搜索字段全部来自现有 schema（name / path / size / seq / deleted），
//      所以已有部署升级后**不需要迁移**——只多一张 CREATE INDEX IF NOT EXISTS。
//   2. **不加 FTS5 虚表**。FTS5 会把索引写入成本摊到每次 UpsertFileIndex（create /
//      upload complete / ApplySync 三条路径），为一个本地运维用的搜索换来每次写入
//      的额外开销，不划算。file_index 的行量级是「一台节点登记过的文件数」
//      （几千到几十万），带 LIMIT 的有界全表扫完全够用。
//   3. **子串 + LIKE，不做 Unicode 大小写折叠**。SQLite 的 LIKE 默认对 ASCII 字母
//      大小写不敏感，覆盖绝大多数文件名；中文/日文等的「大小写」概念不适用，
//      而完整 Unicode case-folding 需要注册自定义函数，收益不成比例。测试里明确
//      断言 ASCII 大小写行为。
//   4. **%/_ 按字面量转义**。用户输入 % 当成通配符会让「搜一个文件」退化成
//      「匹配一切」——既是 bug 也是信息泄露面（全表回显）。

package repository

import (
	"fmt"
	"strings"
)

// SearchQuery file_index 搜索参数。
//
//   - Q：name 或 path 的**子串**匹配（不分大小写，见包注释第 3 条）。
//     空串（含纯空白）= 不按名称过滤，返回分页窗口内的全部未删除行。
//   - MinSize/MaxSize：**指针**而非 int64 哨兵——size=0 是合法值（空文件在索引里
//     就是 0），用 0 表示「不限制」会和「限定为 0 字节」混淆。nil = 不施加。
//     含边界（>= 与 <=）。
//   - Offset/Limit：分页。Limit<=0 取 SearchDefaultLimit，超过 SearchMaxLimit
//     一律 clamp——它来自远端帧/HTTP 参数，不可信，直接进 SQL LIMIT 会让单次查询
//     物化全表（内存 DoS），与 ListFileIndex 是同一套防御。Offset<0 归零
//     （SQLite 把负 OFFSET 当 0，但显式归零让 SQL 与 Go 侧语义一致）。
//
// 刻意**没有**的时间/标签过滤：file_index 没有 tags 列（合集层才有 tags），
// created_at/updated_at 是 DATETIME 字符串，为它们加范围过滤会把「搜索」变成
// 「任意查询构造器」，收益不明显。要按时间排序已经由 ORDER BY seq 近似覆盖
// （seq 单调递增，与登记时间同序）。
// CategoryExtensions 将逻辑文件分类映射为预设的小写文件扩展名集合。
var CategoryExtensions = map[string][]string{
	"docs":     {"pdf", "doc", "docx", "txt", "md", "rtf", "odt", "csv", "xls", "xlsx", "ppt", "pptx"},
	"images":   {"jpg", "jpeg", "png", "gif", "webp", "svg", "bmp", "ico"},
	"videos":   {"mp4", "mkv", "avi", "mov", "webm", "flv", "wmv"},
	"audio":    {"mp3", "wav", "flac", "aac", "ogg", "m4a", "wma"},
	"archives": {"zip", "rar", "7z", "tar", "gz", "bz2", "xz"},
	"code":     {"js", "ts", "jsx", "tsx", "go", "py", "rs", "c", "cpp", "h", "java", "html", "css", "json", "sh", "yaml", "yml"},
}

type SearchQuery struct {
	Q         string   // name 或 path 的子串匹配（不分大小写）
	Tag       string   // 标签过滤：关联 sha_tags 校验
	Category  string   // 分类过滤：docs, images, videos, audio, archives, code, inbox
	Exts      []string // 扩展名自定义过滤
	SortBy    string   // 排序字段白名单：name, size, time, seq
	SortOrder string   // 排序方向：asc, desc
	MinSize   *int64
	MaxSize   *int64
	Offset    int
	Limit     int
}

// 搜索分页边界。导出，让 transport/service 层与 repository 共用同一组数，
// 避免「两层各写一个 1000」之后各自漂移。
const (
	SearchDefaultLimit = 100
	SearchMaxLimit     = 1000
)

// SearchFileIndex 搜索未删除的文件索引映射。
//
// 返回 (命中行, 命中总数, err)：总数单独一条 COUNT(*) 用同一组 WHERE 算，
// 因为调用方（分页控件 / 搜索 UI）需要知道「还有没有下一页」——只回一页数据
// 时，客户端没法区分「这就是全部」和「后面还有」。两条查询都在只读路径上，
// 不在上传/同步热路径。
//
// 无命中时 files 返回 nil、total 返回 0（与 GetFileIndex 的 nil 约定一致，
// 调用方可直接判空；transport 层负责把 nil 转成空数组再发帧）。
//
// 排序：seq DESC（最新登记的在前），与 ListFileIndex 一致——两次翻页之间插入的
// 新文件会「挤掉」末尾条目，这是可接受的（分页游标是 offset 而非游标值）。
func SearchFileIndex(q SearchQuery) ([]FileIndex, int64, error) {
	conds := []string{"deleted = 0"}
	args := make([]any, 0, 8)

	if pat := escapeLikePattern(q.Q); pat != "" {
		// 用括号包住 OR：整个条件列表是 AND 连接的，不括号会变成
		// 「name 必须含 AND path 必须含」，而 path 命中场景（关键词只在目录里）
		// 会一条都回不了。
		conds = append(conds, "(`name` LIKE ? ESCAPE '\\' OR `path` LIKE ? ESCAPE '\\')")
		args = append(args, pat, pat)
	}
	if q.MinSize != nil {
		conds = append(conds, "size >= ?")
		args = append(args, *q.MinSize)
	}
	if q.MaxSize != nil {
		conds = append(conds, "size <= ?")
		args = append(args, *q.MaxSize)
	}

	// 标签过滤（与 sha_tags 建立关联）
	if tag := strings.TrimSpace(q.Tag); tag != "" {
		conds = append(conds, "EXISTS (SELECT 1 FROM sha_tags WHERE sha_tags.sha = file_index.hash AND sha_tags.tag = ?)")
		args = append(args, tag)
	}

	// 分类过滤（标准分类 / inbox 待审态）
	if cat := strings.ToLower(strings.TrimSpace(q.Category)); cat != "" && cat != "all" {
		if cat == "inbox" {
			conds = append(conds, "is_inbox = 1")
		} else if extensions, found := CategoryExtensions[cat]; found {
			orClauses := make([]string, 0, len(extensions))
			for _, ext := range extensions {
				orClauses = append(orClauses, "`name` LIKE ? ESCAPE '\\'")
				args = append(args, "%."+escapeLikeExact(ext))
			}
			conds = append(conds, "("+strings.Join(orClauses, " OR ")+")")
		}
	}

	// 自定义扩展名列表过滤
	if len(q.Exts) > 0 {
		orClauses := make([]string, 0, len(q.Exts))
		for _, ext := range q.Exts {
			cleanExt := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
			if cleanExt != "" {
				orClauses = append(orClauses, "`name` LIKE ? ESCAPE '\\'")
				args = append(args, "%."+escapeLikeExact(cleanExt))
			}
		}
		if len(orClauses) > 0 {
			conds = append(conds, "("+strings.Join(orClauses, " OR ")+")")
		}
	}

	where := strings.Join(conds, " AND ")

	var total int64
	if err := db.QueryRow("SELECT COUNT(*) FROM file_index WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count file_index: %w", err)
	}

	limit := q.Limit
	if limit <= 0 {
		limit = SearchDefaultLimit
	}
	if limit > SearchMaxLimit {
		limit = SearchMaxLimit
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}

	// 严格白名单排序，防御 SQL 注入
	var orderCol string
	switch strings.ToLower(strings.TrimSpace(q.SortBy)) {
	case "name", "filename":
		orderCol = "name"
	case "size":
		orderCol = "size"
	case "time", "date", "created", "created_at":
		orderCol = "created_at"
	case "updated", "updated_at":
		orderCol = "updated_at"
	case "seq":
		orderCol = "seq"
	default:
		orderCol = "seq"
	}

	var orderDir string
	if strings.EqualFold(strings.TrimSpace(q.SortOrder), "asc") {
		orderDir = "ASC"
	} else {
		orderDir = "DESC"
	}

	var orderClause string
	if orderCol == "seq" {
		orderClause = fmt.Sprintf("ORDER BY seq %s", orderDir)
	} else {
		orderClause = fmt.Sprintf("ORDER BY %s %s, seq DESC", orderCol, orderDir)
	}

	query := fmt.Sprintf("SELECT hash, path, name, size, deleted, seq, created_at, updated_at, uploader_peer_id, is_inbox "+
		"FROM file_index WHERE %s %s LIMIT ? OFFSET ?", where, orderClause)

	rows, err := db.Query(query, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("search file_index: %w", err)
	}
	defer rows.Close()

	var out []FileIndex
	for rows.Next() {
		f, err := scanFileIndex(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *f)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// escapeLikePattern 把用户输入转成 LIKE 模式（ESCAPE '\'）：
// \ → \\、% → \%、_ → \_，再包上 %..% 变成子串。
//
// 空串（含纯空白）返回 ""，调用方据此跳过该条件。
//
// 转义顺序由 strings.NewReplacer 保证安全：它是单趟、非重叠匹配，不会回头
// 扫描自己刚写入的替换结果（若先转 % 再转 \，会把 \% 的 \ 再变成 \\）。
func escapeLikePattern(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	repl := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + repl.Replace(s) + "%"
}

// escapeLikeExact 对精准匹配片段（如扩展名）转义 LIKE 特殊字符。
func escapeLikeExact(s string) string {
	repl := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return repl.Replace(s)
}

