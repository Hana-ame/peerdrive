package transport

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"peerdrive/internal/log"
	"peerdrive/internal/repository"
	"peerdrive/pkg/hashutil"
)

// FileIndexService 本地文件索引服务：sha256 → 绝对路径 映射（SQLite 持久化）。
// 帧协议 verb（经 Session 传输，WS/WebRTC 同一套）：
//
//	create   {type:"create", path}            → created {hash,size,name,path}
//	upload   {type:"upload", name,size}       流式：meta → data×N → uploaded {hash,path}
//	list     {type:"list", offset?,limit?}    → list-resp {files,total}
//	info     {type:"info", hash}              → info-resp {hash,size,name,path,seq}
//	download 复用现有 req（返回文件信息由 info 承担）
//	sync     {type:"sync", seq}               → sync-resp {files,lastSeq}（metadata 增量同步）
type FileIndexService struct {
	uploadDir string // upload 默认保存位置
	rootDir   string // create 允许登记的文件根目录（绝对路径，构造时 EvalSymlinks 解析）

	upMu    sync.Mutex
	uploads map[string]*UploadSession // name → 分片上传会话（多 source/续传共用）
}

// NewFileIndexService 创建索引服务。uploadDir 为上传文件默认落盘目录，
// 同时也是 create 登记的允许根目录（H2 安全边界，见 IsPathAllowed）。
func NewFileIndexService(uploadDir string) *FileIndexService {
	if uploadDir == "" {
		uploadDir = "./files"
	}
	root, err := filepath.Abs(uploadDir)
	if err != nil {
		root = uploadDir
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	svc := &FileIndexService{uploadDir: uploadDir, rootDir: root, uploads: make(map[string]*UploadSession)}
	go svc.reapUploads()
	return svc
}

// IsPathAllowed 校验 path 是否位于允许根目录内（绝对路径 + 符号链接解析后）。
// 安全边界（H2 任意文件读取修复）：create 登记与 serveFile 回传只允许根目录内
// 文件——之前接受任意绝对路径，对端可 `create /etc/shadow` 拿 hash 后 `req`
// 读取，info verb 还会泄露路径。符号链接解析防「根目录内软链 → 根外目标」。
func (s *FileIndexService) IsPathAllowed(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	rel, err := filepath.Rel(s.rootDir, abs)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}

// reapUploads 过期会话清理：10 分钟无活动删除（含目标文件，防磁盘耗尽）。
func (s *FileIndexService) reapUploads() {
	for range time.Tick(5 * time.Minute) {
		s.upMu.Lock()
		for name, sess := range s.uploads {
			sess.mu.Lock()
			idle := time.Since(sess.last) > 10*time.Minute
			if idle {
				// M7：持 sess.mu 判定 + 标记 aborted + 摘除句柄，与 WriteAt/Complete
				// 串行——修复「判定 idle 后 Abort，而并发分片正 WriteAt 拿到已关闭
				// 句柄 → 上传莫名失败」的竞态（之前先解锁再 Abort，存在检查窗口）
				sess.aborted = true
				f := sess.file
				sess.file = nil
				p := sess.path
				sess.mu.Unlock()
				_ = f.Close()
				_ = os.Remove(p)
				delete(s.uploads, name)
				log.LogInfo("file-index: upload session reaped name=%s", name)
			} else {
				sess.mu.Unlock()
			}
		}
		s.upMu.Unlock()
	}
}

// uploadSessions 键：文件名（sanitize 后），值：会话。
var _ = uploadChunkSize

// FileInfo 对外返回的文件信息。
type FileInfo struct {
	Hash   string `json:"hash"`
	Path   string `json:"path"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Seq    int64  `json:"seq"`
	Delete bool   `json:"delete,omitempty"` // sync 用：tombstone
}

// Create 登记外部文件：计算 sha256，落盘映射（不复制文件，仅索引绝对路径）。
// 安全：只允许根目录内文件（H2），防止对端登记任意绝对路径后经 req 读取。
func (s *FileIndexService) Create(path string) (*FileInfo, error) {
	if !s.IsPathAllowed(path) {
		return nil, fmt.Errorf("path outside allowed root: %s", path)
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}
	h, err := hashFile(path)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	seq, err := repository.UpsertFileIndex(h, abs, filepath.Base(path), st.Size(), false)
	if err != nil {
		return nil, err
	}
	log.LogInfo("file-index: create hash=%s size=%d path=%s", h, st.Size(), abs)
	return &FileInfo{Hash: h, Path: abs, Name: filepath.Base(path), Size: st.Size(), Seq: seq}, nil
}

// uploadChunkSize 上传位图粒度（分片对齐单位）。
const uploadChunkSize = 64 * 1024

// UploadSession 分片上传会话：目标文件 + 到位位图（64KB chunk 粒度）。
// 支持：
//   - 多 source：多个连接/节点并发 WriteAt 不同分片，位图合并，全满即完成
//   - 断点续传：同 name 复用会话；进程重启后按文件大小重建位图（[0,min(size,fsize)] 视为已写，
//     不精确由最终 sha256 校验兜底）
//
// 位图语义：chunk i 到位 = 字节 [i*64KB, (i+1)*64KB) 已写。Commit 仅在位图全满时进行。
type UploadSession struct {
	mu      sync.Mutex
	name    string
	path    string
	file    *os.File
	size    int64 // 声明总大小
	bitmap  []uint64
	seq     int64
	last    time.Time // 最后活动时间（过期清理用）
	aborted bool      // reap 摘除句柄后置位：WriteAt/Complete 见之即错（M7 竞态修复）
}

// DeclaredSize 返回声明总大小（BeginUpload 同名复用一致性校验用）。
func (u *UploadSession) DeclaredSize() int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.size
}

// BeginUpload 开始/继续一个分片上传会话（同 name 幂等复用）。
func (s *FileIndexService) BeginUpload(name string, size int64) (*UploadSession, error) {
	if size < 0 || size > 8*1024*1024*1024 { // 上限 8GB，防恶意声明
		return nil, fmt.Errorf("invalid size %d", size)
	}
	s.upMu.Lock()
	defer s.upMu.Unlock()
	if sess, ok := s.uploads[name]; ok {
		sess.last = time.Now()
		// M7：同名会话复用必须 size 一致——位图按旧 size 建，声明不一致会导致
		// 续传偏移错乱、末 chunk 判满错误。不一致直接拒绝（多 source 本就应同 size）
		if sess.DeclaredSize() != size {
			return nil, fmt.Errorf("upload session %q already exists with size %d (requested %d)", name, sess.DeclaredSize(), size)
		}
		return sess, nil
	}
	if err := os.MkdirAll(s.uploadDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(s.uploadDir, sanitizeName(name))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open upload file: %w", err)
	}
	// 位图按 64 chunk/word 分配（曾按 chunk 数分配导致末 word 判满逻辑失效）
	totalChunks := (size + uploadChunkSize - 1) / uploadChunkSize
	sess := &UploadSession{
		name:   name,
		path:   path,
		file:   f,
		size:   size,
		bitmap: make([]uint64, (totalChunks+63)/64),
		last:   time.Now(),
	}
	// 断点续传：已有文件大小 → 重建位图（[0, min(size, fsize)) 视为已写）。
	// 坑：空洞/错序可能被误标已写，最终 Commit 的 sha256 校验兜底（不匹配则整体失败）。
	if st, err := f.Stat(); err == nil {
		rebuilt := st.Size()
		if rebuilt > size {
			rebuilt = size
		}
		for i := int64(0); i < rebuilt/uploadChunkSize; i++ {
			sess.setBit(i)
		}
		if rebuilt%uploadChunkSize != 0 {
			sess.setBit(rebuilt / uploadChunkSize)
		}
	}
	s.uploads[name] = sess
	log.LogInfo("file-index: upload session begin name=%s size=%d path=%s", name, size, path)
	return sess, nil
}

// WriteAt 写入一个分片（offset 需 chunk 对齐，长度 ≤ chunk 粒度由帧协议保证）。
func (u *UploadSession) WriteAt(offset int64, data []byte) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.last = time.Now()
	if u.aborted || u.file == nil {
		// 会话已被 reap 摘除句柄（M7）：返回明确错误而非对已关闭句柄写入的
		// 莫名失败——reap 与 WriteAt 持同一把锁串行，无「正在写时被关闭」窗口
		return fmt.Errorf("upload session aborted")
	}
	if offset < 0 || offset+int64(len(data)) > u.size {
		return fmt.Errorf("write out of range: offset=%d len=%d size=%d", offset, len(data), u.size)
	}
	if _, err := u.file.WriteAt(data, offset); err != nil {
		return fmt.Errorf("write at %d: %w", offset, err)
	}
	// 置位覆盖的分片（一次写可能跨 chunk 边界，按字节区间逐 chunk 置位）
	start := offset / uploadChunkSize
	end := (offset + int64(len(data)) + uploadChunkSize - 1) / uploadChunkSize
	for i := start; i < end; i++ {
		u.setBit(i)
	}
	return nil
}

// ContiguousOffset 返回连续已写长度（resume 起点）：从 0 到第一个未到位 chunk。
func (u *UploadSession) ContiguousOffset() int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	for i, w := range u.bitmap {
		// 该 word 内第一个未置位 bit
		for b := 0; b < 64; b++ {
			if w&(1<<b) == 0 {
				return int64(i*64+b) * uploadChunkSize
			}
		}
	}
	return u.size
}

// Complete 检查位图是否全满；全满则计算 sha256、登记映射并返回文件信息。
// 内容校验兜底：hash 与声明字节对不上视为失败（Abort）。
func (u *UploadSession) Complete() (bool, *FileInfo, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.last = time.Now()
	if u.aborted || u.file == nil {
		return false, nil, fmt.Errorf("upload session aborted")
	}
	// 位图全满判定：除最后一个 word 外需全 64 位，末 word 只需
	// (总 chunk 数 mod 64) 位——曾误判尾部块未满导致永不完成。
	totalChunks := (u.size + uploadChunkSize - 1) / uploadChunkSize
	full := true
	for i, w := range u.bitmap {
		want := uint64(^uint64(0))
		if i == len(u.bitmap)-1 {
			bits := totalChunks - int64(i)*64
			if bits < 64 {
				want = (1 << bits) - 1
			}
		}
		if w != want {
			full = false
			break
		}
	}
	if !full {
		return false, nil, nil
	}
	if err := u.file.Sync(); err != nil {
		return false, nil, err
	}
	h, err := hashFile(u.path)
	if err != nil {
		return false, nil, err
	}
	u.seq = 0
	seq, err := repository.UpsertFileIndex(h, u.path, u.name, u.size, false)
	if err != nil {
		return false, nil, err
	}
	u.seq = seq
	log.LogInfo("file-index: upload complete hash=%s size=%d path=%s", h, u.size, u.path)
	return true, &FileInfo{Hash: h, Path: u.path, Name: u.name, Size: u.size, Seq: seq}, nil
}

// Abort 中止会话并删除目标文件（幂等；reap 摘除句柄后调用无副作用）。
func (u *UploadSession) Abort() {
	u.mu.Lock()
	if u.aborted {
		u.mu.Unlock()
		return
	}
	u.aborted = true
	f := u.file
	u.file = nil
	p := u.path
	u.mu.Unlock()
	_ = f.Close()
	_ = os.Remove(p)
}

// Close 关闭文件句柄（不删除文件——保留续传）。
func (u *UploadSession) Close() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.file != nil {
		_ = u.file.Close()
	}
}

func (u *UploadSession) setBit(i int64) {
	u.bitmap[i/64] |= 1 << (i % 64)
}

// uploadSessions 键：文件名（sanitize 后），值：会话。
var _ = uploadChunkSize

// List 列出全部未删除映射。
func (s *FileIndexService) List(offset, limit int) ([]FileInfo, error) {
	// 防御：limit 来自远端 list verb（可任意大），直接进 SQL LIMIT 会全表物化 → 内存 DoS。
	// repository 层只兜 limit<=0，这里 clamp 上限。
	if limit <= 0 {
		limit = 1000
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := repository.ListFileIndex(offset, limit)
	if err != nil {
		return nil, err
	}
	out := make([]FileInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, FileInfo{Hash: r.Hash, Path: r.Path, Name: r.Name, Size: r.Size, Seq: r.Seq})
	}
	return out, nil
}

// Info 按 hash 返回文件信息（download 前先 info 拿 name/path/size）。
func (s *FileIndexService) Info(hash string) (*FileInfo, error) {
	if !hashutil.IsStrictSHA256(hash) {
		return nil, fmt.Errorf("invalid hash %q", hash)
	}
	f, err := repository.GetFileIndex(hash)
	if err != nil {
		return nil, err
	}
	return &FileInfo{Hash: f.Hash, Path: f.Path, Name: f.Name, Size: f.Size, Seq: f.Seq}, nil
}

// DownloadPath 按 hash 返回可读取的本地绝对路径（download 服务用）。
func (s *FileIndexService) DownloadPath(hash string) (string, error) {
	f, err := s.Info(hash)
	if err != nil {
		return "", err
	}
	return f.Path, nil
}

// Delete 逻辑删除映射（同步用 tombstone），返回新 seq。
// L6：seq 是增量同步游标——delete 的 tombstone 必须带序，对端 sync 才能
// 跟踪到删除事件（原实现丢弃了 DeleteFileIndex 的 seq）。
func (s *FileIndexService) Delete(hash string) (int64, error) {
	if !hashutil.IsStrictSHA256(hash) {
		return 0, fmt.Errorf("invalid hash %q", hash)
	}
	return repository.DeleteFileIndex(hash)
}

// SyncSince 增量同步：返回 seq 之后的全部变更（含删除 tombstone）。
func (s *FileIndexService) SyncSince(since int64) ([]FileInfo, int64, error) {
	rows, err := repository.ListFileIndexSince(since)
	if err != nil {
		return nil, 0, err
	}
	out := make([]FileInfo, 0, len(rows))
	last := since
	for _, r := range rows {
		if r.Seq > last {
			last = r.Seq
		}
		out = append(out, FileInfo{
			Hash: r.Hash, Path: r.Path, Name: r.Name, Size: r.Size, Seq: r.Seq,
			Delete: r.Deleted,
		})
	}
	return out, last, nil
}

// ApplySync 应用对端同步来的变更（本地 upsert/tombstone）。
func (s *FileIndexService) ApplySync(files []FileInfo) (int, error) {
	n := 0
	for _, f := range files {
		if !hashutil.IsStrictSHA256(f.Hash) {
			continue
		}
		if f.Delete {
			_, err := repository.DeleteFileIndex(f.Hash)
			if err != nil {
				return n, err
			}
		} else if f.Path != "" {
			_, err := repository.UpsertFileIndex(f.Hash, f.Path, f.Name, f.Size, false)
			if err != nil {
				return n, err
			}
		}
		n++
	}
	return n, nil
}

// hashFile 计算文件 sha256。
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// sanitizeName 文件名净化（防路径穿越）。
func sanitizeName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "" || name == "." || name == "/" {
		return "upload.bin"
	}
	return name
}

// UploadChunkSizeForTest 供集成测试引用分片粒度。
func UploadChunkSizeForTest() int { return uploadChunkSize }
