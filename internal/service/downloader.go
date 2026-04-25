// Package service 提供业务逻辑层，封装下载和 P2P 操作。
// Downloader 使用 provider.Manager 进行内容检索，流程：
//   repository.GetFileByHash(hash) 查询元数据 →
//   manager.GetReader(providerType, path) 获取流 →
//   返回 io.ReadCloser + 文件名 + metadata JSON 给控制器层。
// metadata 是 files 表的 TEXT 列，存储 JSON 扩展属性如：
//   {"is_gzip": true, "mime_type": "application/gzip"}
// 控制器读取 metadata 后自行解析，根据 is_gzip 设置 Content-Encoding: gzip 等。
//
// P2P 回退（新增）：
//   当 repository.GetFileByHash 返回 nil（本地无此文件）且 p2pSvc 不为 nil 时，
//   调用 p2pSvc.FetchFile(hash) 从 P2P Bitswap 获取文件内容。
//   获取后缓存到 storage/p2p/{hash[:2]}/{hash} 并注册到 files 表。
//   注册成功后再重新读取本地文件返回流。
//
// 结构体新增字段：
//   p2pSvc     *service.P2PService  — P2P 服务实例，用于 Bitswap 回退下载
//   storageDir string               — 存储根目录，缓存 P2P 拉取的文件
// NewDownloader 新增参数：p2pSvc, storageDir

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

func (d *Downloader) GetFileStream(hash string) (io.ReadCloser, string, string, error) {
	// 遍历所有可用位置
	for {
		meta, err := repository.GetFileByHash(hash)
		if err != nil {
			return nil, "", "", err
		}
		if meta == nil {
			break // 无可用位置，尝试 P2P
		}
		reader, filenameHint, err := d.providerManager.GetReader(meta.ProviderType, meta.Path)
		if err == nil {
			finalFilename := meta.Filename
			if finalFilename == "" {
				finalFilename = filenameHint
			}
			return reader, finalFilename, meta.Metadata, nil
		}
		// 读取失败 → 标记不可用 → 尝试下一位置
		repository.MarkFileUnavailable(meta.ID)
	}

	// P2P 回退
	if d.p2pSvc != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		data, err := d.p2pSvc.FetchFile(ctx, hash)
		if err == nil {
			h := sha256.Sum256(data)
			if hex.EncodeToString(h[:]) != hash {
				return nil, "", "", fmt.Errorf("p2p data hash mismatch")
			}
			relPath := filepath.Join("p2p", hash[:2], hash)
			fullPath := filepath.Join(d.storageDir, relPath)
			os.MkdirAll(filepath.Dir(fullPath), 0755)
			os.WriteFile(fullPath, data, 0644)

			repository.InsertFile(&model.FileMetadata{
				Hash:         hash,
				ProviderType: "local",
				Path:         relPath,
				Filename:     hash,
				Metadata:     `{}`,
			})

			return io.NopCloser(bytes.NewReader(data)), hash, `{}`, nil
		}
	}
	return nil, "", "", fmt.Errorf("file not found")
}
