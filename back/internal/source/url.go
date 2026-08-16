package source

// url.go：URLSource——按 URL 模板拉取内容（HTTP source）。
// 用途：
//   - 外部资源表：hash → 已知 URL（如 https://gateway/ipfs/<hash>）
//   - 可注入 Transport 指向 ech-proxy 等网络出口（wintools 的 ech-proxy
//     可作为本 source 的出站代理，不需要独立 source 类型）
//
// 语义：
//   - 模板用 fmt.Sprintf：%s = hash；%d（可选两次）= offset, size
//   - 支持 CapStream：优先带 Range 头请求（服务器支持 206 则流式分片）；
//     服务器回 200 全量时截取 offset..offset+size 段（带宽浪费但正确）
//   - 全量请求（offset==0,size<0）读取完成后校验 sha256（内容寻址兜底）——
//     URL 源内容可能被篡改，校验是内容寻址语义的底线
//   - Info 不支持（HTTP HEAD 元数据留给未来）

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"strings"
	"sync"
)

// URLSource HTTP URL 文件源。
type URLSource struct {
	name     string
	template string // fmt 模板：%s=hash，可选 %d=offset %d=size
	client   *http.Client
	caps     Capability // 由模板推导（见 NewURLSource）

	mu       sync.RWMutex
	priority int
}

// NewURLSource 创建 URL 源。template 示例：
//
//	"https://example.com/ipfs/%s"                    （不支持分片，CapFile）
//	"https://example.com/f/%s?off=%d&size=%d"        （支持分片，CapStream）
//
// client 可为 nil（默认 http.Client）；注入自定义 Transport（如 ech-proxy
// 出口代理）时传带该 Transport 的 client。
func NewURLSource(template string, client *http.Client) *URLSource {
	if client == nil {
		client = http.DefaultClient
	}
	s := &URLSource{name: "url", template: template, client: client}
	// 模板含 %d → 支持 range 参数 → CapStream；否则 CapFile
	if strings.Contains(template, "%d") {
		s.caps = CapStream
	} else {
		s.caps = CapFile
	}
	return s
}

func (s *URLSource) Name() string { return s.name }
func (s *URLSource) Type() string { return "url" }

// Capabilities 由模板决定：含 %d（offset/size 参数）→ 流式分片。
func (s *URLSource) Capabilities() Capability { return s.caps }

func (s *URLSource) Priority() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.priority
}

func (s *URLSource) SetPriority(p int) {
	s.mu.Lock()
	s.priority = p
	s.mu.Unlock()
}

// Available URL 源无主动健康检查（ping 会浪费请求）——第一版恒 true，
// 失败由路由统计暴露（LastErr）。未来可加最近成功时间窗。
func (s *URLSource) Available(ctx context.Context) bool { return true }

// buildURL 按模板生成请求 URL。模板含 %d 时补 offset/size（size<0 → -1，
// 由服务器语义决定；这里传 -1 表示整段）。
func (s *URLSource) buildURL(hash string, offset, size int64) string {
	if s.caps&CapStream != 0 {
		return fmt.Sprintf(s.template, hash, offset, size)
	}
	return fmt.Sprintf(s.template, hash)
}

// Open 流式打开（需模板含 %d）。
func (s *URLSource) Open(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	url := s.buildURL(hash, offset, size)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if offset > 0 || size >= 0 {
		// Range 请求：bytes=start-end（size<0 → 到文件尾）
		end := ""
		if size >= 0 {
			end = fmt.Sprintf("%d", offset+size-1)
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%s", offset, end))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable || resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, fmt.Errorf("url: HTTP %d", resp.StatusCode)
	}
	var body io.ReadCloser
	if resp.StatusCode == http.StatusPartialContent {
		body = resp.Body
	} else {
		// 服务器不支持 Range（回 200 全量）：截取 offset 段。
		// 正确性优先，带宽浪费可接受（第一版不做 HEAD 探测）
		if offset > 0 {
			if _, err := io.CopyN(io.Discard, resp.Body, offset); err != nil {
				resp.Body.Close()
				return nil, err
			}
		}
		body = resp.Body
		if size >= 0 {
			body = &limitedReadCloser{r: io.LimitReader(resp.Body, size), c: resp.Body}
		}
	}
	// 全量请求（offset==0 && size<0）→ 读取时校验 sha256（内容寻址兜底）
	if offset == 0 && size < 0 {
		return &verifyReadCloser{r: body, hash: hash}, nil
	}
	return body, nil
}

// Fetch 整体获取（CapFile 模板或防御性实现）。
func (s *URLSource) Fetch(ctx context.Context, hash string) ([]byte, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	url := s.buildURL(hash, 0, -1)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("url: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	// 内容寻址兜底：整体获取必须校验 sha256
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != hash {
		return nil, fmt.Errorf("url: content hash mismatch for %s", hash)
	}
	return data, nil
}

// Info URL 源不做元数据查询。
func (s *URLSource) Info(ctx context.Context, hash string) (*FileMeta, error) {
	return nil, nil
}

// verifyReadCloser 读取完成后校验 sha256（内容寻址兜底——URL 源内容可能
// 被篡改，不校验会把错误内容当作寻址文件使用）。校验在 EOF 时执行：
// 成功 → 返回 io.EOF；失败 → 返回 hash mismatch 错误（ReadAll 会拿到）。
type verifyReadCloser struct {
	r    io.ReadCloser
	hash string
	h    hash.Hash

	done bool
	err  error
}

func (v *verifyReadCloser) Read(p []byte) (int, error) {
	if v.done {
		return 0, v.err
	}
	n, err := v.r.Read(p)
	if n > 0 {
		if v.h == nil {
			v.h = sha256.New()
		}
		v.h.Write(p[:n])
	}
	if err == io.EOF {
		v.done = true
		if v.h == nil || hex.EncodeToString(v.h.Sum(nil)) != v.hash {
			v.err = fmt.Errorf("url: content hash mismatch for %s", v.hash)
		} else {
			v.err = io.EOF
		}
		return n, v.err
	}
	if err != nil {
		v.done = true
		v.err = err
	}
	return n, err
}

func (v *verifyReadCloser) Close() error { return v.r.Close() }
