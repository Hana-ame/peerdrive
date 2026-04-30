// 本地文件提供者 — 从 BaseDir 下的路径读取文件。
// 通过 Manager 注册为 provider_type "local"。
// GetReader 使用 os.Open 打开 BaseDir + path 的完整路径。
// GetFilenameHint 优先返回原始文件名，否则返回 path 的基本名。

package provider

import (
	"io"
	"os"
	"path/filepath"
)

type LocalProvider struct {
	BaseDir string
}

// GetReader 从本地文件系统读取指定路径的文件。
func (p *LocalProvider) GetReader(path string) (io.ReadCloser, error) {
	fullPath := path
	if !filepath.IsAbs(path) {
		fullPath = filepath.Join(p.BaseDir, path)
	}
	return os.Open(fullPath)
}

// GetFilenameHint 从文件路径中提取文件名提示。
func (p *LocalProvider) GetFilenameHint(path, originalFilename string) string {
	if originalFilename != "" {
		return originalFilename
	}
	return filepath.Base(path)
}
