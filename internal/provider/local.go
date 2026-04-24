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
