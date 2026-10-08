// Package hashmap 提供内容寻址（sha256 → 路径/文件信息）映射的核心逻辑：
//
//   - 计算：Sum 把任意 io.Reader 的内容算成 64 位小写 sha256 hex。目标是让它
//     成为唯一计算入口，取代散落的 `sha256.New()+io.Copy` 重复模式——现存 8 处
//     （transport/file_index.go hashReader、transport/outbound.go×2、
//     service/file_service.go×2、service/peerpull.go、extractor/extractor.go、
//     internal/source/url.go）本轮**不改**，逐处改接见后续 PR（每处都要确认
//     读的是同一份字节流，不能顺手改语义）。
//   - 校验：IsStrictSHA256 / IsValidSHA256。此前这两份语义在 pkg/hashutil 各
//     写一遍（同义双份，改一处漏一处），现已迁入本包成为唯一定义处；hashutil
//     退为薄委托 + 只留 ipfs CID 转换（校验与 CID 转换是两件事，拆开 hashmap
//     才能只依赖标准库、不被 ipfs 依赖绑住）。
//   - 查询/登记门面：Map + Store 接口。Map 承载「非法 hash 拒绝、路径不变量、
//     limit 钳制、sync 游标、tombstone 分发」等纯映射语义；存储（SQLite
//     file_index 表，repository 包）由调用方以 Store 实现注入，使映射逻辑
//     与持久化解耦、可脱离数据库单测。
//
// 模块定位：这是主模块 internal 的纯逻辑包，只 import 标准库——不 import
// repository/transport，也不被任何独立 go.mod 发布库使用（peerjs/signalserver/
// p2p_bt 均无此逻辑），故与 signalframe 不同：不需要独立 go.mod，
// 放 back/internal/ 即可。
package hashmap

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrInvalidHash 是非法 hash 输入的统一哨兵（Map 方法在非法输入时包装它）。
var ErrInvalidHash = errors.New("invalid sha256 hash")

// ErrInvalidPath 是登记路径不满足存储不变量的哨兵（Map.Upsert 包装它）。
var ErrInvalidPath = errors.New("invalid stored path")

// FileInfo 是 hash → 路径映射对外返回的文件信息（json 与帧协议 verb 对齐）。
type FileInfo struct {
	Hash   string `json:"hash"`
	Path   string `json:"path"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Seq    int64  `json:"seq"`
	Delete bool   `json:"delete,omitempty"` // sync 用：tombstone
}

// Sum 计算 r 的内容 sha256，返回 64 位小写 hex。读失败时返回错误。
func Sum(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// IsStrictSHA256 严格校验：只接受 64 位小写十六进制 sha256（transport 层对端
// 传来的 hash 用它——允许大写会导致对小写表查询落空、且放宽输入校验）。
func IsStrictSHA256(s string) bool {
	if s == "" || len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// IsValidSHA256 宽松校验：小写化后为 64 位十六进制即通过（HTTP 侧入参用；
// 与 IsStrictSHA256 的差异是有意保留：HTTP 侧容忍大小写，transport 侧不允许）。
func IsValidSHA256(s string) bool {
	s = lowerASCII(s)
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// lowerASCII lowercases ASCII letters only (avoids depending on unicode pkg;
// hash inputs are hex digits + optionally uppercase A-F).
func lowerASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

// Store 是 hash → 路径映射的持久化抽象。SQLite file_index 表在
// repository 包（UpsertFileIndex/GetFileIndex/ListFileIndex/DeleteFileIndex，
// 函数式 API，尚无现成类型实现本接口）——把 transport.FileIndexService
// 改接成 Store 实现属后续 PR；测试用内存实现即可单测映射语义。
type Store interface {
	// Get 按 hash 查映射（不含删除 tombstone）。
	Get(hash string) (*FileInfo, error)
	// List 列出全部未删除映射（offset/limit 分页）。
	List(offset, limit int) ([]FileInfo, error)
	// ListSince 返回 seq 大于 since 的全部变更（含删除 tombstone，增量同步用）。
	ListSince(since int64) ([]FileInfo, error)
	// Upsert 登记/更新映射，返回新 seq。
	Upsert(hash, path, name string, size int64, deleted bool) (int64, error)
	// Delete 逻辑删除映射（sync tombstone），返回新 seq。
	Delete(hash string) (int64, error)
}

// Map 是映射门面：校验 + 查询/登记语义，存储由 Store 提供。
type Map struct {
	store Store
}

// NewMap 创建映射门面。store 不能为 nil——nil 会在任何方法调用上得到
// nil 指针 panic，信息不如这里明确。
func NewMap(store Store) *Map {
	if store == nil {
		panic("hashmap: NewMap(store) requires a non-nil Store")
	}
	return &Map{store: store}
}

// Info 按 hash 返回文件信息（download 前先 info 拿 name/path/size）。
// 非法 hash 一律拒绝（ErrInvalidHash），不落到存储层。
func (m *Map) Info(hash string) (*FileInfo, error) {
	if !IsStrictSHA256(hash) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidHash, hash)
	}
	return m.store.Get(hash)
}

// DownloadPath 按 hash 返回可读取的本地绝对路径（download 服务用）。
func (m *Map) DownloadPath(hash string) (string, error) {
	f, err := m.Info(hash)
	if err != nil {
		return "", err
	}
	return f.Path, nil
}

// List 列出全部未删除映射。
//
// 防御：limit 来自远端 list verb（可任意大），直接进 SQL LIMIT 会全表物化 →
// 内存 DoS。这里钳制上限（repository 层只兜 limit<=0，钳制必须在门面做）。
func (m *Map) List(offset, limit int) ([]FileInfo, error) {
	if limit <= 0 {
		limit = 1000
	}
	if limit > 1000 {
		limit = 1000
	}
	return m.store.List(offset, limit)
}

// SyncSince 增量同步：返回 seq 之后的全部变更（含删除 tombstone）与最新游标。
func (m *Map) SyncSince(since int64) ([]FileInfo, int64, error) {
	rows, err := m.store.ListSince(since)
	if err != nil {
		return nil, 0, err
	}
	last := since
	for _, r := range rows {
		if r.Seq > last {
			last = r.Seq
		}
	}
	return rows, last, nil
}

// ApplySync 应用对端同步来的变更（本地 upsert/tombstone）。对端数据不可信：
// 非法 hash 或越界路径一律跳过（宁可丢不可错登记），只有存储层错误才上抛。
func (m *Map) ApplySync(files []FileInfo) (int, error) {
	n := 0
	for _, f := range files {
		if !IsStrictSHA256(f.Hash) {
			continue
		}
		if f.Delete {
			if _, err := m.store.Delete(f.Hash); err != nil {
				return n, err
			}
			n++
			continue
		}
		if f.Path == "" {
			continue
		}
		// 走同一套校验：越界路径在登记时就掐掉，而不是等"清单列得出、
		// 一拉 read failed"时才发现。
		if _, err := m.Upsert(f.Hash, f.Path, f.Name, f.Size, false); err != nil {
			if errors.Is(err, ErrInvalidHash) || errors.Is(err, ErrInvalidPath) {
				continue
			}
			return n, err
		}
		n++
	}
	return n, nil
}

// Delete 逻辑删除映射（同步用 tombstone），返回新 seq。
func (m *Map) Delete(hash string) (int64, error) {
	if !IsStrictSHA256(hash) {
		return 0, fmt.Errorf("%w: %q", ErrInvalidHash, hash)
	}
	return m.store.Delete(hash)
}

// Upsert 登记/更新映射（create/upload 完成后调用），返回新 seq。
// 两个入口（本地 Upsert 与远端 ApplySync）共用 validateUpsert，校验语义不分叉。
func (m *Map) Upsert(hash, path, name string, size int64, deleted bool) (int64, error) {
	if err := validateUpsert(hash, path); err != nil {
		return 0, err
	}
	return m.store.Upsert(hash, path, name, size, deleted)
}

// validateUpsert 是登记前的输入校验：hash 必须严格合法，path 必须满足
// 存储不变量。返回包装过的 ErrInvalidHash / ErrInvalidPath 哨兵。
func validateUpsert(hash, path string) error {
	if !IsStrictSHA256(hash) {
		return fmt.Errorf("%w: %q", ErrInvalidHash, hash)
	}
	if !safeStoredPath(path) {
		return fmt.Errorf("%w: %q", ErrInvalidPath, path)
	}
	return nil
}

// safeStoredPath 校验落表路径的**语法不变量**：非空、无 NUL、无 ".." 路径段。
//
// 判的是字符串，不是文件系统：这些条件任何规范化后的绝对路径都满足
// （filepath.Clean 会消掉 ".." 段），所以拦不到合法数据。
//
// 为什么要在这里拦：对端 sync/create 推来的路径若直接落表，`../../etc/passwd`
// 这类字符串会变成一条「清单列得出、一拉 read failed」的坏映射，在边界判定
// 之前就已经污染了索引；而边界判定（storage 根 / SHARE_DIRS / download 根）
// 只认"在某个允许根内"，对根本不在任何根内的字符串无能为力。
//
// 注意：这里**不做**完整边界判定——那需要允许根集合，仍属 internal/pathutil
// 与调用方。分隔符 / 与 \ 两种都判，不假设平台。
func safeStoredPath(p string) bool {
	if p == "" || strings.IndexByte(p, 0) >= 0 {
		return false
	}
	for _, seg := range strings.FieldsFunc(p, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if seg == ".." {
			return false
		}
	}
	return true
}
