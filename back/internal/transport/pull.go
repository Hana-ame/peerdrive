// Package transport — pull：让节点替调用方去抓一个 URL 并入库。
//
// 为什么单开一个动词而不是复用 upload：upload 是「调用方有内容，推给我」，
// pull 是「调用方只有地址，让节点自己去取」。消费端是一个静态面板，手里可能
// 只有一个链接——把它取回来的活儿只能落到有网络能力的节点上。
//
// 这是本节点第一次出现「被动 outbound 请求」，所以它天然是一张风险面：
// 任何连得上的人都能指定一个 URL，等价于拿节点当代理。三重约束缺一不可：
//  1. 必须过 PSK 门禁（psk.go 的 servedVerbs）—— 配了密钥的节点只有持密钥者能用；
//  2. SSRF 防护（guardPullURL）：拒绝非 http(s)、拒绝内网/本机/链路本地地址，
//     且每一跳重定向都要重新校验（否则 302 一下就绕过首跳检查了）；
//  3. 大小与超时上限：流式截断到 MaxUploadBytes，超时由 context 兜住。
package transport

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"peerdrive/internal/log"
)

// pullMaxBytes 未配置 MaxUploadBytes 时的兜底上限（100MB）。
// 不设闸的话一个 10GB 的链接能把节点磁盘写满——拉取不像上传那样由调用方掌握
// 节奏，只能由接收方自己守。
const pullMaxBytes = 100 * 1024 * 1024

// pullTimeout 单次拉取的整体超时（含重定向与读完正文）。
const pullTimeout = 5 * time.Minute

// pullMaxRedirects 重定向跳数上限。每一跳都要走一遍 guardPullURL，
// 跳数太多既费资源也放大绕过面。
const pullMaxRedirects = 5

// servePull 处理 pull：本节点下载 URL 内容并登记进本地文件索引。
// 请求 {type:"pull", url, name?, reqId} → 响应 {type:"pulled", hash,size,name,path,reqId} | err
func (s *PeerJSService) servePull(c Session, r dcResp) {
	if strings.TrimSpace(r.URL) == "" {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "url required", ReqID: r.ReqID})
		return
	}
	u, err := url.Parse(r.URL)
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "pull: 无法解析 URL: " + err.Error(), ReqID: r.ReqID})
		return
	}
	if err := pullGuard(u); err != nil {
		// 原因要说清楚：运营者排查 SSRF 拦截全靠这句，一句含混的 forbidden
		// 只会让人以为是网络问题，然后反复重试。
		_ = c.SendJSON(dcResp{Type: "err", Msg: "pull: " + err.Error(), ReqID: r.ReqID})
		return
	}

	fi, err := s.fetchIntoIndex(u.String(), pullName(r.Name, u), s.pullMaxBytes())
	if err != nil {
		log.LogWarn("peerjs: pull %s failed: %v", u.Host, err)
		_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: r.ReqID})
		return
	}
	log.LogInfo("peerjs: pulled url=%s hash=%s size=%d", u.String(), fi.Hash, fi.Size)
	_ = c.SendJSON(dcResp{
		Type:  "pulled",
		Hash:  fi.Hash,
		Total: fi.Size,
		Name:  fi.Name,
		Path:  fi.Path,
		ReqID: r.ReqID,
	})
}

// pullMaxBytes 取配置的上限（未配置则用兜底常量）。
func (s *PeerJSService) pullMaxBytes() int64 {
	if s.cfg != nil && s.cfg.MaxUploadBytes > 0 {
		return s.cfg.MaxUploadBytes
	}
	return pullMaxBytes
}

// pullName 定落库文件名：调用方给了就用（WriteFile 内部会 sanitize），
// 否则取 URL 路径的最后一段——还拿不到就退回 host，保证索引里不留无名条目。
func pullName(name string, u *url.URL) string {
	if n := strings.TrimSpace(name); n != "" {
		return n
	}
	if base := path.Base(u.Path); base != "" && base != "." && base != "/" {
		return base
	}
	return u.Host
}

// fetchIntoIndex 拉取 URL 并写进本地文件索引，返回登记结果。
//
// 全程流式（不经内存整体缓冲）：URL 的大小不归我们控制，整体驻留内存会让一个
// 4GB 的链接直接打爆节点。超限即中止，不留半成品。
func (s *PeerJSService) fetchIntoIndex(rawURL, name string, maxBytes int64) (*FileInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pullTimeout)
	defer cancel()

	client := &http.Client{
		Timeout: pullTimeout,
		// 重定向逐跳校验：只校验首跳的话，公网 URL 302 到 127.0.0.1 就穿进内网了。
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= pullMaxRedirects {
				return fmt.Errorf("重定向超过 %d 跳，中止", pullMaxRedirects)
			}
			if err := guardPullURL(req.URL); err != nil {
				return fmt.Errorf("重定向目标被拒: %w", err)
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("pull: 构造请求失败: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pull: 请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("pull: HTTP %d", resp.StatusCode)
	}
	if cl := resp.ContentLength; cl > maxBytes {
		return nil, fmt.Errorf("pull: 内容 %d 字节超过本节点上限 %d 字节", cl, maxBytes)
	}
	return s.fileIndex.WriteFile(name, &limitedPuller{r: resp.Body, limit: maxBytes})
}

// limitedPuller 包一层 io.Reader：读超过 limit 就返回错误。
// io.LimitReader 到限只给 EOF（静默截断），入库必须把它暴露成错误——
// 否则一个超限的 URL 会被当成成功入库，hash 对应的其实是个截断文件。
type limitedPuller struct {
	r     io.Reader
	limit int64
	seen  int64
	fail  error
}

func (l *limitedPuller) Read(p []byte) (int, error) {
	if l.fail != nil {
		return 0, l.fail
	}
	n, err := l.r.Read(p)
	l.seen += int64(n)
	if l.seen > l.limit {
		l.fail = fmt.Errorf("pull: 内容超过本节点上限 %d 字节（超限即中止，不落半个文件）", l.limit)
		return 0, l.fail
	}
	return n, err
}

// pullGuard servePull 实际使用的 SSRF 判定钩子。默认就是 guardPullURL。
//
// 存在理由（不是为图方便）：httptest 起的服务器只能绑 127.0.0.1，而 guardPullURL
// 理所当然地拒绝 loopback —— 没有这个钩子的话，成功路径、404、超限、重定向
// 这些**真正跑 HTTP 的用例一条也练不到**（全在被拦在门口的第一步）。
// 测试里只允许「放行某个特定 host」，其余仍走真判定，所以替代实现不能图省事
// 写 `return nil`。
var pullGuard = guardPullURL

// guardPullURL SSRF 防护：只允许公网 http(s)，拒绝内网/本机/链路本地地址。
//
// 为什么必须做：servePull 是「被动 outbound」——指向性由外部提供。没有这层，
// 一个能连上节点的 peer 就能拿它去扫内网（http://192.168.1.1/、云厂商 metadata
// 169.254.169.254 都在这张面里）。证书是否可信不加限制（那是另一回事），
// 判断只看解析出来的 IP。
//
// ⚠️ 生产路径请只经 pullGuard 调用它（见下）。
func guardPullURL(u *url.URL) error {
	if u == nil {
		return fmt.Errorf("空 URL")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("只支持 http/https（收到 %q）", u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("URL 不许带用户信息（http://user@host 常用于绕过主机判定）")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL 缺少主机名")
	}
	if strings.EqualFold(host, "localhost") {
		return fmt.Errorf("禁止拉取 localhost")
	}
	// 字面量 IP 与 DNS 都要拦：除了 127.0.0.1 还有 ::1、169.254.169.254，以及
	// DNS 指到内网的域名——所以解析后逐个判，而不是做 hostname 字符串匹配。
	if ip := net.ParseIP(host); ip != nil {
		return guardPullIP(ip)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("域名解析失败: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("域名没有解析到任何地址")
	}
	for _, ia := range ips {
		if err := guardPullIP(ia.IP); err != nil {
			return err
		}
	}
	return nil
}

// guardPullIP 判断单个 IP 是否落在禁止拉取的范围。
func guardPullIP(ip net.IP) error {
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return fmt.Errorf("禁止拉向内网/本机地址（%s）", ip.String())
	}
	// IPv4-mapped IPv6（::ffff:127.0.0.1）会绕过上面那组判定，显式解一层。
	if v4 := ip.To4(); v4 != nil {
		private := v4[0] == 127 || v4[0] == 10 || v4[0] == 0 ||
			(v4[0] == 172 && v4[1]&0xf0 == 16) ||
			(v4[0] == 192 && v4[1] == 168) ||
			(v4[0] == 169 && v4[1] == 254) ||
			v4[0] >= 224
		if private {
			return fmt.Errorf("禁止拉向内网/本机地址（%s）", v4.String())
		}
	}
	return nil
}
