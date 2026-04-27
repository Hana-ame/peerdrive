// Package service 提供业务逻辑层，封装下载和 P2P 操作。
// Downloader 流程：
//   GetFileMeta(hash) → 存在则继续
//   GetFileProviders(hash) → 循环尝试每份副本
//     GetReader(provider_type, path) → 成功返回流 / 失败 MarkUnavailable 继续
//   全部失败 → P2P 回退（占位）

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/provider"
	"peerdrive/internal/repository"
)

type Downloader struct {
	providerManager *provider.Manager
	p2pSvc          *P2PService
	storageDir      string
}

// NewDownloader 创建一个下载器实例，通过 provider manager 和 P2P 回退获取文件流。
func NewDownloader(manager *provider.Manager, p2pSvc *P2PService, storageDir string) *Downloader {
	return &Downloader{
		providerManager: manager,
		p2pSvc:          p2pSvc,
		storageDir:      storageDir,
	}
}

// GetFileStream 获取指定 hash 的文件读取流，依次尝试各 provider，失败时回退到 P2P 网络。
func (d *Downloader) GetFileStream(hash string) (io.ReadCloser, string, bool, error) {
	defer log.LogDuration("Downloader.GetFileStream")()
	log.LogDebug("downloader: GetFileStream hash=%s", hash)

	meta, err := repository.GetFileMeta(hash)
	if err != nil {
		log.LogError("downloader: GetFileStream meta lookup failed: %v", err)
		return nil, "", false, err
	}
	if meta == nil {
		log.LogWarn("downloader: GetFileStream %s not found locally, trying P2P fallback", hash)
		return d.p2pFallback(hash)
	}

	providers, err := repository.GetFileProviders(hash)
	if err != nil {
		log.LogError("downloader: GetFileStream providers lookup failed: %v", err)
		return nil, "", false, err
	}

	for _, p := range providers {
		if !p.Available {
			continue
		}
		reader, hint, err := d.providerManager.GetReader(p.ProviderType, p.Path)
		if err == nil {
			fn := meta.Filename
			if fn == "" {
				fn = hint
			}
			log.LogInfo("downloader: GetFileStream %s found via provider %s", hash, p.ProviderType)
			return reader, fn, meta.Gziped, nil
		}
		repository.MarkProviderUnavailable(p.ID)
	}

	log.LogWarn("downloader: GetFileStream %s no available provider, trying P2P fallback", hash)
	return d.p2pFallback(hash)
}

func (d *Downloader) p2pFallback(hash string) (io.ReadCloser, string, bool, error) {
	if d.p2pSvc == nil || !d.p2pSvc.IsEnabled() {
		return nil, "", false, fmt.Errorf("file not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	data, err := d.p2pSvc.FetchFile(ctx, hash, nil)
	if err != nil {
		return nil, "", false, fmt.Errorf("file not found")
	}
	h := sha256.Sum256(data)
	if hex.EncodeToString(h[:]) != hash {
		return nil, "", false, fmt.Errorf("p2p data hash mismatch")
	}
	relPath := filepath.Join("p2p", hash[:2], hash)
	fullPath := filepath.Join(d.storageDir, relPath)
	os.MkdirAll(filepath.Dir(fullPath), 0755)
	os.WriteFile(fullPath, data, 0644)

	repository.InsertFileMeta(&model.FileMeta{
		Hash:     hash,
		Gziped:   false,
		Filename: hash,
		Type:     repository.FileTypeBlob,
	})
	repository.InsertFileProvider(hash, "local", relPath)

	log.LogInfo("downloader: p2pFallback fetched %s (%d bytes)", hash, len(data))
	return io.NopCloser(bytes.NewReader(data)), hash, false, nil
}
