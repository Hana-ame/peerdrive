// IPFS 网关提供者 — 通过公共 IPFS 网关按 CID 获取文件内容。
// 实现 ContentProvider 接口，通过 Manager 注册为 provider_type "ipfsgw"。
// GetReader 并发尝试所有网关，返回第一个成功的响应体；
// 一旦某个网关成功，取消其余请求，避免 goroutine 泄漏。
//
// 配置示例：
//
//	provider.NewIPFSProvider([]string{
//	    "https://ipfs.io",
//	    "https://cloudflare-ipfs.com",
//	    "https://dweb.link",
//	})

package provider

import (
	"context"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

const (
	defaultMaxRetries   = 3
	defaultBaseInterval = 500 * time.Millisecond
	defaultMaxInterval  = 5 * time.Second
	defaultHTTPTimeout  = 30 * time.Second
)

// IPFSProvider 管理一组公共 IPFS 网关，按优先级依次尝试获取文件。
type IPFSProvider struct {
	Gateways []string
	client   *http.Client
}

// NewIPFSProvider 创建 IPFS 网关提供者。
func NewIPFSProvider(gateways []string) *IPFSProvider {
	return &IPFSProvider{
		Gateways: gateways,
		client: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}
}

// GetReader 实现 ContentProvider 接口。
// 并发尝试所有网关，返回第一个成功的响应体。其余请求会被取消并关闭。
func (p *IPFSProvider) GetReader(cid string) (io.ReadCloser, error) {
	if len(p.Gateways) == 0 {
		return nil, fmt.Errorf("ipfs: no gateways configured")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type res struct {
		body io.ReadCloser
		err  error
	}

	ch := make(chan res, len(p.Gateways))
	for _, gw := range p.Gateways {
		gw := gw
		go func() {
			body, err := p.fetchBody(ctx, gw, cid)
			ch <- res{body, err}
		}()
	}

	var firstErr error
	for i := 0; i < len(p.Gateways); i++ {
		r := <-ch
		if r.err == nil && r.body != nil {
			go func(start int) {
				for j := start; j < len(p.Gateways); j++ {
					rr := <-ch
					if rr.body != nil {
						rr.body.Close()
					}
				}
			}(i + 1)
			return r.body, nil
		}
		if r.body != nil {
			r.body.Close()
		}
		if firstErr == nil && r.err != nil {
			firstErr = r.err
		}
	}
	return nil, fmt.Errorf("ipfs: all %d gateways failed: %w", len(p.Gateways), firstErr)
}

// GetFilenameHint 实现 ContentProvider 接口。
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

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type res struct {
		data []byte
		err  error
	}

	ch := make(chan res, len(p.Gateways))
	for _, gw := range p.Gateways {
		gw := gw
		go func() {
			data, err := p.fetchBytes(ctx, gw, cid)
			ch <- res{data, err}
		}()
	}

	var firstErr error
	for i := 0; i < len(p.Gateways); i++ {
		r := <-ch
		if r.err == nil && r.data != nil {
			return r.data, nil
		}
		if firstErr == nil && r.err != nil {
			firstErr = r.err
		}
	}
	return nil, fmt.Errorf("ipfs: all %d gateways failed: %w", len(p.Gateways), firstErr)
}

// ─── per-gateway fetch ─────────────────────────────────────────────

func (p *IPFSProvider) fetchBody(ctx context.Context, gw, cid string) (io.ReadCloser, error) {
	var lastErr error
	for attempt := 1; attempt <= defaultMaxRetries; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		body, err := p.doRequest(ctx, gw, cid)
		if err != nil {
			lastErr = err
			if attempt < defaultMaxRetries {
				sleepBackoff(attempt)
			}
			continue
		}
		return body, nil
	}
	return nil, fmt.Errorf("gateway %s failed after %d retries: %w", gw, defaultMaxRetries, lastErr)
}

func (p *IPFSProvider) fetchBytes(ctx context.Context, gw, cid string) ([]byte, error) {
	body, err := p.fetchBody(ctx, gw, cid)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return io.ReadAll(body)
}

func (p *IPFSProvider) doRequest(ctx context.Context, gw, cid string) (io.ReadCloser, error) {
	url := fmt.Sprintf("%s/ipfs/%s", strings.TrimRight(gw, "/"), cid)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// ─── backoff ────────────────────────────────────────────────────────

func sleepBackoff(attempt int) {
	base := float64(defaultBaseInterval)
	ceil := float64(defaultMaxInterval)
	delay := time.Duration(math.Min(base*math.Pow(2, float64(attempt-1)), ceil))
	jitter := time.Duration(float64(delay) * (0.75 + rand.Float64()*0.5))
	time.Sleep(jitter)
}
