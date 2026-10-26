package collection

// fetch.go: 条目「多备选源」取文件语义 + 降级轨迹记录。
//
// 一个条目的文件可以从多个备选源取到（见 SourceRef）：sha（内容寻址热路径，
// 本地 sha-文件系统 / file_index / 对端）、ech-url（ECH/ech-proxy 出口冷路径）、
// url（直连冷路径）、private.url（私有源）。取文件语义 = 「任一可用即可」：
// 按优先级 sha → ech-url → url → private.url 依次尝试，第一条成功的流就是答案。
//
// 为什么优先级是这个顺序：sha 是内容寻址——命中即「字节就是该地址指向的内容」，
// 不需要再校验（本地权威）；ech-url 排第二是因为它通常比直连更可达（CN 场景
// pbs.twimg.com 直连常被墙，pbs.moonchan.xyz 反代可达）；url 直连兜底；private
// 只在节点被授权时由调用方决定是否尝试（本包只负责把地址作为最后一个候选暴露）。
//
// 与 source 包的衔接：collection 是叶子包，不 import source。sha 备选通过注入的
// SHAOpener 解析（source.Manager.OpenAny / ShaSource.Open / collection.ReadFile
// 都满足签名），http 备选走注入的 *http.Client（可复用 ech-proxy 的 Transport）。
// 每次尝试都记入 FetchMonitor（见 monitor.go），降级轨迹（先 sha 失败 → 回退
// ech-url → 回退 url…）与各源访问次数/成功率分布因此可查询。
//
// 字节计数约定：http 备选在打开时记录响应 Content-Length（未知时为 0），流式
// 读取后的实际字节数不追溯更新——监控的目的是分布与轨迹，不是精确字节审计。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"peerdrive/internal/log"
	"peerdrive/pkg/urlguard"
)

// 备选源名字（监控统计键，同时也是汇报里的稳定标识）。
const (
	AltSHA     = "sha"
	AltECHURL  = "ech-url"
	AltURL     = "url"
	AltPrivate = "private.url"
)

// SHAOpener 把一个内容地址（64-hex sha256）解析成可读流。source.Manager /
// ShaSource / collection.ReadFile 的读取路径都满足这个签名；collection 故意
// 不 import source，保持依赖叶子形状。
type SHAOpener func(ctx context.Context, sha string) (io.ReadCloser, error)

// FetchOptions 是单次取文件的依赖注入点。
type FetchOptions struct {
	// OpenSHA 解析 sha 备选（nil 时 sha 备选直接判失败）。
	OpenSHA SHAOpener
	// Client 用于 url / ech-url / private.url 备选（nil = http.DefaultClient）。
	// 注入带 ech-proxy Transport 的 client 即可让冷路径走 ech-proxy 出口。
	Client *http.Client
	// Monitor 记录每次尝试（nil = 不记录）。
	Monitor *FetchMonitor
}

func (opt FetchOptions) client() *http.Client {
	if opt.Client != nil {
		return opt.Client
	}
	// 闸门开着且调用方没注入 client 时，回退到带 SafeDialContext 的 client，
	// 而不是 http.DefaultClient。两个原因：DefaultClient 没有超时，一个对端塞进
	// 合集条目的黑洞 IP 能一直占着这条 goroutine；而且它不做任何目标 IP 校验，
	// 302 跳到内网地址会在 dial 层被直接放行。
	if guardCollectionFetch {
		return guardedClient()
	}
	return http.DefaultClient
}

// guardedClient 进程级共享的安全 client。30s 超时对齐 urlguard.NewSafeClient 的
// 默认值。
var (
	guardedClientOnce sync.Once
	guardedClientV    *http.Client
)

func guardedClient() *http.Client {
	guardedClientOnce.Do(func() { guardedClientV = urlguard.NewSafeClient(30 * time.Second) })
	return guardedClientV
}

// guardCollectionFetch 控制集合条目的 url 备选要不要过 SSRF 闸门。
//
// 默认开。集合条目里的 Source.URL / ECHURL / Private.URL 是对端可控的：匿名合集
// 可由任何有权限的用户创建并同步过来，而这条链最终落到本节点的一个出站 HTTP
// 请求上。闸门开着时拦 127.0.0.1 / 192.168.x / 169.254.169.254(IMDS) 一类内网
// 目标；关掉用于「自建内网图床合集」这种合法但需要运维显式接受的场景。
var guardCollectionFetch = true

// SetGuardCollectionFetch 在启动期由 serverapp 调用一次。
func SetGuardCollectionFetch(on bool) { guardCollectionFetch = on }

// GuardCollectionFetchEnabled 供启动日志与自检确认当前姿态。
func GuardCollectionFetchEnabled() bool { return guardCollectionFetch }

// Alt 是一个备选源的描述。
type Alt struct {
	Name string // AltSHA / AltECHURL / AltURL / AltPrivate
	SHA  string // 仅 AltSHA：内容地址
	URL  string // http 备选的完整 URL
}

// Alternatives 返回条目按优先级排序的备选源列表。
func (e *Entry) Alternatives() []Alt {
	var out []Alt
	if sha := e.effectiveSHA(); sha != "" {
		out = append(out, Alt{Name: AltSHA, SHA: sha})
	}
	if e.Source == nil {
		return out
	}
	if e.Source.ECHURL != "" {
		out = append(out, Alt{Name: AltECHURL, URL: e.Source.ECHURL})
	}
	if e.Source.URL != "" {
		out = append(out, Alt{Name: AltURL, URL: e.Source.URL})
	}
	if e.Source.Private != nil && e.Source.Private.URL != "" {
		out = append(out, Alt{Name: AltPrivate, URL: e.Source.Private.URL})
	}
	return out
}

// effectiveSHA 归一化两个 sha 字段：Source.SHA 优先（新格式），顶层 SHA 兼容
// 旧格式。Validate 已保证两者同时出现时一致。
func (e *Entry) effectiveSHA() string {
	if e.Source != nil && e.Source.SHA != "" {
		return e.Source.SHA
	}
	return e.SHA
}

// Fetch 按优先级尝试条目的全部备选源，返回第一条成功流的 io.ReadCloser 与
// 命中备选的名字。每次尝试都记录进 Monitor（设置了的话），全部失败返回带
// 最后一条失败原因的聚合错误。
func (e *Entry) Fetch(ctx context.Context, opt FetchOptions) (io.ReadCloser, string, error) {
	alts := e.Alternatives()
	if len(alts) == 0 {
		// Validate 挡掉了这个输入，但防御性保留：nil/空条目直接取会 panic 在
		// 别处（例如 URL 拼接），这里显式失败比隐式错误好排查。
		return nil, "", errors.New("collection: entry has no fetchable alternative")
	}
	var lastErr error
	for _, a := range alts {
		rc, err := e.fetchAlt(ctx, a, opt)
		if err == nil {
			return rc, a.Name, nil
		}
		lastErr = err
	}
	return nil, "", fmt.Errorf("collection: entry %q: all alternatives failed: %w", e.Path, lastErr)
}

// fetchAlt 尝试单个备选源并记录尝试（成功/失败/耗时/字节数）。
func (e *Entry) fetchAlt(ctx context.Context, a Alt, opt FetchOptions) (io.ReadCloser, error) {
	start := time.Now()
	var rc io.ReadCloser
	var err error
	var bytes int64
	switch a.Name {
	case AltSHA:
		if opt.OpenSHA == nil {
			err = errors.New("sha alternative requested but no OpenSHA resolver configured")
		} else {
			rc, err = opt.OpenSHA(ctx, a.SHA)
		}
	default:
		rc, bytes, err = fetchHTTP(ctx, opt.client(), a.URL)
	}
	rec := Attempt{Alt: a.Name, At: start, Duration: time.Since(start), OK: err == nil, Bytes: bytes}
	if err != nil {
		rec.Err = err.Error()
	}
	if opt.Monitor != nil {
		opt.Monitor.Record(rec)
	}
	log.LogDebug("collection: fetch %q via %s: ok=%v bytes=%d dur=%s",
		e.Path, a.Name, err == nil, bytes, time.Since(start).Round(time.Millisecond))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", a.Name, err)
	}
	return rc, nil
}

// fetchHTTP GET 一个备选 URL，返回响应体与已知字节数（Content-Length；未知为 0）。
func fetchHTTP(ctx context.Context, client *http.Client, rawURL string) (io.ReadCloser, int64, error) {
	// 闸门：URL 来自集合条目，经 collection.New 的 JSON 解析进来，而集合可以被
	// 对端同步/由第三方创建。只校验 scheme+host 不够——validateSourceURL 会放行
	// https://169.254.169.254/ 和 https://127.0.0.1:8080/admin。这里在发起请求前
	// 做 IP 归属校验；client 侧的 SafeDialContext 是第二层，堵 302/DNS rebinding。
	if guardCollectionFetch {
		if err := urlguard.GuardExternalURL(rawURL); err != nil {
			return nil, 0, fmt.Errorf("ssrf guard: %w", err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	n := int64(0)
	if resp.ContentLength > 0 {
		n = resp.ContentLength
	}
	return resp.Body, n, nil
}
