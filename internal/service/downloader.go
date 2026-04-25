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

func (d *Downloader) GetFileStream(hash string) (io.ReadCloser, string, string, error) {
	meta, err := repository.GetFileByHash(hash)
	if err != nil {
		return nil, "", "", err
	}
	if meta == nil {
		return nil, "", "", fmt.Errorf("file not found")
	}
	reader, filenameHint, err := d.providerManager.GetReader(meta.ProviderType, meta.Path)
	if err != nil {
		return nil, "", "", err
	}
	finalFilename := meta.Filename
	if finalFilename == "" {
		finalFilename = filenameHint
	}
	return reader, finalFilename, meta.Metadata, nil
}
