// HTTP 远程提供者 — 通过 HTTP GET 从 URL 获取文件内容。
// 通过 Manager 注册为 provider_type "http"。
// GetReader 发送 net/http GET 请求，检查 200 状态码后返回 Body。
// GetFilenameHint 使用 path.Base 从 URL 提取文件名。

package provider

import (
	"io"
	"net/http"
	"path"
)

type HTTPProvider struct{}

func (p *HTTPProvider) GetReader(url string) (io.ReadCloser, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, err
	}
	return resp.Body, nil
}

func (p *HTTPProvider) GetFilenameHint(url, originalFilename string) string {
	if originalFilename != "" {
		return originalFilename
	}
	return path.Base(url)
}
