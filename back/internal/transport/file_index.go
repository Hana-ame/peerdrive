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
	"peerdrive/internal/pathutil"
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

	// readRoots 运营者额外声明的**可读**根目录（storage 根 + PEERDRIVE_SHARE_DIRS）。
	//
	// 为什么要和 rootDir 分开：rootDir 是**写/登记边界**——对端可以带任意 path 走
	// create，绝不能让它登记到下载目录之外。而读取侧是另一回事：运营者自己声明
	// 「/data/media 是我的共享目录」并登记了它，就该能对外提供。两套判定混用时
	// 会出现「登记成功、清单列得出、对端一拉 read failed」——读取侧把它判成越权，
	// 回退到并不存在的内容寻址副本。见 AddReadRoot / IsPathReadable。
	rootMu    sync.RWMutex
	readRoots []string

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

// IsPathAllowed 校验 path 是否位于**登记/写入**的允许根目录内。
// 安全边界（H2 任意文件读取修复）：create 只允许登记根目录内文件——之前接受任意
// 绝对路径，对端可 `create /etc/shadow` 拿 hash 后 `req` 读取，info verb 还会
// 泄露路径。符号链接解析防「根目录内软链 → 根外目标」。
//
// 它同时也是**上传/落盘**的边界，因此不要把共享目录塞进来：那等于允许对端把
// 文件写进你对外共享的目录。对外可读请看 IsPathReadable。
func (s *FileIndexService) IsPathAllowed(path string) bool {
	return pathutil.Within(s.rootDir, path)
}

// writeRoots 写/落盘边界：只有 rootDir 一个。
//
// 什么时候用它：任何**产生或改动文件**的动作（WriteFile / BeginUpload / 删临时
// 文件）都得过这里。判定（IsPathAllowed）与落盘必须落在同一个根上，否则又会
// 出现"判定按 A、写入按 B"的裂缝——历史上出过事的就是这种。
func (s *FileIndexService) writeRoots() []string {
	return []string{s.rootDir}
}

// AddReadRoot 追加一个运营者声明的可读根目录（storage 根、PEERDRIVE_SHARE_DIRS）。
//
// 语义：这些目录里的文件**可以对外提供**，但对端仍然**不能登记/写入**它们
// （写边界仍只有 rootDir）。空串与非法路径直接忽略——配置写错时宁可少给。
func (s *FileIndexService) AddReadRoot(dir string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		log.LogWarn("file-index: ignore invalid read root %q: %v", dir, err)
		return
	}
	s.rootMu.Lock()
	s.readRoots = append(s.readRoots, abs)
	s.rootMu.Unlock()
	log.LogInfo("file-index: read root added %s", abs)
}

// IsPathReadable 校验 path 是否**允许对外读取**：下载根（写边界）之内，
// 或运营者额外声明过的可读根之内。
//
// 用它取代读取侧的 IsPathAllowed（serveFile / LocalSource.resolvePath）。
// 登记侧继续用 IsPathAllowed——两者不能合并，理由见 AddReadRoot 注释。
func (s *FileIndexService) IsPathReadable(path string) bool {
	return pathutil.WithinAny(s.readRootsAll(), path)
}

// readRootsAll 读取边界的全部根目录：下载根 + 运营者声明的可读根。
func (s *FileIndexService) readRootsAll() []string {
	s.rootMu.RLock()
	defer s.rootMu.RUnlock()
	roots := make([]string, 0, len(s.readRoots)+1)
	roots = append(roots, s.rootDir)
	roots = append(roots, s.readRoots...)
	return roots
}

// OpenReadable 在**读取边界**内安全地打开 path。
//
// 用 pathutil.SafeOpen（os.Root）而不是 os.Open：后者是"先校验再打开"两步走，
// 中间有 TOCTOU 窗口——能在共享目录里写文件的人可以把路径成分换成软链。
// SafeOpen 把解析交给内核，打开与解析是同一次系统调用序列。
func (s *FileIndexService) OpenReadable(path string) (*os.File, error) {
	return pathutil.SafeOpenAny(s.readRootsAll(), path)
}

// OpenAllowed 在**登记/写入边界**（只有 rootDir）内安全地打开 path。
// 与 IsPathAllowed 配套，语义一致：对端可写的范围不因可读根而扩大。
func (s *FileIndexService) OpenAllowed(path string) (*os.File, error) {
	return pathutil.SafeOpen(s.rootDir, path)
}

// reapUploads 过期会话清理：10 分钟无活动删除（含目标文件，防磁盘耗尽）。
func (s *FileIndexService) reapUploads() {
	for range time.Tick(5 * time.Minute) {
		s.upMu.Lock()
		for name, sess := range s.uploads {
			sess.mu.Lock()
			// 已完成的会话：句柄在 Complete 里就关掉了，文件必须留着（它已经
			// 登记进索引，是对外提供的副本）。只摘除表项，绝不删文件。
			if sess.done {
				delete(s.uploads, name)
				sess.mu.Unlock()
				continue
			}
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

// Close 关掉全部进行中的上传会话（句柄 + 临时文件）。
//
// 未完成的分片上传按作废处理（续传语义只在进程存活期间有意义）。
// 测试里**必须**调：Windows 上没关掉的句柄会让 t.TempDir() 清不掉——
// "The process cannot access the file because it is being used by another
// process"，Linux 上同样泄漏但完全看不见（2026-09-20 真机 Windows 才发现）。
func (s *FileIndexService) Close() {
	s.upMu.Lock()
	sessions := make([]*UploadSession, 0, len(s.uploads))
	for _, sess := range s.uploads {
		sessions = append(sessions, sess)
	}
	s.uploads = make(map[string]*UploadSession)
	s.upMu.Unlock()
	for _, sess := range sessions {
		sess.Abort() // 已完成的会话 Abort 直接返回（句柄在 Complete 里就关了）
	}
}

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
	// 顺序要紧：**先打开**，再对已打开的 fd 取属性。
	// 旧的写法是 os.Stat(path) 然后 hashFile(path) 再开一次——两次解析路径，
	// 中间 anybody 能把成分换成软链（TOCTOU）。现在只解析一次（SafeOpen），
	// 后续全部基于这个 fd。
	f, err := s.OpenAllowed(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}
	// 硬链接判定放在 pathutil 里，HTTP 登记侧（service.RegisterLocal）共用同一份。
	// 传句柄不传 FileInfo：Windows 要对着句柄才拿得到 NumberOfLinks。
	if err := pathutil.RejectHardlink(path, f); err != nil {
		return nil, err
	}
	h, err := hashReader(f)
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

// WriteFile 直接写文件到索引导航目录，并登记为本地文件。
// 这是 Source 控制面“直接写文件”的底层实现：流式写入 uploadDir 下的目标
// 文件，写完后复用 Create 计算 sha256 并登记映射（安全边界与 Create 一致，
// 只允许 allowed root 内路径）。
func (s *FileIndexService) WriteFile(name string, r io.Reader) (*FileInfo, error) {
	if r == nil {
		return nil, fmt.Errorf("reader is nil")
	}
	if err := os.MkdirAll(s.uploadDir, 0o755); err != nil {
		return nil, fmt.Errorf("create upload dir: %w", err)
	}
	path := filepath.Join(s.uploadDir, sanitizeName(name))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open write target: %w", err)
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("close file: %w", err)
	}
	fi, err := s.Create(path)
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	log.LogInfo("file-index: write-file name=%s hash=%s size=%d path=%s", name, fi.Hash, fi.Size, fi.Path)
	return fi, nil
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
	mu        sync.Mutex
	name      string
	path      string
	file      *os.File
	size      int64    // 声明总大小
	roots     []string // 写边界（BeginUpload 时定下来，Abort/reap 删文件时共用）
	bitmap    []uint64
	fullWords int // 非末 word 中已满 64 chunk 的数量——Complete O(1) 判满
	seq       int64
	last      time.Time // 最后活动时间（过期清理用）
	aborted   bool      // reap 摘除句柄后置位：WriteAt/Complete 见之即错（M7 竞态修复）
	done      bool      // 已完成：句柄已关闭，重复 Complete 直接返回缓存结果
	doneInfo  *FileInfo // done=true 时缓存的文件信息（多 source 各自 Complete 都要拿到）
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
		roots:  s.writeRoots(),
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
//
// 完成后**立刻关掉文件句柄**（句柄由调用方在锁外关闭）：以前句柄要留到 10 分钟
// 后的 reap 才关。这在 Linux 上完全看不出来（打开的文件照样能删能移），
// 在 Windows 上却是硬伤——文件被本进程占着，删不掉、移不动、覆盖不了，
// 测试 TempDir 清理直接失败（2026-09-20 真机 Windows 跑出来的）。
// 已完成的表项由 reap 摘除（不删文件，见 reapUploads 的 done 分支）。
func (u *UploadSession) Complete() (bool, *FileInfo, error) {
	done, fi, err, toClose := u.completeLocked()
	if toClose != nil {
		_ = toClose.Close()
	}
	return done, fi, err
}

// completeLocked 在 u.mu 下完成判定与登记，返回需要在**锁外**关闭的句柄。
func (u *UploadSession) completeLocked() (bool, *FileInfo, error, *os.File) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.last = time.Now()
	if u.done {
		return true, u.doneInfo, nil, nil
	}
	if u.aborted || u.file == nil {
		return false, nil, fmt.Errorf("upload session aborted"), nil
	}
	// 位图全满判定（O(1)）：非末 word 由增量 fullWords 计数（setBit 维护），
	// 末 word 单独检查（其满阈值 = 尾部不足 64 的 chunk 数，<64）。曾按
	// chunk 数分配位图导致末 word 判满逻辑失效、全扫 O(words) 每分片一次
	// （8GB 上传 2.6 亿次比较），见 setBit 注释。
	totalChunks := (u.size + uploadChunkSize - 1) / uploadChunkSize
	if totalChunks > 0 {
		words := len(u.bitmap)
		if u.fullWords != words-1 {
			return false, nil, nil, nil
		}
		// 末 word：需要 bits 个低位 chunk（<64 时按位掩码；=64 时全满）
		bits := totalChunks - int64(words-1)*64
		want := uint64(^uint64(0))
		if bits < 64 {
			want = (1 << bits) - 1
		}
		if u.bitmap[words-1] != want {
			return false, nil, nil, nil
		}
	}
	if err := u.file.Sync(); err != nil {
		return false, nil, err, nil
	}
	h, err := hashFile(u.path)
	if err != nil {
		return false, nil, err, nil
	}
	seq, err := repository.UpsertFileIndex(h, u.path, u.name, u.size, false)
	if err != nil {
		return false, nil, err, nil
	}
	log.LogInfo("file-index: upload complete hash=%s size=%d path=%s", h, u.size, u.path)
	fi := &FileInfo{Hash: h, Path: u.path, Name: u.name, Size: u.size, Seq: seq}
	u.done, u.doneInfo = true, fi
	// 句柄交回调用方在锁外关闭；置 nil 让后续 WriteAt/Complete 走 done/aborted 分支
	f := u.file
	u.file = nil
	return true, fi, nil, f
}

// Abort 中止会话并删除目标文件（幂等；reap 摘除句柄后调用无副作用）。
func (u *UploadSession) Abort() {
	u.mu.Lock()
	// 已完成的文件**不能**删：它已经登记进索引、可能正在对外提供服务。
	// （句柄在 Complete 里关掉了，这里无事可做。）
	if u.aborted || u.done {
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
	w := i / 64
	mask := uint64(1) << (i % 64)
	if u.bitmap[w]&mask != 0 {
		return // 已置位（重复分片）：不重复计数
	}
	before := u.bitmap[w] == ^uint64(0)
	u.bitmap[w] |= mask
	// 增量维护 fullWords（Complete 判满从 O(bitmap) 降到 O(1)——大文件
	// 上传每分片一次 Complete，8GB = 13 万分片 × 2048 word 全扫 = 2.6 亿次
	// 比较，全耗在单 worker 上。只对满阈值=64 的非末 word 计数；末 word 的
	// 满阈值 <64（尾部不足一个 word 的 chunk），由 Complete 单独判定）
	if !before && u.bitmap[w] == ^uint64(0) {
		totalChunks := (u.size + uploadChunkSize - 1) / uploadChunkSize
		if w < (totalChunks-1)/64 {
			u.fullWords++
		}
	}
}

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

// hashFile 计算文件 sha256（按路径打开）。
//
// 只对**上传会话自己落盘的文件**用（路径是 uploadDir 下自己拼出来的，不是对端
// 给的）。对端可控路径一律走 Create → OpenAllowed，别用这个。
func hashFile(path string) (string, error) {
	f, err := pathutil.SafeOpen(filepath.Dir(path), path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return hashReader(f)
}

func hashReader(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// sanitizeName 文件名净化（防路径穿越）。
func sanitizeName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	// ".." 必须一并挡掉：Base("../../etc/passwd") 是 "passwd" 没问题，但
	// Base("..") 还是 ".."，Join 出来的路径会指到 uploadDir 的**父目录**。
	// 虽然 Create 的边界检查会兜住，但那层依赖顺序太脆——落盘前就该掐掉。
	// Windows 上 `filepath.Base("/")` 返回的是 **`\`**（分隔符），不是 "/" ——
	// 只写 `name == "/"` 在 Windows 上漏掉，Join 出来的路径就是 uploadDir 自己
	// （"is a directory"）。两种分隔符都算上（真机 Windows 跑出来的，2026-09-20）。
	if name == "" || name == "." || name == ".." || name == "/" || name == `\` {
		return "upload.bin"
	}
	return name
}

// UploadChunkSizeForTest 供集成测试引用分片粒度。
func UploadChunkSizeForTest() int { return uploadChunkSize }

// PendingFetchesForTest 返回某连接上残留的 fetch 状态 reqId（测试辅助：
// source 包 PeerSource 竞速测试验证输家流被收割后状态清理，需要跨包观察
// 内部 fetches map。生产路径不调用）。
func (s *PeerJSService) PendingFetchesForTest(sess Session) []string {
	st := s.stateFor(sess)
	if st == nil {
		return nil
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]string, 0, len(st.fetches))
	for id := range st.fetches {
		out = append(out, id)
	}
	return out
}
