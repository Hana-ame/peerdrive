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
	"math"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

// Default retry configuration
const (
	defaultMaxRetries    = 3
	defaultBaseInterval  = 500 * time.Millisecond
	defaultMaxInterval   = 5 * time.Second
	defaultTotalTimeout  = 30 * time.Second
)

// IPFSProvider 管理一组公共 IPFS 网关，按优先级依次尝试获取文件。
type IPFSProvider struct {
	Gateways []string
}

// GetReader 通过 IPFS 网关获取指定 CID 的文件读取流。
// 依次尝试每个网关，每个网关最多重试 3 次（指数退避），返回第一个 HTTP 200 的响应体。
func (p *IPFSProvider) GetReader(cid string) (io.ReadCloser, error) {
	if len(p.Gateways) == 0 {
		return nil, fmt.Errorf("ipfs: no gateways configured")
	}
	ctx := context.Background()
	result := make(chan readCloserResult, len(p.Gateways))
	for _, gw := range p.Gateways {
		gw := gw
		go func() {
			r, err := p.tryGatewayWithRetry(ctx, gw, cid)
			result <- readCloserResult{reader: r, err: err}
		}()
	}
	// Collect the first successful result or all errors.
	var firstErr error
	for i := 0; i < len(p.Gateways); i++ {
		res := <-result
		if res.err == nil && res.reader != nil {
			return res.reader, nil
		}
		if firstErr == nil {
			firstErr = res.err
		}
	}
	return nil, fmt.Errorf("ipfs: all %d gateways failed for CID %s: %w", len(p.Gateways), cid, firstErr)
}

// GetFilenameHint 返回 CID 作为文件名提示。
func (p *IPFSProvider) GetFilenameHint(cid, originalFilename string) string {
	if originalFilename != "" {
		return originalFilename
	}
	return cid
}

// FetchByCID 通过 IPFS 网关获取指定 CID 的完整字节数据。
// 支持 context 取消/超时控制。每个网关最多重试 3 次（指数退避）。
func (p *IPFSProvider) FetchByCID(ctx context.Context, cid string) ([]byte, error) {
	if len(p.Gateways) == 0 {
		return nil, fmt.Errorf("ipfs: no gateways configured")
	}

	type byteResult struct {
		data []byte
		err  error
	}

	result := make(chan byteResult, len(p.Gateways))
	for _, gw := range p.Gateways {
		gw := gw
		go func() {
			data, err := p.tryGatewayFetchWithRetry(ctx, gw, cid)
			result <- byteResult{data: data, err: err}
		}()
	}

	var firstErr error
	for i := 0; i < len(p.Gateways); i++ {
		res := <-result
		if res.err == nil && res.data != nil {
			return res.data, nil
		}
		if firstErr == nil {
			firstErr = res.err
		}
	}
	return nil, fmt.Errorf("ipfs: all %d gateways failed for CID %s: %w", len(p.Gateways), cid, firstErr)
}

// ─── helpers ──────────────────────────────────────────────────────────

type readCloserResult struct {
	reader io.ReadCloser
	err    error
}

// tryGatewayWithRetry attempts to GET a CID from a single gateway with
// exponential backoff.  When fullBody is false, it returns the HTTP
// response body directly; when true it reads the full body.
func (p *IPFSProvider) tryGatewayWithRetry(ctx context.Context, gw, cid string) (io.ReadCloser, error) {
	var lastErr error
	for attempt := 1; attempt <= defaultMaxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		url := fmt.Sprintf("%s/ipfs/%s", strings.TrimRight(gw, "/"), cid)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt < defaultMaxRetries {
				backoff(attempt)
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			if attempt < defaultMaxRetries {
				backoff(attempt)
			}
			continue
		}
		return resp.Body, nil
	}
	return nil, fmt.Errorf("gateway %s failed after %d retries: %w", gw, defaultMaxRetries, lastErr)
}

// tryGatewayFetchWithRetry attempts to GET a CID from a single gateway with
// exponential backoff and returns the full body bytes.
func (p *IPFSProvider) tryGatewayFetchWithRetry(ctx context.Context, gw, cid string) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= defaultMaxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		url := fmt.Sprintf("%s/ipfs/%s", strings.TrimRight(gw, "/"), cid)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt < defaultMaxRetries {
				backoff(attempt)
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			if attempt < defaultMaxRetries {
				backoff(attempt)
			}
			continue
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			if attempt < defaultMaxRetries {
				backoff(attempt)
			}
			continue
		}
		return data, nil
	}
	return nil, fmt.Errorf("gateway %s failed after %d retries: %w", gw, defaultMaxRetries, lastErr)
}

// backoff sleeps with exponential backoff plus jitter based on the attempt number.
func backoff(attempt int) {
	base := float64(defaultBaseInterval)
	max := float64(defaultMaxInterval)
	delay := time.Duration(math.Min(base*math.Pow(2, float64(attempt-1)), max))
	// Add jitter: +/- 25%
	jitter := time.Duration(float64(delay) * (0.75 + rand.Float64()*0.5))
	time.Sleep(jitter)
}
