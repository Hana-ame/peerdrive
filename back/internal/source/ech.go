package source

// ech.go: EchSource — 用 echcore 域前置出口按内容地址取内容的数据源。
//
// 「取」与「地址」在这里汇合：sha 是地址（描述内容是什么），ECH 是出口（描述怎么把字节搬
// 过来）。EchSource = resolver（hash → 直连 CDN URL）+ egress（ECH 域前置取字节）+ 校验
// （整文件 sha256，内容寻址基线）。它是「冷路径」——首次从远端取内容并校验；取到后可登记
// 进 file_index/CAS，之后由 ShaSource 走「热路径」（本地读），两者不冲突。
//
// 与 URLSource 的关系：URLSource 用 fmt.Sprintf 模板把 hash 拼成 URL，client 可注入
// ech-proxy 的 Transport；EchSource 把 resolver 提成函数（iwara/exhentai 需要程序化解析，
// 不是模板能表达的——iwara 还要 X-Version 签名）、把出口提成 Egress（echcore 域前置，
// 而不是 HTTP 代理）。两者是同一模式的两种粒度，接口相同、可互换。
//
// 出口抽象：Egress 是「把一个 HTTP 请求发出去」的最小抽象。默认实现走 echcore.Do（ECH
// 域前置 + utls 指纹 + IP 族控制）；测试注入 httptest 假远端即可离线验证整条语义，
// 不需要真的拨号 Cloudflare。
//
// 注意：echcore 的 client 设了 CheckRedirect=ErrUseLastResponse（不自动跟随重定向），
// 所以 resolver 必须返回最终 URL——遇到 3xx 本源直接报错（静默跟随会绕过校验语义）。

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"peerdrive/internal/echcore"
)

// Resolver 把内容地址（sha256）解析成出口 URL。
//
// 解析失败（hash 无对应 URL、ID 反查不到、上游 API 拒绝）返回 error——调用方（Manager）
// 会把本源记为失败并继续降级下一个源。解析可能需要网络调用（iwara API 两步解析），
// 所以带 ctx。
type Resolver func(ctx context.Context, hash string) (string, error)

// Egress 出口抽象：把 HTTP 请求发出去。实现可以是 echcore.Do（ECH 域前置）、
// 直连 http.Client、HTTP 代理，或测试用的假远端。
type Egress interface {
	Do(ctx context.Context, req *http.Request) (*http.Response, error)
}

// egressFunc 函数式出口（测试与适配用）。
type egressFunc func(ctx context.Context, req *http.Request) (*http.Response, error)

func (f egressFunc) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	return f(ctx, req)
}

// echcoreEgress 默认出口：走 echcore.Do（ECH 域前置 + IP 族控制）。
type echcoreEgress struct {
	ipMode string // ""/"v4"/"v6"，空 = 用全局配置
}

func (e echcoreEgress) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	// echcore.Do 用 req 自带的 context，保证 ctx 已挂上去（Egress 契约要求用 ctx）。
	if req.Context() != ctx {
		req = req.WithContext(ctx)
	}
	if e.ipMode != "" {
		return echcore.Do(req, e.ipMode)
	}
	return echcore.Do(req)
}

// EchSource 是 ECH 出口的数据源（Source + MetaSource）。
type EchSource struct {
	name      string
	resolver  Resolver // 程序化解析（iwara/exhentai 需要多步 API 调用）
	template  string   // URL 模板（%s=hash，可选 %d=offset %d=size）；与 resolver 二选一
	egress    Egress
	verify    bool // 整文件 sha256 校验（内容寻址基线，默认开）
	rangeReq  bool // 是否发 Range 头（默认开）
	userAgent string
	headers   map[string]string

	mu       sync.RWMutex
	priority int
}

// EchOption EchSource 构造选项。
type EchOption func(*EchSource)

// WithEgress 注入出口（默认 echcore 域前置）。
func WithEgress(e Egress) EchOption {
	return func(s *EchSource) { s.egress = e }
}

// WithIPMode 设置 ECH 出口的 IP 族偏好（"v4"/"v6"）。
func WithIPMode(mode string) EchOption {
	return func(s *EchSource) { s.egress = echcoreEgress{ipMode: mode} }
}

// WithoutVerify 关闭整文件 sha256 校验（仅调试用；生产应开启——内容寻址的基线）。
func WithoutVerify() EchOption {
	return func(s *EchSource) { s.verify = false }
}

// WithoutRange 关闭 Range 头（不支持分片的出口/端点用；此时只声明 CapFile）。
func WithoutRange() EchOption {
	return func(s *EchSource) { s.rangeReq = false }
}

// WithUserAgent 覆盖 User-Agent。
func WithUserAgent(ua string) EchOption {
	return func(s *EchSource) { s.userAgent = ua }
}

// WithHeader 追加请求头（iwara 的 X-Site/Referer 等场景用）。
func WithHeader(k, v string) EchOption {
	return func(s *EchSource) {
		if s.headers == nil {
			s.headers = map[string]string{}
		}
		s.headers[k] = v
	}
}

// NewEchSource 创建一个 ECH 数据源。resolver 必需（hash → URL）；不传 egress 选项时
// 默认走 echcore.Do（ECH 域前置）。
func NewEchSource(name string, resolver Resolver, opts ...EchOption) *EchSource {
	if name == "" {
		name = "ech"
	}
	s := &EchSource{
		name:     name,
		resolver: resolver,
		egress:   echcoreEgress{},
		verify:   true,
		rangeReq: true,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// EchSourceFromTemplate 用 URL 模板构造 ECH 数据源（%s=hash，可选 %d=offset %d=size）。
// 适合「URL 形态规整」的来源（IPFS gateway、twimg 等）；需要程序化解析的（iwara/exhentai）
// 请用 NewEchSource + Resolver。
//
// 带 %d 的模板说明端点用自己的协议参数传分片（query 之类），此时 Range 头无意义，
// 自动关闭 rangeReq（只声明 CapFile）。
func EchSourceFromTemplate(name, template string, opts ...EchOption) *EchSource {
	s := NewEchSource(name, nil, opts...)
	s.template = template
	if strings.Contains(template, "%d") {
		s.rangeReq = false
	}
	return s
}

// resolveURL 解析出口 URL：优先用 resolver（程序化解析），其次按模板生成。
//
// 为什么模板在每次请求时重新 Sprintf：resolver 是「hash → URL」的纯函数，无法感知本次
// Open 的 offset/size；模板里的 %d 必须填**本次请求**的 offset/size 才有意义（否则分片
// 参数永远是解析期的占位值 0/-1，形同虚设）。这与 URLSource.buildURL 的语义一致。
func (s *EchSource) resolveURL(ctx context.Context, hash string, offset, size int64) (string, error) {
	if s.resolver != nil {
		url, err := s.resolver(ctx, hash)
		if err != nil {
			return "", fmt.Errorf("ech: resolve %s: %w", hash, err)
		}
		return url, nil
	}
	if s.template != "" {
		if strings.Contains(s.template, "%d") {
			return fmt.Sprintf(s.template, hash, offset, size), nil
		}
		return fmt.Sprintf(s.template, hash), nil
	}
	return "", fmt.Errorf("ech: no resolver or template configured")
}

func (s *EchSource) Name() string { return s.name }
func (s *EchSource) Type() string { return "ech" }

// Capabilities：Range 头支持 → CapStream；否则只 CapFile。整文件校验开启时加 CapVerify。
func (s *EchSource) Capabilities() Capability {
	c := CapFile
	if s.rangeReq {
		c |= CapStream
	}
	if s.verify {
		c |= CapVerify
	}
	return c
}

func (s *EchSource) Priority() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.priority
}

func (s *EchSource) SetPriority(p int) {
	s.mu.Lock()
	s.priority = p
	s.mu.Unlock()
}

// Available：不做真实网络探测（探测一次就消耗一次 ECH 建连成本，且「探测绿、请求红」
// 更难排查）。默认 true，失败交给 Manager 的路由统计（Stats.LastErr）暴露。
// Available 只看「配置完整性」：要么有 resolver，要么有模板；出口必须非 nil。
// 不做真实网络探测（一次探测就消耗一次 ECH 建连成本，且「探测绿、请求红」更难排查）；
// 失败交给路由统计（Stats.LastErr）暴露。
func (s *EchSource) Available(ctx context.Context) bool {
	if s.egress == nil || (s.resolver == nil && s.template == "") {
		return false
	}
	return true
}

// buildRequest 构造出口请求（Range 头 + 自定义头）。
func (s *EchSource) buildRequest(ctx context.Context, url string, offset, size int64) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// offset<0 归一化为 0：HTTP Range 的 start 不接受负数，传 -1 会发出 "bytes=-1-..." 这种
	// 畸形头（服务端要么 400 要么忽略）。size<0 = 到文件尾，不带上界。
	if offset < 0 {
		offset = 0
	}
	// 这里不拿 file length 给 end 钳位：EchSource 不知道总长（要做一次 HEAD 探测，
	// 多一次往返）。越界的 end（如 "bytes=8-107" 指向一个 10 字节文件）由服务端负责
	// 截断到文件尾（回 206 + 实际可得段）；如果 offset 已超出文件，服务端回 416，
	// 由调用方处理。与 URLSource 的 Range 语义保持一致。
	if s.rangeReq && (offset > 0 || size >= 0) {
		end := ""
		if size >= 0 {
			end = fmt.Sprintf("%d", offset+size-1)
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%s", offset, end))
	}
	if s.userAgent != "" {
		req.Header.Set("User-Agent", s.userAgent)
	}
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

// Open 流式打开：resolver 解析 URL → egress 取流 → 整文件时可选 sha256 校验。
func (s *EchSource) Open(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	r, _, err := s.OpenMeta(ctx, hash, offset, size)
	return r, err
}

// OpenMeta 实现 MetaSource：与 Open 同语义，额外返回元数据。Size 仅在「整文件请求 +
// 服务端返回 200 + Content-Length 存在」时可信（分片请求的 Content-Length 是分片长度，
// 不是总长），其余情况填 -1 表示未知。
func (s *EchSource) OpenMeta(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, *FileMeta, error) {
	if err := validHash(hash); err != nil {
		return nil, nil, err
	}
	url, err := s.resolveURL(ctx, hash, offset, size)
	if err != nil {
		return nil, nil, err
	}
	req, err := s.buildRequest(ctx, url, offset, size)
	if err != nil {
		return nil, nil, err
	}
	resp, err := s.egress.Do(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		resp.Body.Close()
		return nil, nil, fmt.Errorf("ech: HTTP %d (range not satisfiable)", resp.StatusCode)
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		resp.Body.Close()
		// echcore 不跟随重定向（CheckRedirect=ErrUseLastResponse），resolver 必须给最终 URL。
		return nil, nil, fmt.Errorf("ech: HTTP %d (redirect not followed; resolver must return the final URL)", resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, nil, fmt.Errorf("ech: HTTP %d", resp.StatusCode)
	}

	meta := &FileMeta{Hash: hash, Size: -1}
	var body io.ReadCloser
	if resp.StatusCode == http.StatusPartialContent {
		body = resp.Body
	} else {
		// 服务端不支持 Range（返回 200 全量）：抽取出 offset 段。
		// 正确性优先，带宽浪费可接受（首版不做 HEAD 探测）。
		if offset > 0 {
			if _, err := io.CopyN(io.Discard, resp.Body, offset); err != nil {
				resp.Body.Close()
				return nil, nil, err
			}
		}
		body = resp.Body
		if size >= 0 {
			body = &limitedReadCloser{r: io.LimitReader(resp.Body, size), c: resp.Body}
		}
	}
	// Content-Length 只在整文件 200 时代表总长（分片的 Content-Length 是分片长度）。
	if resp.StatusCode == http.StatusOK && offset == 0 && size < 0 {
		if n := resp.ContentLength; n >= 0 {
			meta.Size = n
		}
	}
	// 整文件请求 + 校验开启 → sha256 校验（内容寻址基线）。
	if s.verify && offset == 0 && size < 0 {
		body = &verifyReadCloser{r: body, hash: hash, label: "ech"}
	}
	return body, meta, nil
}

// Fetch 整文件取回（CapFile）。整文件必须校验（verifyReadCloser 在 EOF 时报错，
// ReadAll 会把它上抛）。
func (s *EchSource) Fetch(ctx context.Context, hash string) ([]byte, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	r, err := s.Open(ctx, hash, 0, -1)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// Info：ECH 源不做元数据查询（没有独立的元数据通道），返回 nil, nil。
func (s *EchSource) Info(ctx context.Context, hash string) (*FileMeta, error) {
	return nil, nil
}
