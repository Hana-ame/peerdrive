package source

// local.go：LocalSource——本地磁盘源（file_index 映射优先 + 内容寻址存储兜底）。
// 语义与 transport.serveFile 的路径决策完全一致（同一逻辑收敛到一处）：
//   - file_index 命中且路径在允许根目录内 → 读映射路径
//   - 否则 → 内容寻址存储 storageDir/<hash[:2]>/<hash>
// 本地文件写入时已完成 sha256 校验（upload Complete），Open 不再校验（与
// serveFile 行为一致）；Available = 存储目录可读。

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"

	"peerdrive/internal/log"
	"peerdrive/internal/pathutil"
	"peerdrive/internal/transport"
)

// LocalSource 本地磁盘文件源。
type LocalSource struct {
	name       string
	storageDir string
	fileIndex  *transport.FileIndexService

	mu       sync.RWMutex
	priority int
}

// NewLocalSource 创建本地源。name 默认 "local"；storageDir 为内容寻址存储根；
// fileIndex 可为 nil（仅内容寻址）。
func NewLocalSource(storageDir string, fileIndex *transport.FileIndexService) *LocalSource {
	return &LocalSource{
		name:       "local",
		storageDir: storageDir,
		fileIndex:  fileIndex,
	}
}

func (s *LocalSource) Name() string { return s.name }
func (s *LocalSource) Type() string { return "local" }

// Capabilities 本地磁盘天然支持流式分片（os.File Seek/ReadAt）。
func (s *LocalSource) Capabilities() Capability { return CapStream }

func (s *LocalSource) Priority() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.priority
}

func (s *LocalSource) SetPriority(p int) {
	s.mu.Lock()
	s.priority = p
	s.mu.Unlock()
}

// Available 存储目录存在且可读。
func (s *LocalSource) Available(ctx context.Context) bool {
	f, err := os.Open(s.storageDir)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// resolvePath 复刻 serveFile 的路径决策：file_index 优先（路径须**可读**，否则
// 回退 CAS——历史脏数据/恶意登记不回传根外文件，H2）。
//
// 注意用 IsPathReadable 而不是 IsPathAllowed：后者是登记/写入边界，只认下载目录；
// 运营者把共享目录设在下载目录之外时，用它会把一份**正当**的文件判成越权，
// 于是回退到并不存在的 CAS 副本 → 对端 "read failed"。
func (s *LocalSource) resolvePath(hash string) string {
	path := filepath.Join(s.storageDir, hash[:2], hash)
	if s.fileIndex != nil {
		if fi, err := s.fileIndex.Info(hash); err == nil && fi.Path != "" {
			if s.fileIndex.IsPathReadable(fi.Path) {
				return fi.Path
			}
			log.LogWarn("source/local: index path not readable, serving content-addressed: %s", fi.Path)
		}
	}
	return path
}

// open 安全地打开 resolvePath 的结果。
//
// 不走 os.Open：resolvePath 返回的索引路径是**登记时**校验过的，到此刻之间可能
// 已被换成软链。走 pathutil.SafeOpen（os.Root）让内核在打开那一刻重新判定；
// 共享根里的路径用 fileIndex.OpenReadable，CAS 副本锚定 storageDir。
func (s *LocalSource) open(hash string) (*os.File, error) {
	p := s.resolvePath(hash)
	if s.fileIndex != nil && s.fileIndex.IsPathReadable(p) {
		if f, err := s.fileIndex.OpenReadable(p); err == nil {
			return f, nil
		}
	}
	if s.storageDir == "" {
		return os.Open(p) // 未配置 storageDir（纯测试装配），保持旧行为
	}
	return pathutil.SafeOpen(s.storageDir, p)
}

// Open 流式打开：offset<0 → 0；size<0 → 到文件尾。分片用 os.File.Seek 定位
// （本地文件无网络成本，直接给原文件句柄）。
func (s *LocalSource) Open(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	f, err := s.open(hash)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	off := offset
	if off < 0 {
		off = 0
	}
	if off > st.Size() {
		off = st.Size()
	}
	length := size
	if length < 0 || off+length > st.Size() {
		length = st.Size() - off
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	// 限长读取：io.LimitReader 截断到 length（防越界读——offset/size 是
	// 调用方输入，防御性处理）
	return &limitedReadCloser{r: io.LimitReader(f, length), c: f}, nil
}

// Fetch 整体获取（CapStream 已覆盖，此处防御性实现，Manager 不会调用）。
func (s *LocalSource) Fetch(ctx context.Context, hash string) ([]byte, error) {
	r, err := s.Open(ctx, hash, 0, -1)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// Info 元数据：优先 file_index（有 name/path），否则 CAS 文件 stat。
func (s *LocalSource) Info(ctx context.Context, hash string) (*FileMeta, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	if s.fileIndex != nil {
		if fi, err := s.fileIndex.Info(hash); err == nil && fi.Path != "" {
			return &FileMeta{Hash: hash, Size: fi.Size, Name: fi.Name, Path: fi.Path}, nil
		}
	}
	f, err := s.open(hash)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return &FileMeta{Hash: hash, Size: st.Size()}, nil
}

// AddLocalFile 把本地已有文件加入 local source（Source 控制面）。
// 底层复用 FileIndexService.Create，安全边界、去重、索引语义与 create verb 一致。
func (s *LocalSource) AddLocalFile(path string) (*FileMeta, error) {
	if s.fileIndex == nil {
		return nil, ErrControlUnsupported
	}
	fi, err := s.fileIndex.Create(path)
	if err != nil {
		return nil, err
	}
	log.LogInfo("source/local: add local file path=%s hash=%s size=%d", path, fi.Hash, fi.Size)
	return &FileMeta{Hash: fi.Hash, Size: fi.Size, Name: fi.Name, Path: fi.Path}, nil
}

// WriteFile 直接写文件到 local source（Source 控制面）。
// 底层复用 FileIndexService.WriteFile：流式写入索引导航目录，完成后登记。
func (s *LocalSource) WriteFile(name string, r io.Reader) (*FileMeta, error) {
	if s.fileIndex == nil {
		return nil, ErrControlUnsupported
	}
	fi, err := s.fileIndex.WriteFile(name, r)
	if err != nil {
		return nil, err
	}
	log.LogInfo("source/local: write file name=%s hash=%s size=%d", name, fi.Hash, fi.Size)
	return &FileMeta{Hash: fi.Hash, Size: fi.Size, Name: fi.Name, Path: fi.Path}, nil
}

// limitedReadCloser 限长读取 + 关闭底层文件。
type limitedReadCloser struct {
	r io.Reader
	c io.Closer
}

func (l *limitedReadCloser) Read(p []byte) (int, error) { return l.r.Read(p) }
func (l *limitedReadCloser) Close() error               { return l.c.Close() }
