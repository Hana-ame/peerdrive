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

	"peerdrive/internal/model"
	"peerdrive/internal/provider"
	"peerdrive/internal/repository"
)

type Downloader struct {
	providerManager *provider.Manager
	p2pSvc          *P2PService
	storageDir      string
}

func NewDownloader(manager *provider.Manager, p2pSvc *P2PService, storageDir string) *Downloader {
	return &Downloader{
		providerManager: manager,
		p2pSvc:          p2pSvc,
		storageDir:      storageDir,
	}
}

func (d *Downloader) GetFileStream(hash string) (io.ReadCloser, string, bool, error) {
	meta, err := repository.GetFileMeta(hash)
	if err != nil {
		return nil, "", false, err
	}
	if meta == nil {
		return d.p2pFallback(hash)
	}

	providers, err := repository.GetFileProviders(hash)
	if err != nil {
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
			return reader, fn, meta.Gziped, nil
		}
		repository.MarkProviderUnavailable(p.ID)
	}

	return d.p2pFallback(hash)
}

func (d *Downloader) p2pFallback(hash string) (io.ReadCloser, string, bool, error) {
	if d.p2pSvc == nil {
		return nil, "", false, fmt.Errorf("file not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	data, err := d.p2pSvc.FetchFile(ctx, hash)
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

	return io.NopCloser(bytes.NewReader(data)), hash, false, nil
}
