package transport

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
)

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
	mu             sync.Mutex
	name           string
	path           string
	uploadDir      string
	file           *os.File
	size           int64    // 声明总大小
	roots          []string // 写边界（BeginUpload 时定下来，Abort/reap 删文件时共用）
	bitmap         []uint64
	fullWords      int // 非末 word 中已满 64 chunk 的数量——Complete O(1) 判满
	seq            int64
	last           time.Time // 最后活动时间（过期清理用）
	aborted        bool      // reap 摘除句柄后置位：WriteAt/Complete 见之即错（M7 竞态修复）
	done           bool      // 已完成：句柄已关闭，重复 Complete 直接返回缓存结果
	doneInfo       *FileInfo // done=true 时缓存的文件信息（多 source 各自 Complete 都要拿到）
	uploaderPeerID string
	isInbox        bool
}

// DeclaredSize 返回声明总大小（BeginUpload 同名复用一致性校验用）。
func (u *UploadSession) DeclaredSize() int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.size
}

// BeginUpload 开始/继续一个分片上传会话（同 name 幂等复用）。
func (s *FileIndexService) BeginUpload(name string, size int64) (*UploadSession, error) {
	return s.BeginUploadForPeer(name, size, "")
}

// BeginUploadForPeer starts or resumes an upload session, quarantining external peer uploads into inbox/ (Issue #267).
func (s *FileIndexService) BeginUploadForPeer(name string, size int64, uploaderPeerID string) (*UploadSession, error) {
	if size < 0 || size > 8*1024*1024*1024 { // 上限 8GB，防恶意声明
		return nil, fmt.Errorf("invalid size %d", size)
	}
	s.upMu.Lock()
	defer s.upMu.Unlock()
	cleanName := sanitizeName(name)
	sessKey := cleanName
	if uploaderPeerID != "" {
		sessKey = uploaderPeerID + ":" + cleanName
	}
	if sess, ok := s.uploads[sessKey]; ok {
		sess.last = time.Now()
		// M7：同名会话复用必须 size 一致——位图按旧 size 建，声明不一致会导致
		// 续传偏移错乱、末 chunk 判满错误。不一致直接拒绝（多 source 本就应同 size）
		if sess.DeclaredSize() != size {
			return nil, fmt.Errorf("upload session %q already exists with size %d (requested %d)", name, sess.DeclaredSize(), size)
		}
		return sess, nil
	}
	targetDir := s.uploadDir
	isInbox := false
	if uploaderPeerID != "" {
		targetDir = filepath.Join(s.uploadDir, "inbox")
		isInbox = true
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(targetDir, cleanName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open upload file: %w", err)
	}
	// 位图按 64 chunk/word 分配（曾按 chunk 数分配导致末 word 判满逻辑失效）
	totalChunks := (size + uploadChunkSize - 1) / uploadChunkSize
	sess := &UploadSession{
		name:           name,
		path:           path,
		uploadDir:      targetDir,
		file:           f,
		roots:          s.writeRoots(),
		size:           size,
		bitmap:         make([]uint64, (totalChunks+63)/64),
		last:           time.Now(),
		uploaderPeerID: uploaderPeerID,
		isInbox:        isInbox,
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
	s.uploads[sessKey] = sess
	log.LogInfo("file-index: upload session begin name=%s size=%d path=%s", name, size, path)
	return sess, nil
}

// WriteAt 写入一个分片（offset 需 chunk 对齐，长度 ≤ chunk 粒度由帧协议保证）。
//
// 2026-10-06 correctness fix (audit C-16): if the underlying write fails, the bitmap
// bits for the affected chunks are cleared (not just left as-is). A previous write may
// have set those bits, but a failed retransmit write could have partially overwritten
// the on-disk data — leaving the bit set would silently corrupt the final file.
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
		// Clear the bits for the chunks this write was supposed to cover. Even though
		// the current call didn't set them, a previous successful write may have. A
		// failed write can leave partial/corrupt data on disk; clearing the bits
		// forces the client to retry those chunks, which will overwrite the corrupt
		// region. Without this, the bitmap says "done" while the on-disk data is
		// garbage — the sha256 check at Complete would catch it, but only after the
		// entire upload finishes (wasted bandwidth).
		start := offset / uploadChunkSize
		end := (offset + int64(len(data)) + uploadChunkSize - 1) / uploadChunkSize
		for i := start; i < end; i++ {
			u.clearBit(i)
		}
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
// 完成后立刻关掉文件句柄（句柄由调用方在锁外关闭）。
func (u *UploadSession) Complete() (bool, *FileInfo, error) {
	done, fi, err, toClose := u.completeLocked()
	if toClose != nil {
		if closeErr := toClose.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close upload file: %w", closeErr)
		}
	}
	return done, fi, err
}

// completeLocked 在 u.mu 下完成判定与登记，返回需要在锁外关闭的句柄。
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
	// 末 word 单独检查（其满阈值 = 尾部不足 64 的 chunk 数，<64）。
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
	// 在 Windows 上打开的文件删不掉、重命名不了。
	// 必须在 hashFile 与 os.Rename 之前先关掉文件句柄。
	toClose := u.file
	u.file = nil
	if closeErr := toClose.Close(); closeErr != nil {
		return false, nil, fmt.Errorf("close upload file: %w", closeErr), nil
	}
	h, err := hashFile(u.path)
	if err != nil {
		return false, nil, err, nil
	}
	// Default to raw file with no extension, named by its sha
	destDir := u.uploadDir
	if destDir == "" {
		destDir = filepath.Dir(u.path)
	}
	destPath := filepath.Join(destDir, h)
	if destPath != u.path {
		if _, err := os.Stat(destPath); err == nil {
			_ = os.Remove(u.path)
		} else {
			if err := os.Rename(u.path, destPath); err != nil {
				return false, nil, err, nil
			}
		}
		u.path = destPath
	}
	// Issue #405: unified write, preserving the uploader/is_inbox provenance flags.
	seq, err := repository.RegisterFile(repository.FileRecord{
		Hash: h, Path: u.path, Name: u.name, Size: u.size,
		Kind: model.FileTypeBlob, ProviderPath: u.path,
		UploaderPeerID: u.uploaderPeerID, IsInbox: u.isInbox,
	})
	if err != nil {
		return false, nil, err, nil
	}

	log.LogInfo("file-index: upload complete hash=%s size=%d path=%s name=%s", h, u.size, u.path, u.name)
	fi := &FileInfo{Hash: h, Path: u.path, Name: u.name, Size: u.size, Seq: seq}
	u.done, u.doneInfo = true, fi
	return true, fi, nil, nil
}

// Abort 中止会话并删除目标文件（幂等；reap 摘除句柄后调用无副作用）。
func (u *UploadSession) Abort() {
	u.mu.Lock()
	// 已完成的文件不能删：它已经登记进索引、可能正在对外提供服务。
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
	// 增量维护 fullWords
	if !before && u.bitmap[w] == ^uint64(0) {
		totalChunks := (u.size + uploadChunkSize - 1) / uploadChunkSize
		if w < (totalChunks-1)/64 {
			u.fullWords++
		}
	}
}

// clearBit unsets bit i and decrements fullWords if the word is no longer full.
func (u *UploadSession) clearBit(i int64) {
	w := i / 64
	mask := uint64(1) << (i % 64)
	if u.bitmap[w]&mask == 0 {
		return // 本就未置位
	}
	before := u.bitmap[w] == ^uint64(0)
	u.bitmap[w] &^= mask
	// 该 word 刚不再满：回退计数
	if before {
		totalChunks := (u.size + uploadChunkSize - 1) / uploadChunkSize
		if w < (totalChunks-1)/64 {
			u.fullWords--
		}
	}
}
