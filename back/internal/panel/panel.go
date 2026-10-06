// Package panel 内嵌公共面板单文件，让二进制自带界面。
//
// 为什么内嵌：零学习成本的前提是「下载一个二进制就能用」。
// 之前面板在 packages/peerdrive-client/dist/panel.html，用户得另外找到它、
// 自己填 node/host/port 拼 URL。内嵌之后 `peerdrive` 直接把它托管在
// /panel 上——浏览器打开 http://127.0.0.1:3000/panel 就是一个填好参数的可用界面，
// 不需要装任何东西、不需要 Node、不需要打开本地文件。
//
// 为什么不直接把 panel.go 放回 packages/：那是另一个 module（无 go.mod 归 back 管
// 的前提下不能被 back 的二进制 import），而 dist/ 又在 CI 里做漂移校验。
// 这里内嵌的是同一份文件，CI 那边 check:panel 的漂移校验继续守着它。
package panel

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

//go:embed panel.html
var panelHTML []byte

// peerjs.min.js 一并内嵌。
//
// 为什么：面板默认按「同目录副本 → jsdelivr → unpkg」回退加载 peerjs 库。
// 二进制发布的场景里**同目录没有这个文件**（用户手里只有一个可执行文件），
// 于是每次都落到 CDN——而 CDN 的可达性与时延不可控：内网、墙、慢网都直接
// 表现为「面板一直连不上」。e2e.yml 里记为 known-flaky 的面板用例正是这个原因，
// 已记录两年（2026-10-04）。内嵌之后这条回退链的第一环恒定命中。
//
//go:embed peerjs.min.js
var peerjsJS []byte

// PeerJSJS 返回 peerjs 库字节（供 /peerjs.min.js 路由使用）。
func PeerJSJS() []byte { return peerjsJS }

// HTML 返回面板单文件的字节内容。
//
// Content-Type 必须是 text/html：浏览器对 file:// 与 http:// 的嗅探规则不同，
// 标成 application/octet-stream 会被当成下载而不是打开。
func HTML() []byte { return panelHTML }

// Handler 把面板挂在指定路径上。
//
// ⚠️ 路径必须精确匹配，不能用前缀：面板内部会请求同源的 API
//（/peerjs/* 等），挂成前缀会把那些 API 也吃掉。
// 根路径 "/" 交给调用方决定是否接管——它同时是 API 的兜底，不能抢。
func Handler(path string) http.Handler {
	p := "/" + strings.Trim(path, "/")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != p {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(withBootstrap(panelHTML, r.Host, p))
	})
}

// bootstrap 为托管的面板注入「我已经知道该连谁」的前置脚本。
//
// ⚠️ 面板默认要用户手填 node/host/port/key 四项才能用——这不是零学习成本。
// 但面板是由**某个具体节点**托管的：同源就意味着 host/port 就是这个节点，
// 节点 ID 也能问它自己（/peerjs/node）。所以这里在面板脚本之前插一段，
// 把这些参数补进 URL；已显式给出的参数不覆盖（用户手填的优先）。
//
// 注入点在第一个 <script> 之前，且用 location.search 改写而非替换正文：
// 面板自己的 URLSearchParams(location.search) 会照常读到。
func withBootstrap(html []byte, host, panelPath string) []byte {
	_ = host // 面板同源，host/port 直接从 location 取即可
	// 内嵌小脚本：同源 → 反查本节点 ID → 补全 URL 参数 → 直接开始连接。
	const bs = `(() => {
  try {
    const u = new URL(location.href);
    // 已经手填过就别动（用户显式指定优先于自动推断）
    if (u.searchParams.get('node')) return;
    u.searchParams.set('auto', '1');
    // 向托管本页的节点问两件事：我是谁 + 我在哪个信令上。
    // ⚠️ 信令参数必须问节点，不能用 location：**面板要连的是「这个节点所在的
    // 信令」**，不是托管面板的那个 HTTP 端口。实测填本机 3000 会 ws 404。
    fetch(%s).then(r => r.json()).then(j => {
      if (!j || !j.id) return;
      u.searchParams.set('node', j.id);
      if (j.signal_host) u.searchParams.set('host', j.signal_host);
      if (j.signal_port) u.searchParams.set('port', j.signal_port);
      if (j.signal_key)  u.searchParams.set('key', j.signal_key);
      if (j.signal_path) u.searchParams.set('path', j.signal_path);
      u.searchParams.set('secure', j.signal_secure ? '1' : '0');
      history.replaceState(null, '', u.toString());
      location.reload();
    }).catch(() => {});
  } catch (e) { /* 推断失败就退回手填，不影响面板本身 */ }
})();`

	js := fmt.Sprintf(bs, jsonStr("/peerjs/node"))
	tag := "<script>" + js + "</script>\n"
	idx := strings.Index(string(html), "<script>")
	if idx < 0 {
		return html
	}
	out := make([]byte, 0, len(html)+len(tag))
	out = append(out, html[:idx]...)
	out = append(out, tag...)
	out = append(out, html[idx:]...)
	return out
}

// jsonStr 把字符串编码成可安全嵌进 JS 字面量的形式（防面板路径里的引号/尖括号）。
func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
