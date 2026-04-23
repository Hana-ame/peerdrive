package provider

import (
	"io"
	"os"
	"path/filepath"
)

type LocalProvider struct {
	BaseDir string
}

func (p *LocalProvider) GetReader(path string) (io.ReadCloser, error) {
	fullPath := filepath.Join(p.BaseDir, path)
	return os.Open(fullPath)
}

func (p *LocalProvider) GetFilenameHint(path, originalFilename string) string {
	if originalFilename != "" {
		return originalFilename
	}
	return filepath.Base(path)
}