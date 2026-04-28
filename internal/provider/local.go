package provider

import (
	"io"
	"os"
)

type LocalProvider struct {
	BaseDir string
}

func NewLocalProvider(baseDir string) *LocalProvider {
	return &LocalProvider{BaseDir: baseDir}
}

func (p *LocalProvider) Name() string { return "local" }

func (p *LocalProvider) GetReader(path string) (io.ReadCloser, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	info, _ := f.Stat()
	size := int64(0)
	if info != nil {
		size = info.Size()
	}
	return f, size, nil
}
