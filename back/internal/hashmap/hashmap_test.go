package hashmap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// fakeStore is an in-memory Store implementation (test-only): it mirrors the
// SQLite table semantics used by repository.FileIndex — Get hides tombstones,
// List/ListSince filter them, Upsert bumps seq. 之所以能脱离数据库单测门面
// 语义，靠的就是 Store 接口这一层间接。
type fakeStore struct {
	mu     sync.Mutex
	byHash map[string]*FileInfo
	seq    int64
}

func newFakeStore() *fakeStore {
	return &fakeStore{byHash: map[string]*FileInfo{}}
}

func (f *fakeStore) Get(hash string) (*FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := f.byHash[hash]
	if rec == nil || rec.Delete {
		return nil, errors.New("sql: no rows")
	}
	cp := *rec
	return &cp, nil
}

func (f *fakeStore) List(offset, limit int) ([]FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []FileInfo
	n := 0
	for _, rec := range f.byHash {
		if rec.Delete {
			continue
		}
		if n < offset {
			n++
			continue
		}
		if len(out) >= limit {
			break
		}
		out = append(out, *rec)
		n++
	}
	return out, nil
}

func (f *fakeStore) ListSince(since int64) ([]FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []FileInfo
	for _, rec := range f.byHash {
		if rec.Seq > since {
			out = append(out, *rec)
		}
	}
	return out, nil
}

func (f *fakeStore) Upsert(hash, path, name string, size int64, deleted bool) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	f.byHash[hash] = &FileInfo{Hash: hash, Path: path, Name: name, Size: size, Seq: f.seq, Delete: deleted}
	return f.seq, nil
}

func (f *fakeStore) Delete(hash string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	if rec := f.byHash[hash]; rec != nil {
		rec.Delete = true
		rec.Seq = f.seq
	}
	return f.seq, nil
}

func mustHash(s string) string {
	h, err := Sum(strings.NewReader(s))
	if err != nil {
		panic(err)
	}
	return h
}

// sum256Hex 是独立 oracle：直接走 crypto/sha256，不经过被测的 Sum。
func sum256Hex(b []byte) string {
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:])
}

// 一个总是失败的 Reader：Sum 必须把 io 错误原样上抛（不能静默返回空摘要）。
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

// 发现背景：Sum 是 7+ 处 `sha256.New()+io.Copy` 收敛成的唯一入口，
// 摘要正确性错了整个内容寻址链路都会悄悄错。
func TestSum(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		// 固定向量（硬编码，独立于 Sum 自身）
		{"empty", "", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"abc", "abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{"peerdrive", "peerdrive", "1588bf2073a10bae06b609b12a651e9b3f9b0c20a6ac6bd0b2b9d48f23b2d16f"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Sum(strings.NewReader(tc.input))
			if err != nil {
				t.Fatalf("Sum: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Sum = %s, want %s", got, tc.want)
			}
			if !IsStrictSHA256(got) {
				t.Fatalf("Sum 输出 %q 不是 64 位小写 hex", got)
			}
		})
	}
	// 用 crypto/sha256 一次性摘要做独立 oracle：特殊字符与超长输入
	// 都必须与标准库逐字节一致（不用 Sum 自证，避免循环断言）。
	for _, s := range []string{
		"中文-路径\\x\\y \t\n",
		strings.Repeat("a", 64*1024+3),
	} {
		want := sum256Hex([]byte(s))
		got, err := Sum(strings.NewReader(s))
		if err != nil {
			t.Fatalf("Sum(len=%d): %v", len(s), err)
		}
		if got != want {
			t.Fatalf("Sum(len=%d) = %s, want %s", len(s), got, want)
		}
	}
	t.Run("read error propagates", func(t *testing.T) {
		got, err := Sum(errReader{})
		if err == nil {
			t.Fatal("Sum with failing reader: want error")
		}
		if got != "" {
			t.Fatalf("Sum on failing reader returned %q, want empty", got)
		}
	})
}

// 发现背景：streaming 等价性——将来若有人"优化"成分块/并行实现，
// 摘要必须逐字节不变，否则存量索引全失效。
func TestSumStreamingEquivalence(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 256*1024+13)
	got, err := Sum(bytes.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 64 {
		t.Fatalf("Sum length = %d, want 64", len(got))
	}
	want := sum256Hex(big)
	if got != want {
		t.Fatalf("Sum = %s, want %s", got, want)
	}
}

// 发现背景：transport 侧对端传来的 hash 必须严格小写——大写会让对
// 小写表的查询落空，且放宽了输入校验（历史踩坑，见 hashutil 注释）。
func TestIsStrictSHA256(t *testing.T) {
	ok := mustHash("abc")
	long := ok + "0"
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"valid", ok, true},
		{"empty", "", false},
		{"uppercase rejected", strings.ToUpper(ok), false},
		{"mixed case rejected", ok[:10] + strings.ToUpper(ok[10:20]) + ok[20:], false},
		{"too short 63", ok[:63], false},
		{"too long 65", long, false},
		{"too long 128", ok + ok, false},
		{"hex-prefixed", "0x" + ok[2:], false},
		{"non-hex char", "g" + ok[1:], false},
		{"space inside", ok[:32] + " " + ok[33:], false},
		{"dash inside", ok[:32] + "-" + ok[33:], false},
		{"non-ascii byte", ok[:32] + "\xff\xff" + ok[34:], false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsStrictSHA256(tc.in); got != tc.want {
				t.Fatalf("IsStrictSHA256(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// 发现背景：HTTP 侧入参容忍大小写（浏览器面板可能提交大写），
// 但宽松版仍必须拒绝长度错/非 hex/空串。
func TestIsValidSHA256(t *testing.T) {
	ok := mustHash("abc")
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"valid", ok, true},
		{"uppercase tolerated", strings.ToUpper(ok), true},
		{"mixed case tolerated", ok[:10] + strings.ToUpper(ok[10:20]) + ok[20:], true},
		{"empty", "", false},
		{"too short 63", ok[:63], false},
		{"too long 65", ok + "0", false},
		{"non-hex", "z" + ok[1:], false},
		{"space inside", ok[:32] + " " + ok[33:], false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsValidSHA256(tc.in); got != tc.want {
				t.Fatalf("IsValidSHA256(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
	// 两个校验器的分工必须稳定：transport 侧严格、HTTP 侧宽松。
	if !IsValidSHA256(strings.ToUpper(ok)) {
		t.Fatal("IsValidSHA256 应当接受大写（HTTP 侧容忍）")
	}
	if IsStrictSHA256(strings.ToUpper(ok)) {
		t.Fatal("IsStrictSHA256 不该接受大写（transport 侧不允许）")
	}
}

// 发现背景：登记路径的语法不变量。对端 sync/create 推 `../../etc/passwd`
// 直接落表会污染索引（清单列得出、一拉 read failed）；而边界判定只认
// 「在允许根内」，对根本不在任何根内的字符串无能为力。这里判的是
// filepath.Clean 之后绝不存在的形态，所以不会拦到合法数据。
func TestSafeStoredPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		// 合法：规范化绝对路径
		{"posix abs", "/data/files/a.txt", true},
		{"windows abs", `C:\Users\a.txt`, true},
		{"root only", "/", true},
		{"dots inside filename", "/data/files/backup..2026.tar", true},
		{"single dot segment", "/data/./a.txt", true}, // Clean 会消掉，但语法上无害
		{"trailing slash", "/data/files/", true},
		// 非法：穿越
		{"empty", "", false},
		{"bare dotdot", "..", false},
		{"leading traversal", "../etc/passwd", false},
		{"slash traversal", "/etc/../etc/passwd", false},
		{"deep traversal", "/data/../../etc/passwd", false},
		{"window traversal", `C:\..\..\Windows\win.ini`, false},
		{"mixed separators", `/data/..\etc/passwd`, false},
		{"dotdot at end", "/data/files/..", false},
		// 非法：控制字符
		{"nul byte", "/data/a\x00b.txt", false},
		// 超长但合法（不变量是语法，不是长度）
		{"long but legal", "/data/" + strings.Repeat("x", 4096) + ".txt", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := safeStoredPath(tc.in); got != tc.want {
				t.Fatalf("safeStoredPath(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// 发现背景：NewMap 的注释声称 store 不能为 nil，但半成品没有实现这个
// 守卫——nil 会在方法调用处变成 panic(nil pointer dereference)，信息
// 不如构造期明确。
func TestNewMap(t *testing.T) {
	t.Run("nil store panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("NewMap(nil) 应当 panic")
			}
		}()
		NewMap(nil)
	})
	t.Run("valid store", func(t *testing.T) {
		m := NewMap(newFakeStore())
		if m == nil || m.store == nil {
			t.Fatal("NewMap 返回了空 Map")
		}
	})
}

// 发现背景：Map 方法在非法输入时必须短路、且**不落到存储层**——
// 覆盖构造（空）/超长/特殊字符三类输入。
func TestMap_Info(t *testing.T) {
	st := newFakeStore()
	ok := mustHash("abc")
	if _, err := st.Upsert(ok, "/data/a.txt", "a.txt", 3, false); err != nil {
		t.Fatal(err)
	}
	m := NewMap(st)

	t.Run("hit", func(t *testing.T) {
		fi, err := m.Info(ok)
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		if fi.Path != "/data/a.txt" || fi.Name != "a.txt" || fi.Size != 3 || fi.Delete {
			t.Fatalf("Info = %+v, want /data/a.txt record", fi)
		}
	})
	t.Run("miss returns store error", func(t *testing.T) {
		_, err := m.Info(mustHash("nope"))
		if err == nil {
			t.Fatal("Info(miss): want error")
		}
	})
	bad := []string{"", "0", "abc", strings.ToUpper(ok), ok + "0", ok + ok,
		"0x" + ok[2:], ok[:32] + " " + ok[33:], "\x00"}
	t.Run("invalid hash rejected with ErrInvalidHash and never reaches store", func(t *testing.T) {
		for _, bad := range bad {
			_, err := m.Info(bad)
			if !errors.Is(err, ErrInvalidHash) {
				t.Fatalf("Info(%q) err = %v, want ErrInvalidHash", bad, err)
			}
		}
	})
}

// 发现背景：DownloadPath 是 download 服务的取路径入口，非法 hash 不该
// 走到磁盘层。
func TestMap_DownloadPath(t *testing.T) {
	st := newFakeStore()
	ok := mustHash("abc")
	st.Upsert(ok, "/data/a.txt", "a.txt", 3, false)
	m := NewMap(st)
	if p, err := m.DownloadPath(ok); err != nil || p != "/data/a.txt" {
		t.Fatalf("DownloadPath = %q, %v; want /data/a.txt", p, err)
	}
	if _, err := m.DownloadPath("bad"); !errors.Is(err, ErrInvalidHash) {
		t.Fatalf("DownloadPath(bad) err = %v, want ErrInvalidHash", err)
	}
}

// 发现背景：limit 来自远端 list verb（可任意大），直接进 SQL LIMIT 会
// 全表物化 → 内存 DoS；钳制必须在门面做（repository 层只兜 limit<=0）。
func TestMap_List(t *testing.T) {
	st := newFakeStore()
	m := NewMap(st)
	for i := 0; i < 5; i++ {
		h := mustHash(fmt.Sprintf("file-%d", i))
		st.Upsert(h, fmt.Sprintf("/d/f%d", i), fmt.Sprintf("f%d", i), int64(i), false)
	}
	t.Run("negative limit clamped to 1000", func(t *testing.T) {
		rows, err := m.List(0, -1)
		if err != nil || len(rows) != 5 {
			t.Fatalf("List(0,-1) = %d rows, %v; want 5", len(rows), err)
		}
	})
	t.Run("zero limit clamped to 1000", func(t *testing.T) {
		rows, err := m.List(0, 0)
		if err != nil || len(rows) != 5 {
			t.Fatalf("List(0,0) = %d rows, %v; want 5", len(rows), err)
		}
	})
	t.Run("oversized limit clamped to 1000", func(t *testing.T) {
		rows, err := m.List(0, 999999)
		if err != nil || len(rows) != 5 {
			t.Fatalf("List(0,999999) = %d rows, %v; want 5", len(rows), err)
		}
	})
	t.Run("offset pagination", func(t *testing.T) {
		rows, err := m.List(2, 2)
		if err != nil || len(rows) != 2 {
			t.Fatalf("List(2,2) = %d rows, %v; want 2", len(rows), err)
		}
	})
	t.Run("tombstone hidden from List", func(t *testing.T) {
		st.Delete(mustHash("file-1"))
		rows, err := m.List(0, 100)
		if err != nil || len(rows) != 4 {
			t.Fatalf("List after delete = %d rows, %v; want 4", len(rows), err)
		}
	})
}

// 发现背景：seq 是增量同步游标，delete 的 tombstone 必须带序，
// 否则对端 sync 跟踪不到删除事件（历史缺陷 L6）。
func TestMap_SyncSince(t *testing.T) {
	st := newFakeStore()
	m := NewMap(st)
	h1 := mustHash("one")
	h2 := mustHash("two")
	st.Upsert(h1, "/d/one", "one", 1, false)
	st.Upsert(h2, "/d/two", "two", 2, false)
	rows, last, err := m.SyncSince(0)
	if err != nil || last != 2 || len(rows) != 2 {
		t.Fatalf("SyncSince(0) = %d rows, last %d, %v; want 2/2", len(rows), last, err)
	}
	st.Delete(h1)
	rows, last, err = m.SyncSince(2)
	if err != nil || last != 3 || len(rows) != 1 || !rows[0].Delete || rows[0].Hash != h1 {
		t.Fatalf("SyncSince(2) = %+v, last %d, %v; want h1 tombstone, last 3", rows, last, err)
	}
	// 游标不推进：区间为空时 last 应回显入参 since。
	rows, last, err = m.SyncSince(3)
	if err != nil || last != 3 || len(rows) != 0 {
		t.Fatalf("SyncSince(3) = %d rows, last %d, %v; want 0/3", len(rows), last, err)
	}
}

// 发现背景：对端 sync 数据不可信——非法 hash、穿越路径必须跳过（宁丢
// 不错登记），合法项照常落表，tombstone 生效。
func TestMap_ApplySync(t *testing.T) {
	st := newFakeStore()
	m := NewMap(st)
	trav := "/data/../../etc/passwd"
	n, err := m.ApplySync([]FileInfo{
		{Hash: mustHash("ok1"), Path: "/d/ok1", Name: "ok1", Size: 1},
		{Hash: "not-a-hash", Path: "/d/bad", Name: "bad"},        // 非法 hash：跳过
		{Hash: mustHash("gone"), Delete: true},                   // tombstone（不存在也算一次事件）
		{Hash: mustHash("ok2"), Path: ""},                        // 空路径：跳过
		{Hash: mustHash("evil"), Path: trav, Name: "p"},          // 穿越：跳过
		{Hash: strings.ToUpper(mustHash("upper")), Path: "/d/u"}, // 大写 hash：跳过
		{Hash: mustHash("dots"), Path: "/d/backup..2026.tar"},    // 合法：文件名带点
	})
	if err != nil {
		t.Fatalf("ApplySync: %v", err)
	}
	if n != 3 {
		t.Fatalf("ApplySync applied %d, want 3 (ok1 upsert, gone tombstone, dots upsert)", n)
	}
	if fi, err := m.Info(mustHash("ok1")); err != nil || fi.Path != "/d/ok1" {
		t.Fatalf("ok1 not applied: %+v, %v", fi, err)
	}
	if _, err := m.Info(mustHash("evil")); err == nil {
		t.Fatal("穿越路径不应被登记")
	}
	if _, err := m.Info(mustHash("ok2")); err == nil {
		t.Fatal("空路径不应被登记")
	}
}

// 发现背景：Upsert 是本地登记入口（create/upload 完成后调用），
// 必须与远端 ApplySync 共用同一套校验，否则两个入口语义分叉。
func TestMap_Upsert(t *testing.T) {
	st := newFakeStore()
	m := NewMap(st)
	ok := mustHash("abc")
	seq, err := m.Upsert(ok, "/d/a", "a", 3, false)
	if err != nil || seq != 1 {
		t.Fatalf("Upsert = seq %d, %v; want 1", seq, err)
	}
	t.Run("invalid hash rejected", func(t *testing.T) {
		if _, err := m.Upsert("bad", "/d/x", "x", 1, false); !errors.Is(err, ErrInvalidHash) {
			t.Fatalf("Upsert(bad hash) err = %v, want ErrInvalidHash", err)
		}
		if _, err := m.Upsert(strings.ToUpper(ok), "/d/x", "x", 1, false); !errors.Is(err, ErrInvalidHash) {
			t.Fatalf("Upsert(uppercase hash) err = %v, want ErrInvalidHash", err)
		}
	})
	t.Run("traversal path rejected with ErrInvalidPath", func(t *testing.T) {
		for _, p := range []string{"", "../etc/passwd", `C:\..\win.ini`, "/d/\x00.txt"} {
			if _, err := m.Upsert(ok, p, "x", 1, false); !errors.Is(err, ErrInvalidPath) {
				t.Fatalf("Upsert(%q) err = %v, want ErrInvalidPath", p, err)
			}
		}
	})
	t.Run("rejection does not touch the store", func(t *testing.T) {
		rows, _ := m.List(0, 100)
		if len(rows) != 1 {
			t.Fatalf("after rejected upserts: %d rows, want 1 (only the valid one)", len(rows))
		}
	})
	seq, err = m.Delete(ok)
	if err != nil || seq != 2 {
		t.Fatalf("Delete = seq %d, %v; want 2", seq, err)
	}
	if _, err := m.Delete("bad"); !errors.Is(err, ErrInvalidHash) {
		t.Fatalf("Delete(bad) err = %v, want ErrInvalidHash", err)
	}
}

// 发现背景：Map 自身无状态（状态都在 Store 里），但门面方法必须能在
// 并发下与 Store 并发调用混跑——CI 的 peerjs job 不带 -race，
// 这条用例是本地 -race 的唯一竞态防线。
func TestMap_Concurrent(t *testing.T) {
	st := newFakeStore()
	m := NewMap(st)
	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h := mustHash(fmt.Sprintf("g%d", i))
			m.Upsert(h, fmt.Sprintf("/d/g%d", i), fmt.Sprintf("g%d", i), int64(i), false)
			for j := 0; j < 20; j++ {
				_, _ = m.Info(h)
				_, _ = m.DownloadPath(h)
				_, _ = m.List(0, 10)
				_, _, _ = m.SyncSince(0)
				_, _ = m.ApplySync([]FileInfo{{Hash: h, Path: "/d/g", Name: "g"}})
			}
		}(i)
	}
	wg.Wait()
	if rows, _ := m.List(0, 1000); len(rows) != n {
		t.Fatalf("after concurrent ops: %d rows, want %d", len(rows), n)
	}
}

var _ Store = (*fakeStore)(nil)
