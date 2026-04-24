// Provider manager — routes GetReader calls to the appropriate ContentProvider based on provider_type.
// Usage: NewManager(baseDir) creates a Manager with "local" and "http" providers.
//   manager.GetReader("local", "path/to/file")  → LocalProvider
//   manager.GetReader("http", "https://...")    → HTTPProvider

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
