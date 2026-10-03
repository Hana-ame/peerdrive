// IPFS gateway provider -- fetches file content by CID through public IPFS gateways.
// GetReader concurrently tries all gateways and returns the first successful response body;
// once one gateway succeeds, the remaining requests are cancelled to avoid goroutine leaks.
// The BitswapFetcher callback can inject boxo Bitswap as the first priority.

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

// BitswapFetcher attempts to fetch data for a CID through Bitswap/DHT;
// returns nil, nil on failure; the caller should fall back to other methods (such as HTTP gateways).
type BitswapFetcher func(ctx context.Context, cid string) ([]byte, error)

// IPFSProvider manages a set of public IPFS gateways.
// If BitswapFetcher is set, GetReader/FetchByCID tries Bitswap first,
// then falls back to HTTP gateway racing on failure.
type IPFSProvider struct {
	Gateways       []string
	bitswapFetcher BitswapFetcher
	client         *http.Client
}

// NewIPFSProvider creates an IPFS gateway provider.
func NewIPFSProvider(gateways []string) *IPFSProvider {
	return &IPFSProvider{
		Gateways: gateways,
		client: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}
}

// SetBitswapFetcher sets the Bitswap fetch callback so the provider prefers the Bitswap network.
func (p *IPFSProvider) SetBitswapFetcher(f BitswapFetcher) {
	p.bitswapFetcher = f
}

// GetReader fetches file content by CID.
// Tries the Bitswap network first, then falls back to HTTP gateway racing on failure.
func (p *IPFSProvider) GetReader(cid string) (io.ReadCloser, error) {
	// 1. Bitswap first
	if p.bitswapFetcher != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		data, err := p.bitswapFetcher(ctx, cid)
		cancel()
		if err == nil && data != nil {
			return io.NopCloser(bytes.NewReader(data)), nil
		}
	}

	// 2. HTTP gateway fallback
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

// GetFilenameHint returns a filename hint.
func (p *IPFSProvider) GetFilenameHint(cid, originalFilename string) string {
	if originalFilename != "" {
		return originalFilename
	}
	return cid
}

// FetchByCID fetches the full data for a given CID.
// Tries Bitswap network first, then falls back to HTTP gateway racing on failure.
func (p *IPFSProvider) FetchByCID(ctx context.Context, cid string) ([]byte, error) {
	// 1. Bitswap first
	if p.bitsswapFetcher != nil {
		data, err := p.bitswapFetcher(ctx, cid)
		if err == nil && data != nil {
			return data, nil
		}
	}

	// 2. HTTP gateway fallback
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
