// Package service 提供业务逻辑层，封装下载和 P2P 操作。
// Downloader 使用 provider.Manager 进行内容检索，流程：
//   repository.GetFileByHash(hash) 查询元数据 →
//   manager.GetReader(providerType, path) 获取流 →
//   返回 io.ReadCloser + 文件名给控制器层流式响应。

package service

import (
	"fmt"
	"io"
	"peerdrive/internal/provider"
	"peerdrive/internal/repository"
)

type Downloader struct {
	providerManager *provider.Manager
}

func NewDownloader(manager *provider.Manager) *Downloader {
	return &Downloader{providerManager: manager}
}

func (d *Downloader) GetFileStream(hash string) (io.ReadCloser, string, error) {
	meta, err := repository.GetFileByHash(hash)
	if err != nil {
		return nil, "", err
	}
	if meta == nil {
		return nil, "", fmt.Errorf("file not found")
	}
	reader, filenameHint, err := d.providerManager.GetReader(meta.ProviderType, meta.Path)
	if err != nil {
		return nil, "", err
	}
	finalFilename := meta.Filename
	if finalFilename == "" {
		finalFilename = filenameHint
	}
	return reader, finalFilename, nil
}
