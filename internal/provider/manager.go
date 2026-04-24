// 提供者管理器 — 根据 provider_type 字符串将 GetReader 调用路由到
// 对应的 ContentProvider 实现。
// NewManager(baseDir) 创建 Manager 并注册 "local" 和 "http" 两个提供者。
// 路由示例：
//   manager.GetReader("local", "path/to/file")  → LocalProvider
//   manager.GetReader("http", "https://...")    → HTTPProvider
// 未知 provider_type 返回 "unknown provider" 错误。

package provider

import (
	"fmt"
	"io"
)

type Manager struct {
	providers map[string]ContentProvider
}

func NewManager(localBaseDir string) *Manager {
	m := &Manager{
		providers: make(map[string]ContentProvider),
	}
	m.providers["local"] = &LocalProvider{BaseDir: localBaseDir}
	m.providers["http"] = &HTTPProvider{}
	return m
}

func (m *Manager) GetReader(providerType, path string) (io.ReadCloser, string, error) {
	p, ok := m.providers[providerType]
	if !ok {
		return nil, "", fmt.Errorf("unknown provider: %s", providerType)
	}
	reader, err := p.GetReader(path)
	return reader, p.GetFilenameHint(path, ""), err
}
