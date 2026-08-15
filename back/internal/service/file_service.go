// FileService 处理文件上传、URL 注册、本地文件注册/批量注册、文件验证/删除、目录遍历。
package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"peerdrive/internal/config"
	"peerdrive/internal/legacy"
	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"

	"github.com/gin-gonic/gin"
)

var (
	ErrStorageDisabled   = errors.New("storage is disabled")
	ErrFileAlreadyExists = errors.New("file already exists")
)

type FileService struct {
	storageDir    string
	storageEnable bool
	cfg           *config.Config
	ipfsCompat    *legacy.IPFSCompatLayer
}

// SetIPFSCompat 注入 IPFS 兼容层实例，用于在文件注册/上传时自动创建 IPFS 块副本。
func (s *FileService) SetIPFSCompat(layer *legacy.IPFSCompatLayer) {
	s.ipfsCompat = layer
}

// NewFileService 创建一个新的文件服务实例。
func NewFileService(cfg *config.Config) *FileService {
	return &FileService{
		storageDir:    cfg.StorageDir,
		storageEnable: cfg.StorageEnable,
		cfg:           cfg,
	}
}

// GetMeta 按 hash 查文件元数据（M2 收层：download 控制器此前直调 repository.GetFileMeta）。
func (s *FileService) GetMeta(hash string) (*model.FileMeta, error) {
	return repository.GetFileMeta(hash)
}

// GetMetaByCID 按 IPFS CID 查文件元数据（M2 收层：download 控制器 DownloadByCID）。
func (s *FileService) GetMetaByCID(cid string) (*model.FileMeta, error) {
	return repository.GetFileMetaByCID(cid)
}

// ImportGatewayData 把从 IPFS 公共网关拉取的数据落盘 + 登记元数据/provider。
// M2 收层：原逻辑内联在 download 控制器 DownloadByCID 的网关 fallback 分支
// （写盘 + InsertFileMeta + InsertFileProvider 三连）。返回内容 hash。
func (s *FileService) ImportGatewayData(cid string, data []byte) (string, error) {
	h := sha256.Sum256(data)
	hashStr := hex.EncodeToString(h[:])
	relPath := filepath.Join(hashStr[:2], hashStr)
	fullPath := filepath.Join(s.storageDir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return "", err
	}
	_ = repository.InsertFileMeta(&model.FileMeta{
		Hash:     hashStr,
		Size:     int64(len(data)),
		Filename: cid,
		Type:     model.FileTypeBlob,
	})
	_ = repository.InsertFileProvider(hashStr, "local", relPath)
	return hashStr, nil
}

// RegisterBTFile 登记 BT 下载完成的文件：写入存储目录 + 元数据/provider 登记。
// M2 收层：原逻辑内联在 router.go BT onComplete 回调（InsertFileMeta +
// InsertFileProvider + 文件复制三连）。返回错误（原内联全忽略错误，这里
// 至少把存储失败暴露出来）。
func (s *FileService) RegisterBTFile(sha256hex string, size int64, srcPath string) error {
	if !isValidHash(sha256hex) {
		return fmt.Errorf("invalid sha256 %q", sha256hex)
	}
	relPath := filepath.Join(sha256hex[:2], sha256hex)
	destPath := filepath.Join(s.storageDir, relPath)
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}
	input, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer output.Close()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := repository.InsertFileMeta(&model.FileMeta{
		Hash:     sha256hex,
		Size:     size,
		Filename: filepath.Base(srcPath),
		Type:     model.FileTypeBlob,
	}); err != nil {
		log.LogWarn("file-svc: RegisterBTFile InsertFileMeta: %v", err)
	}
	if err := repository.InsertFileProvider(sha256hex, "local", relPath); err != nil {
		log.LogWarn("file-svc: RegisterBTFile InsertFileProvider: %v", err)
	}
	return nil
}

// ListAll 列出全部 blob 文件（M2 收层：file 控制器 ListFiles 此前直调 repository.ListAllFiles）。
func (s *FileService) ListAll(sortBy string) ([]model.FileListItem, error) {
	return repository.ListAllFiles(sortBy)
}

// isPathInStorage 校验 absPath 是否落在 storageDir 内（绝对路径 + 符号链接解析后）。
// 防御：register_local/register_folder/browse/copy 都接受调用方路径，若不锚定根目录，
// 任意绝对路径（如 /etc/shadow）会经 LocalFetcher 回读 / os.Remove 构成任意文件读写。
// 与 FileIndexService.IsPathAllowed 同一模式（file_index.go:53）。
func (s *FileService) isPathInStorage(absPath string) bool {
	if s.storageDir == "" {
		return false
	}
	root, err := filepath.Abs(s.storageDir)
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	abs, err := filepath.Abs(absPath)
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}

// RegisterLocal 计算本地文件的 SHA256 哈希，注册到 file_meta 和 file_providers。
func (s *FileService) RegisterLocal(path, filename string) (string, error) {
	defer log.LogDuration("FileService.RegisterLocal")()
	log.LogDebug("file-svc: RegisterLocal path=%s filename=%s", path, filename)

	if !s.storageEnable {
		err := ErrStorageDisabled
		log.LogError("file-svc: RegisterLocal storage disabled")
		return "", err
	}

	absPath := path
	if !filepath.IsAbs(path) {
		absPath = filepath.Join(s.storageDir, path)
	}

	// 安全边界：只允许注册 storage 根目录内的文件。
	// 坑：此前接受任意绝对路径，配合 LocalFetcher 的 provider 回读 = 匿名任意文件读取。
	if !s.isPathInStorage(absPath) {
		log.LogWarn("file-svc: RegisterLocal path outside storage root: %s", absPath)
		return "", fmt.Errorf("path outside storage root")
	}

	f, err := os.Open(absPath)
	if err != nil {
		log.LogError("file-svc: RegisterLocal open %s failed: %v", absPath, err)
		return "", err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		log.LogError("file-svc: RegisterLocal stat %s failed: %v", absPath, err)
		return "", err
	}
	size := info.Size()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		log.LogError("file-svc: RegisterLocal hash %s failed: %v", absPath, err)
		return "", err
	}
	hash := hex.EncodeToString(h.Sum(nil))

	f.Seek(0, io.SeekStart)
	buf := make([]byte, 512)
	n, _ := io.ReadFull(f, buf)
	mimeType := http.DetectContentType(buf[:n])
	if mimeType == "application/octet-stream" {
		if t := mime.TypeByExtension(filepath.Ext(absPath)); t != "" {
			mimeType = t
		}
	}

	// Derive filename from path if not provided
	if filename == "" {
		filename = filepath.Base(absPath)
	}

	existing, _ := repository.GetFileMeta(hash)
	if existing == nil {
		_ = repository.InsertFileMeta(&model.FileMeta{
			Hash:     hash,
			Size:     size,
			MimeType: mimeType,
			Gziped:   false,
			Filename: filename,
			Type:     repository.FileTypeBlob,
		})
	}

	_ = repository.InsertFileProvider(hash, "local", absPath)
	// IPFS 兼容：如果启用，将文件拷贝到 IPFS 块存储
	if s.ipfsCompat != nil && s.ipfsCompat.Enabled() {
		if err := s.ipfsCompat.AddFile(hash); err != nil {
			log.LogWarn("file-svc: RegisterLocal ipfs compat AddFile failed (non-fatal): %v", err)
		}
	}

	log.LogInfo("file-svc: RegisterLocal %s -> hash=%s size=%d", absPath, hash, size)
	return hash, nil
}

// RegisterFolder 递归注册文件夹内的所有文件，返回每个文件的 filename 和 hash。
func (s *FileService) RegisterFolder(folderPath string) ([]map[string]string, error) {
	defer log.LogDuration("FileService.RegisterFolder")()
	log.LogDebug("file-svc: RegisterFolder folderPath=%s", folderPath)

	if !s.storageEnable {
		err := ErrStorageDisabled
		log.LogError("file-svc: RegisterFolder storage disabled")
		return nil, err
	}

	absDir := folderPath
	if !filepath.IsAbs(folderPath) {
		absDir = filepath.Join(s.storageDir, folderPath)
	}

	// 安全边界：文件夹也必须锚定 storage 根目录内（否则批量读取任意目录）。
	if !s.isPathInStorage(absDir) {
		log.LogWarn("file-svc: RegisterFolder outside storage root: %s", absDir)
		return nil, fmt.Errorf("path outside storage root")
	}

	var results []map[string]string
	err := filepath.Walk(absDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		hash, err := s.RegisterLocal(path, info.Name())
		if err != nil {
			return err
		}
		results = append(results, map[string]string{
			"filename": info.Name(),
			"hash":     hash,
		})
		return nil
	})

	if err != nil {
		log.LogError("file-svc: RegisterFolder walk %s failed: %v", absDir, err)
		return results, err
	}

	log.LogInfo("file-svc: RegisterFolder %s registered %d files", folderPath, len(results))
	return results, nil
}

// ResolveURL 从 URL 获取文件，计算 SHA256、检测 MIME 类型，不写入存储/DB。
// followRedirects=false 时拒绝 301/302 重定向。
func (s *FileService) ResolveURL(rawURL string, followRedirects bool) (hash string, mimeType string, size int64, body []byte, filename string, err error) {
	defer log.LogDuration("FileService.ResolveURL")()
	log.LogDebug("file-svc: ResolveURL url=%s followRedirects=%v", rawURL, followRedirects)

	client := http.DefaultClient
	if !followRedirects {
		client = &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}

	resp, err := client.Get(rawURL)
	if err != nil {
		log.LogError("file-svc: ResolveURL GET %s failed: %v", rawURL, err)
		return "", "", 0, nil, "", fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.LogWarn("file-svc: ResolveURL %s returned status %d", rawURL, resp.StatusCode)
		return "", "", 0, nil, "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		log.LogError("file-svc: ResolveURL read body failed: %v", err)
		return "", "", 0, nil, "", fmt.Errorf("read body: %w", err)
	}

	h := sha256.Sum256(body)
	hash = hex.EncodeToString(h[:])
	size = int64(len(body))

	// MIME 检测：魔数嗅探优先，Content-Type 回退
	mimeType = http.DetectContentType(body[:min(len(body), 512)])
	if ct := resp.Header.Get("Content-Type"); ct != "" && mimeType == "application/octet-stream" {
		mimeType = ct
	}

	// 文件名提取：Content-Disposition → URL basename
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, encoded, ok := strings.Cut(cd, "filename*="); ok {
			if idx := strings.Index(encoded, "''"); idx > 0 && idx+2 < len(encoded) {
				part := encoded[idx+2:]
				if end := strings.IndexByte(part, ';'); end > 0 {
					part = part[:end]
				}
				decoded, decErr := percentUnescape(strings.TrimSpace(part))
				if decErr == nil && decoded != "" {
					filename = decoded
				}
			}
		}
		if filename == "" {
			if _, f, ok := strings.Cut(cd, "filename="); ok {
				filename = strings.Trim(f, "\" ")
			}
		}
	}
	if filename == "" {
		filename = path.Base(rawURL)
	}

	log.LogInfo("file-svc: ResolveURL %s -> hash=%s mime=%s size=%d", rawURL, hash, mimeType, size)
	return hash, mimeType, size, body, filename, nil
}

// RegisterURL 从 URL 获取文件，计算 SHA256 并注册（provider_type="http"），自动跟随 301/302 重定向。
func (s *FileService) RegisterURL(rawURL string, filename string) (*model.FileMeta, error) {
	defer log.LogDuration("FileService.RegisterURL")()
	log.LogDebug("file-svc: RegisterURL url=%s filename=%s", rawURL, filename)

	hash, mimeType, size, body, autoFilename, err := s.ResolveURL(rawURL, true)
	if err != nil {
		return nil, err
	}
	if filename == "" {
		filename = autoFilename
	}

	// 3. Insert into file_meta (skip if already exists)
	existing, _ := repository.GetFileMeta(hash)
	if existing == nil {
		err = repository.InsertFileMeta(&model.FileMeta{
			Hash:     hash,
			Size:     size,
			MimeType: mimeType,
			Gziped:   false,
			Filename: filename,
			Type:     repository.FileTypeBlob,
		})
		if err != nil {
			log.LogError("file-svc: RegisterURL insert meta failed: %v", err)
			return nil, fmt.Errorf("insert meta: %w", err)
		}
	}

	// Insert file_provider (type "http", path = url)
	err = repository.InsertFileProvider(hash, "http", rawURL)
	if err != nil {
		log.LogError("file-svc: RegisterURL insert provider failed: %v", err)
		return nil, fmt.Errorf("insert provider: %w", err)
	}

	// 4. If storage enabled, save to content-addressed storage
	if s.storageEnable {
		relPath := hash[:2] + "/" + hash
		fullPath := filepath.Join(s.storageDir, relPath)
		os.MkdirAll(filepath.Dir(fullPath), 0755)
		if err := os.WriteFile(fullPath, body, 0644); err != nil {
			log.LogWarn("file-svc: RegisterURL save to storage failed (non-fatal): %v", err)
		}
	}
	// IPFS 兼容：如果启用，将文件拷贝到 IPFS 块存储
	if s.ipfsCompat != nil && s.ipfsCompat.Enabled() {
		if err := s.ipfsCompat.AddFile(hash); err != nil {
			log.LogWarn("file-svc: RegisterURL ipfs compat AddFile failed (non-fatal): %v", err)
		}
	}

	meta := &model.FileMeta{
		Hash:     hash,
		Size:     size,
		MimeType: mimeType,
		Gziped:   false,
		Filename: filename,
		Type:     repository.FileTypeBlob,
	}

	log.LogInfo("file-svc: RegisterURL %s -> hash=%s size=%d", rawURL, hash, size)
	return meta, nil
}

// percentUnescape decodes percent-encoded sequences (e.g. %20 -> space).
func percentUnescape(s string) (string, error) {
	var buf strings.Builder
	buf.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi, err1 := hexDecodeNibble(s[i+1])
			lo, err2 := hexDecodeNibble(s[i+2])
			if err1 == nil && err2 == nil {
				buf.WriteByte(hi<<4 | lo)
				i += 2
				continue
			}
		}
		buf.WriteByte(s[i])
	}
	return buf.String(), nil
}

func hexDecodeNibble(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	default:
		return 0, fmt.Errorf("invalid hex nibble: %c", c)
	}
}

// Upload 上传文件到 content-addressed 存储，计算 SHA256 并注册元数据和 provider。
func (s *FileService) Upload(reader io.Reader, filename string) (*model.FileMeta, error) {
	defer log.LogDuration("FileService.Upload")()
	log.LogDebug("file-svc: Upload filename=%s", filename)

	if !s.storageEnable {
		err := ErrStorageDisabled
		log.LogError("file-svc: Upload storage disabled")
		return nil, err
	}

	tmpFile, err := os.CreateTemp("", "peerdrive-upload-*")
	if err != nil {
		log.LogError("file-svc: Upload create temp file failed: %v", err)
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	hasher := sha256.New()
	tee := io.TeeReader(reader, hasher)
	size, err := io.Copy(tmpFile, tee)
	if err != nil {
		tmpFile.Close()
		log.LogError("file-svc: Upload write temp file failed: %v", err)
		return nil, fmt.Errorf("write temp file: %w", err)
	}
	hash := hex.EncodeToString(hasher.Sum(nil))

	tmpFile.Seek(0, io.SeekStart)
	buf := make([]byte, 512)
	n, _ := io.ReadFull(tmpFile, buf)
	mimeType := http.DetectContentType(buf[:n])
	if ext := filepath.Ext(filename); ext != "" && mimeType == "application/octet-stream" {
		if t := mime.TypeByExtension(ext); t != "" {
			mimeType = t
		}
	}
	tmpFile.Close()

	if existing, _ := repository.GetFileMeta(hash); existing != nil {
		log.LogInfo("file-svc: Upload %s already exists (hash=%s)", filename, hash)
		return existing, ErrFileAlreadyExists
	}

	relPath := hash[:2] + "/" + hash
	fullPath := filepath.Join(s.storageDir, relPath)
	os.MkdirAll(filepath.Dir(fullPath), 0755)

	if err := os.Rename(tmpName, fullPath); err != nil {
		// Fallback: cross-device link, use copy instead
		if err := copyFile(tmpName, fullPath); err != nil {
			log.LogError("file-svc: Upload move to storage failed: %v", err)
			return nil, fmt.Errorf("move to storage: %w", err)
		}
	}
	tmpName = ""

	meta := &model.FileMeta{
		Hash:     hash,
		Size:     size,
		MimeType: mimeType,
		Gziped:   false,
		Filename: filename,
		Type:     repository.FileTypeBlob,
	}
	if err := repository.InsertFileMeta(meta); err != nil {
		log.LogError("file-svc: Upload insert meta failed: %v", err)
		return nil, fmt.Errorf("insert meta: %w", err)
	}

	if err := repository.InsertFileProvider(hash, "local", relPath); err != nil {
		log.LogError("file-svc: Upload insert provider failed: %v", err)
		return nil, fmt.Errorf("insert provider: %w", err)
	}
	// IPFS 兼容：如果启用，将文件拷贝到 IPFS 块存储
	if s.ipfsCompat != nil && s.ipfsCompat.Enabled() {
		if err := s.ipfsCompat.AddFile(meta.Hash); err != nil {
			log.LogWarn("file-svc: Upload ipfs compat AddFile failed (non-fatal): %v", err)
		}
	}

	log.LogInfo("file-svc: Upload %s completed (hash=%s, size=%d)", filename, hash, size)
	return meta, nil
}

// Verify 通过 hash 查询文件元数据，用于验证文件是否存在。
func (s *FileService) Verify(hash string) (*model.FileMeta, error) {
	defer log.LogDuration("FileService.Verify")()
	log.LogDebug("file-svc: Verify hash=%s", hash)

	meta, err := repository.GetFileMeta(hash)
	if err != nil {
		log.LogError("file-svc: Verify %s failed: %v", hash, err)
		return nil, err
	}
	if meta != nil {
		log.LogInfo("file-svc: Verify %s found (size=%d)", hash, meta.Size)
	} else {
		log.LogInfo("file-svc: Verify %s not found", hash)
	}
	return meta, nil
}

// Delete 删除指定 hash 的本地文件及其元数据和 provider 记录。
func (s *FileService) Delete(hash string) error {
	defer log.LogDuration("FileService.Delete")()
	log.LogDebug("file-svc: Delete hash=%s", hash)

	if !s.storageEnable {
		err := ErrStorageDisabled
		log.LogError("file-svc: Delete storage disabled")
		return err
	}
	// 防御：hash 未校验就进 provider 路径，配合 LocalFetcher 回读 = 任意文件删。
	// 由于 RegisterLocal 现在已锚定 storage 根，这里再兜底防止历史数据里有根外 provider 路径。
	if !isValidHash(hash) {
		log.LogWarn("file-svc: Delete invalid hash %q", hash)
		return fmt.Errorf("invalid hash")
	}
	providers, _ := repository.GetFileProviders(hash)
	for _, p := range providers {
		if p.ProviderType == "local" {
			if s.isPathInStorage(p.Path) {
				os.Remove(p.Path)
			}
		}
	}
	repository.DB.Exec(`DELETE FROM file_providers WHERE hash = ?`, hash)
	repository.DB.Exec(`DELETE FROM file_meta WHERE hash = ?`, hash)
	log.LogInfo("file-svc: Delete %s completed", hash)
	return nil
}

// BrowseDir 浏览本地目录，返回文件和子目录列表（含大小和修改时间）。
func (s *FileService) BrowseDir(dirPath string) ([]model.DirEntry, error) {
	defer log.LogDuration("FileService.BrowseDir")()
	log.LogDebug("file-svc: BrowseDir dirPath=%s", dirPath)

	if !s.storageEnable {
		err := ErrStorageDisabled
		log.LogError("file-svc: BrowseDir storage disabled")
		return nil, err
	}

	absDir := dirPath
	if !filepath.IsAbs(dirPath) {
		absDir = filepath.Join(s.storageDir, dirPath)
	}

	// 安全边界：只允许浏览 storage 根目录内，拒绝任意目录列举（任意文件读取的前提）。
	if !s.isPathInStorage(absDir) {
		log.LogWarn("file-svc: BrowseDir outside storage root: %s", absDir)
		return nil, fmt.Errorf("path outside storage root")
	}

	entries, err := os.ReadDir(absDir)
	if err != nil {
		log.LogError("file-svc: BrowseDir read %s failed: %v", absDir, err)
		return nil, err
	}

	var result []model.DirEntry
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		fullPath := filepath.Join(absDir, e.Name())
		entry := model.DirEntry{
			Name:    e.Name(),
			Path:    fullPath,
			IsDir:   e.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
		}
		result = append(result, entry)
	}
	log.LogInfo("file-svc: BrowseDir %s found %d entries", absDir, len(result))
	return result, nil
}

func copyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()

	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer d.Close()

	if _, err := io.Copy(d, s); err != nil {
		return err
	}
	return d.Sync()
}

// CopyFile copies a file identified by hash to a destination path within storage
// and registers the copy in file_providers. Returns the destination path.
func (s *FileService) CopyFile(hash string, destPath string) (string, error) {
	defer log.LogDuration("FileService.CopyFile")()
	log.LogDebug("file-svc: CopyFile hash=%s dest=%s", hash, destPath)

	if !s.storageEnable {
		return "", ErrStorageDisabled
	}

	// Verify source exists and get its data
	meta, err := repository.GetFileMeta(hash)
	if err != nil {
		return "", fmt.Errorf("lookup source meta: %w", err)
	}
	if meta == nil {
		return "", fmt.Errorf("source hash %s not found", hash)
	}

	// Resolve full destination path
	absDest := destPath
	if !filepath.IsAbs(destPath) {
		absDest = filepath.Join(s.storageDir, destPath)
	}

	// 安全边界：目标必须在 storage 根目录内，且 hash 必须合法。
	// 坑：此前绝对路径原样采用 / 相对路径可 ../ 逃逸，配合公开 upload 可写任意文件（如 authorized_keys）。
	// 写盘前的检查而不是写盘后的 Rel 判定（旧代码 615 行只在写完后改 DB 记录）。
	if !isValidHash(hash) {
		return "", fmt.Errorf("invalid source hash")
	}
	if !s.isPathInStorage(absDest) {
		log.LogWarn("file-svc: CopyFile dest outside storage root: %s", absDest)
		return "", fmt.Errorf("destination path outside storage root")
	}

	// Get source data via the download pipeline
	body, err := s.ReadFile(hash)
	if err != nil {
		return "", fmt.Errorf("read source file: %w", err)
	}

	// Write to destination
	if err := os.MkdirAll(filepath.Dir(absDest), 0755); err != nil {
		return "", fmt.Errorf("create dest dir: %w", err)
	}
	if err := os.WriteFile(absDest, body, 0644); err != nil {
		return "", fmt.Errorf("write dest file: %w", err)
	}

	// Register the copy as a local provider
	relPath, _ := filepath.Rel(s.storageDir, absDest)
	if relPath == "" || strings.HasPrefix(relPath, "..") {
		relPath = absDest
	}
	_ = repository.InsertFileProvider(hash, "local", relPath)

	log.LogInfo("file-svc: CopyFile hash=%s -> %s (rel=%s)", hash, absDest, relPath)
	return absDest, nil
}

// ReadFile reads a file's bytes from content-addressed storage or via providers.
func (s *FileService) ReadFile(hash string) ([]byte, error) {
	// Try content-addressed paths first
	candidates := []string{
		filepath.Join(s.storageDir, hash[:2], hash),
		filepath.Join(s.storageDir, "p2p", hash[:2], hash),
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err == nil {
			return data, nil
		}
	}

	// Fallback to DB providers
	providers, err := repository.GetFileProviders(hash)
	if err != nil {
		return nil, fmt.Errorf("db lookup: %w", err)
	}
	for _, p := range providers {
		if p.ProviderType == "local" && p.Available {
			path := p.Path
			if !filepath.IsAbs(path) {
				path = filepath.Join(s.storageDir, path)
			}
			data, err := os.ReadFile(path)
			if err == nil {
				return data, nil
			}
		}
		if p.ProviderType == "http" && p.Available {
			resp, err := http.Get(p.Path)
			if err == nil {
				data, readErr := io.ReadAll(resp.Body)
				resp.Body.Close()
				if readErr == nil {
					return data, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("file %s not found", hash)
}

// MaxUploadBytes 根据认证状态返回最大上传字节数（认证用户使用 cfg.MaxUploadBytes，匿名用户使用 cfg.MaxUploadBytesAnon）。
func (s *FileService) MaxUploadBytes(c *gin.Context) int64 {
	if authed, exists := c.Get("authenticated"); exists && authed.(bool) {
		return s.cfg.MaxUploadBytes
	}
	return s.cfg.MaxUploadBytesAnon
}
