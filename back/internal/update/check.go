// Package update 实现 peerdrive 的**可选**自动更新能力。
//
// 设计（与 release.yml 配套）：
//
//   - 发布侧：release.yml 在发版时额外生成 checksums.txt（人可 sha256sum -c 校验）
//     与 latest.json（机器可读清单：版本 + 各平台资产 URL + sha256，本包直接消费）。
//   - 检查侧：Check 拉取 latest.json → 版本比对 → 给出「当前 / 最新 / 是否有更新 /
//     本平台资产」。默认源是 GitHub latest release 的 latest.json，
//     PEERDRIVE_UPDATE_URL 可覆盖为任意镜像。
//   - 应用侧：Apply 下载资产到当前二进制同目录的临时文件 → 校验 sha256 →
//     两步原子替换（旧版留 .bak）→ 任何一步失败回滚，原二进制不动。
//
// 默认关（PEERDRIVE_AUTO_UPDATE 未设置 = false）：StartupCheck 直接返回，
// 整个流程零网络请求。打开后也只是**启动/周期提示**（log + 提示），
// 不自动替换运行中的服务器二进制——替换由显式的 `peerdrive update` 完成，
// 重启时机由操作者决定（服务器常被 systemd/docker 托管，进程内自替换
// 会与监督者冲突，且 Windows 上运行中替换有文件锁）。
package update

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"time"
)

// DefaultUpdateURL 是默认的更新检查源：GitHub latest release 的 latest.json。
// `releases/latest/download/<asset>` 是 GitHub 对「最新版 release 的某个资产」
// 的固定直链，latest.json 是 release.yml 上传的资产之一。
const DefaultUpdateURL = "https://github.com/Hana-ame/peerdrive/releases/latest/download/latest.json"

// MaxManifestBytes 是清单读取上限：latest.json 只是几 KB 的元数据，
// 4MiB 足够容纳未来资产变多的情况，同时防止被拖一个无限流。
const MaxManifestBytes = 4 << 20

// Result 是一次版本检查的结果。
type Result struct {
	// Current 是当前二进制版本（version.Version 或调用方传入值）。
	Current string
	// Latest 是清单里的最新版本（原样，如 "v0.3.1"）。
	Latest string
	// HasUpdate 为 true 表示 Latest 高于 Current。
	HasUpdate bool
	// Asset 是当前平台对应的下载资产（HasUpdate 为 true 时总有值）。
	Asset *Asset
}

// Check 拉取 updateURL 的 latest.json，比对 currentVersion 与最新版本，
// 并定位当前平台的资产。updateURL 为空时用 DefaultUpdateURL。
//
// 失败语义：网络 / 清单解析 / 缺本平台资产都返回 error——检查失败时调用方
// 走「提示失败但不影响运行」，绝不因清单不可读而误报「已是最新」。
func Check(ctx context.Context, currentVersion, updateURL string) (*Result, error) {
	if updateURL == "" {
		updateURL = DefaultUpdateURL
	}
	body, err := fetchBody(ctx, updateURL, MaxManifestBytes)
	if err != nil {
		return nil, fmt.Errorf("update: fetch manifest from %s: %w", updateURL, err)
	}
	m, err := ParseManifest(body)
	if err != nil {
		return nil, fmt.Errorf("update: parse manifest from %s: %w", updateURL, err)
	}
	asset, err := m.FindAsset(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}
	return &Result{
		Current:   currentVersion,
		Latest:    m.Version,
		HasUpdate: CompareVersions(currentVersion, m.Version) < 0,
		Asset:     asset,
	}, nil
}

// fetchBody GET 一个 URL 并返回响应体，限长 limit 字节，非 2xx 报错。
func fetchBody(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "peerdrive/self-update")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, err
	}
	return body, nil
}

// downloadAsset 把 asset.URL 下载到 dst（限长由调用方场景决定；这里不设上限，
// 二进制本身几十 MB，由外层 ctx 超时兜底）。
func downloadAsset(ctx context.Context, url string, dst io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "peerdrive/self-update")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	_, err = io.Copy(dst, resp.Body)
	return err
}

// sleep 是测试注入点（startup.go 的检查循环用它做可测的等待）。
var sleep = time.Sleep
