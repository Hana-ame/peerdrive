// IPFS 网关提供者 — 通过公共 IPFS 网关按 CID 获取文件内容。
// GetReader 并发尝试所有网关，返回第一个成功的响应体；
// 一旦某个网关成功，取消其余请求，避免 goroutine 泄漏。
// BitswapFetcher 回调可注入 boxo Bitswap 作为第一优先级。

package provider

import (
	"bytes"
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

// BitswapFetcher 尝试通过 Bitswap/DHT 获取 CID 对应的数据，
// 失败时返回 nil, nil；调用方应回退到其他方式（如 HTTP 网关）。
type BitswapFetcher func(ctx context.Context, cid string) ([]byte, error)

// IPFSProvider 管理一组公共 IPFS 网关。
// 如果设置了 BitswapFetcher，GetReader/FetchByCID 会先尝试 Bitswap，
// 失败后再回退到 HTTP 网关竞速。
type IPFSProvider struct {
	Gateways       []string
	bitswapFetcher BitswapFetcher
	client         *http.Client
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

// SetBitswapFetcher 设置 Bitswap 获取回调，使 Provider 优先走 Bitswap 网络。
func (p *IPFSProvider) SetBitswapFetcher(f BitswapFetcher) {
	p.bitswapFetcher = f
}

// GetReader 按 CID 获取文件内容。
// 优先尝试 Bitswap 网络获取，失败后回退到 HTTP 网关竞速。
func (p *IPFSProvider) GetReader(cid string) (io.ReadCloser, error) {
	// 1. Bitswap 优先
	if p.bitswapFetcher != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		data, err := p.bitswapFetcher(ctx, cid)
		cancel()
		if err == nil && data != nil {
			return io.NopCloser(bytes.NewReader(data)), nil
		}
	}

	// 2. HTTP 网关回退
	if len(p.Gateways) == 0 {
		return nil, fmt.Errorf("ipfs: no gateways configured and Bitswap unavailable")
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

// GetFilenameHint 返回文件名提示。
func (p *IPFSProvider) GetFilenameHint(cid, originalFilename string) string {
	if originalFilename != "" {
		return originalFilename
	}
	return cid
}

// FetchByCID 获取指定 CID 的完整数据。
// 优先 Bitswap 网络，失败后回退到 HTTP 网关竞速。
func (p *IPFSProvider) FetchByCID(ctx context.Context, cid string) ([]byte, error) {
	// 1. Bitswap 优先
	if p.bitswapFetcher != nil {
		data, err := p.bitswapFetcher(ctx, cid)
		if err == nil && data != nil {
			return data, nil
		}
	}

	// 2. HTTP 网关回退
	if len(p.Gateways) == 0 {
		return nil, fmt.Errorf("ipfs: no gateways configured and Bitswap unavailable")
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
