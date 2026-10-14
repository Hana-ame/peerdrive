package update

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Manifest 是 release.yml 生成的机器可读发布清单（latest.json）：
// 版本号 + 各平台资产的下载地址与 sha256。程序用它做「版本比对 + 完整性校验」，
// 不依赖 GitHub API（PEERDRIVE_UPDATE_URL 可指向任意镜像源）。
type Manifest struct {
	Version string  `json:"version"`
	Assets  []Asset `json:"assets"`
}

// Asset 是清单里单个平台资产的元数据。
type Asset struct {
	Name   string `json:"name"`   // 资产文件名，如 peerdrive-linux-amd64
	OS     string `json:"os"`     // linux / windows / darwin
	Arch   string `json:"arch"`   // amd64 / arm64
	URL    string `json:"url"`    // 下载地址（release 资产直链）
	SHA256 string `json:"sha256"` // 资产 sha256（hex），与 checksums.txt 同源
}

// ParseManifest 解析 latest.json。空版本 / 空资产列表视为无效清单。
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("invalid manifest JSON: %w", err)
	}
	if strings.TrimSpace(m.Version) == "" {
		return nil, fmt.Errorf("invalid manifest: empty version")
	}
	if len(m.Assets) == 0 {
		return nil, fmt.Errorf("invalid manifest: no assets")
	}
	for _, a := range m.Assets {
		if strings.TrimSpace(a.URL) == "" || strings.TrimSpace(a.SHA256) == "" {
			return nil, fmt.Errorf("invalid manifest: asset %q missing url or sha256", a.Name)
		}
	}
	return &m, nil
}

// AssetName 返回当前平台对应的 release 资产文件名，与 release.yml 的命名一致：
//
//	peerdrive-<goos>-<goarch>[.exe]
func AssetName(goos, goarch string) string {
	name := "peerdrive-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// FindAsset 在清单里找指定平台的资产，按文件名精确匹配。
// 找不到时报错并列出清单里实际有哪些平台，便于排查清单与发布矩阵不一致。
func (m *Manifest) FindAsset(goos, goarch string) (*Asset, error) {
	want := AssetName(goos, goarch)
	for i := range m.Assets {
		if m.Assets[i].Name == want {
			return &m.Assets[i], nil
		}
	}
	var avail []string
	for _, a := range m.Assets {
		avail = append(avail, a.Name)
	}
	return nil, fmt.Errorf("manifest has no asset for %s (%s); available: %s",
		goos, goarch, strings.Join(avail, ", "))
}
