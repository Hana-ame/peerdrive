package serverapp

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// NextStepInfo 描述「服务起来了，用户接下来该做什么」。
//
// 为什么要有它：2026-10-06 实测首次运行 `peerdrive serve`，屏幕上刷过 30 行
// 启动日志（含 4 条 security 提示）之后**没有任何一个可点开的地址**。
// 而节点 ID 是每次启动随机生成的（PEERDRIVE_PEERJS_ID 为空时生成
// peerdrive-<random>），用户要拿它去连面板，只能回头翻日志、再手动拼 URL 参数。
//
// 也就是说：即使面板本身做到 file:// 双击即开，**不知道往里填什么，
// 就等于零学习成本没做到**。这个结构就是把那一步替用户做完并打印出来。
type NextStepInfo struct {
	NodeID    string // 本节点在 PeerJS 网络里的 ID
	Host      string // 监听地址（可能是 0.0.0.0 / :）
	Port      string
	Storage   string // 共享根目录
	PanelFile string // 公共面板单文件路径（存在时才给 URL）
	Secure    bool   // 是否 HTTPS/WSS
	PublicIP  string // 探测到的对外 IP，可能为空
}

// browserReachableHost 把监听地址换成浏览器能用的地址。
//
// 关键：0.0.0.0 / :: / 空 是「监听所有网卡」的写法，**浏览器填这个连不上**——
// 用户把 0.0.0.0 粘进面板只会看到连接失败。这是「照着提示填却失败」最常见的来源。
func browserReachableHost(host string) string {
	h := strings.TrimSpace(host)
	switch h {
	case "", "0.0.0.0", "::", "[::]", "*":
		return "127.0.0.1" // 只监听本地时，面板也在本地打开，用回环地址
	default:
		return h
	}
}

// lanIP 找一张非回环的 IPv4 地址，给同一局域网内的浏览器用。
// 找不到就返回空——宁可不填，也不要给一个连不上的。
func lanIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() {
				continue
			}
			if v4 := ipnet.IP.To4(); v4 != nil {
				return v4.String()
			}
		}
	}
	return ""
}

// PanelURL 拼出可直接点开的面板地址。
//
// 面板是单文件 HTML，file:// 双击即用，不需要装任何东西——这是「零学习成本」的
// 前提。参数取自 packages/peerdrive-client 的 URL 解析
// （URLSearchParams(location.search)，共 8 个：node/host/port/path/key/secure/auto/psk）。
//
// panelFile 为空时返回空字符串：没有面板文件就不给 URL，
// 别返回一个打不开的地址——那比不给更糟。
func (i NextStepInfo) PanelURL(panelFile string) string {
	if panelFile == "" || i.NodeID == "" {
		return ""
	}
	host := browserReachableHost(i.Host)
	secure := "0"
	if i.Secure {
		secure = "1"
	}
	// ⚠️ 必须是绝对路径：file:// 后面接相对路径（file://panel.html）在浏览器里
	// 会被解析成 file://panel.html/ 这种主机名，直接打不开。实测踩到过。
	abs, err := filepath.Abs(panelFile)
	if err != nil {
		abs = panelFile
	}

	// url.Values.Encode 会把 / 编成 %2F、: 编成 %3A，读起来不友好；
	// 面板自己会 decode，这里手工拼以保持链接可读、可手改。
	// key 固定 peerjs：面板默认信令的 key，与默认值一致（signalserver.NewServer(key)）。
	return fmt.Sprintf("file://%s?node=%s&host=%s&port=%s&path=%%2F&key=peerjs&secure=%s&auto=1",
		abs, i.NodeID, host, i.Port, secure)
}

// LocalURL 节点自身的健康检查地址，用于「先确认它活着」。
func (i NextStepInfo) LocalURL() string {
	scheme := "http"
	if i.Secure {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/swagger/index.html", scheme, net.JoinHostPort(browserReachableHost(i.Host), i.Port))
}

// PrintNextStep 把「接下来做什么」直接打到 stdout。
//
// ⚠️ 必须打 stdout 而不是 log 包：启动日志走的是结构化 logger，混在里面会被
// 滚动、会被日志级别过滤、被 grep 走。这里是给人看的一次性指引，
// 任何情况下都该出现在终端上。
func PrintNextStep(i NextStepInfo, panelFile string) {
	var b strings.Builder
	line := strings.Repeat("─", 64)

	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "  peerdrive 已启动\n\n")
	fmt.Fprintf(&b, "  %s\n", line)

	if id := i.NodeID; id != "" {
		fmt.Fprintf(&b, "  节点 ID     %s\n", id)
	}
	fmt.Fprintf(&b, "  共享目录    %s\n", i.Storage)
	fmt.Fprintf(&b, "  API 文档    %s\n", i.LocalURL())
	if lan := lanIP(); lan != "" {
		fmt.Fprintf(&b, "  局域网访问  http://%s:%s/swagger/index.html\n", lan, i.Port)
	}

	// 面板已由主服务内嵌托管在 /panel（internal/panel），所以这个地址
	// **总是**可给的——不依赖面板文件在不在磁盘上。file:// 兜底才是可选的。
	if i.NodeID != "" {
		fmt.Fprintf(&b, "\n  下一步：打开面板\n\n")
		scheme := "http"
		if i.Secure {
			scheme = "https"
		}
		fmt.Fprintf(&b, "    %s://%s/panel\n\n", scheme, net.JoinHostPort(browserReachableHost(i.Host), i.Port))
		fmt.Fprintf(&b, "    浏览器打开上面的地址即可开始使用，不需要再做任何配置。\n")
		if lan := lanIP(); lan != "" {
			fmt.Fprintf(&b, "    手机/其他设备用：%s://%s:%s/panel\n", scheme, lan, i.Port)
		}
		if url := i.PanelURL(panelFile); strings.HasPrefix(url, "file://") {
			fmt.Fprintf(&b, "    （离线面板文件：%s）\n", url)
		}
	}
	fmt.Fprintf(&b, "  %s\n\n", line)

	fmt.Print(b.String())
}
// panelFilePath 找公共面板单文件（packages/peerdrive-client/dist/panel.html）。
//
// 找不到就返回空——启动指引会据此省略「打开面板」那一段。
// 不给一个打不开的地址，比不给更糟：用户会以为是程序坏了。
//
// 查找顺序：
//  1. PEERDRIVE_PANEL —— 显式指定（发行版把它放在 /usr/share/peerdrive/panel.html 之类）
//  2. 相对本二进制 ../packages/peerdrive-client/dist/panel.html（开发态：go run / 本地构建）
//  3. 常见的安装位置
func panelFilePath() string {
	if p := os.Getenv("PEERDRIVE_PANEL"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		return ""
	}
	// 开发态：go run 或本地构建时，面板就在「仓库根/packages/peerdrive-client/dist」。
	// 从 back/ 往上两级正好是仓库根。
	for _, cand := range []string{
		filepath.Join("packages", "peerdrive-client", "dist", "panel.html"),
		filepath.Join("..", "packages", "peerdrive-client", "dist", "panel.html"),
		filepath.Join("..", "..", "packages", "peerdrive-client", "dist", "panel.html"),
		"panel.html",
	} {
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, cand := range []string{
			filepath.Join(dir, "panel.html"),
			filepath.Join(dir, "..", "packages", "peerdrive-client", "dist", "panel.html"),
			filepath.Join(dir, "..", "..", "packages", "peerdrive-client", "dist", "panel.html"),
		} {
			if _, err := os.Stat(cand); err == nil {
				return cand
			}
		}
	}
	return ""
}
