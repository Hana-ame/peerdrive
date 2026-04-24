// Package service provides business logic layer for download and P2P operations.
// Uses provider.Manager for content retrieval and libp2p for networking.

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
