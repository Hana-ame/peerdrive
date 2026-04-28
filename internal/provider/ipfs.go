// IPFS 网关提供者 — 通过公共 IPFS 网关按 CID 获取文件内容。
// 通过 Manager 注册为 provider_type "ipfsgw"。
// GetReader 依次尝试每个网关，返回第一个成功的响应体。
// FetchByCID 返回完整字节数据（供通用下载器使用）。
//
// 配置示例：
//
//	IPFSProvider{Gateways: []string{
//	    "https://ipfs.io",
//	    "https://cloudflare-ipfs.com",
//	    "https://dweb.link",
//	}}

package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// IPFSProvider 管理一组公共 IPFS 网关，按优先级依次尝试获取文件。
type IPFSProvider struct {
	Gateways []string
}

// GetReader 通过 IPFS 网关获取指定 CID 的文件读取流。
// 依次尝试每个网关，返回第一个 HTTP 200 的响应体。
func (p *IPFSProvider) GetReader(cid string) (io.ReadCloser, error) {
	if len(p.Gateways) == 0 {
		return nil, fmt.Errorf("ipfs: no gateways configured")
	}
	for _, gw := range p.Gateways {
		url := fmt.Sprintf("%s/ipfs/%s", strings.TrimRight(gw, "/"), cid)
		resp, err := http.Get(url)
		if err != nil {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}
		return resp.Body, nil
	}
	return nil, fmt.Errorf("ipfs: all %d gateways failed for CID %s", len(p.Gateways), cid)
}

// GetFilenameHint 返回 CID 作为文件名提示。
func (p *IPFSProvider) GetFilenameHint(cid, originalFilename string) string {
	if originalFilename != "" {
		return originalFilename
	}
	return cid
}

// FetchByCID 通过 IPFS 网关获取指定 CID 的完整字节数据。
// 支持 context 取消/超时控制。
func (p *IPFSProvider) FetchByCID(ctx context.Context, cid string) ([]byte, error) {
	if len(p.Gateways) == 0 {
		return nil, fmt.Errorf("ipfs: no gateways configured")
	}
	for _, gw := range p.Gateways {
		url := fmt.Sprintf("%s/ipfs/%s", strings.TrimRight(gw, "/"), cid)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			continue
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}
		return data, nil
	}
	return nil, fmt.Errorf("ipfs: all %d gateways failed for CID %s", len(p.Gateways), cid)
}
