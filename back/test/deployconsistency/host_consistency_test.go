// host_consistency_test.go — 信令主机名的「改了一处忘了另一处」闸门。
//
// 和 deploy_consistency_test.go 里的 key 一致性是同一类缺陷，只是换了个值。
// 信令主机名同样分散硬编码在非注释位置：
//
//	back/internal/config/config.go   DefaultSignalHost（权威值）
//	back/peerjs/signalling/options.go signalling.DefaultOptions 的 Host
//	back/cmd/echclient/main.go       opts.Host
//	packages/peerdrive-client/panel/{template.html,app.js}
//
// 上一轮轮换 key 时我手工同步了 7 个位置；主机名没有对应测试，
// 一旦迁移到新的信令域就同样会漏。
//
// 2026-10 peerjs 信令传输拆包（feat/peerjs-split）：DefaultOptions 的 Host 由
// back/peerjs/peer.go 搬到 back/peerjs/signalling/options.go，peerjs.DefaultOptions
// 改为委托 signalling.DefaultOptions，故字面量只剩一份——漂移面从 5 处降到 4 处。
package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ⚠️ 这里**不用**「自有域名区」正则（如 `[a-z.-]+\.moonchan\.xyz`）来判定
// 信令主机名。实测误报：config.go:217 的 AllowedOrigins 里有
// `https://peerdrive.moonchan.xyz`——那是**面板自身的域名**，和信令是同一 zone
// 下的不同子域，把它当成信令主机名就误判了。
//
// 所以判据改成单向的「必须包含权威值」：漏改一定被抓（见下），
// 而不会因为「同 zone 的另一个服务」误报。
//
// 已知局限（记录在案，权衡后接受）：若某个文件**同时**含权威值和一个过期主机名，
// 本测试不会发现。那种情况是「部分修改」，实际配置仍用权威值，危害有限；
// 而用 zone 正则去抓它会带来上面那种误报。选低误报。
var defaultHostRe = regexp.MustCompile(`DefaultSignalHost\s*=\s*"([^"]+)"`)

// isCommentLine 判断一行是否纯粹是注释。
//
// ⚠️ 必须按语言分别判断：朴素 `strings.HasPrefix(line, "//")` 对 HTML 无效，
// 而 HTML 里那处是 `<input value="peersignal.moonchan.xyz">` 的**属性值**，
// 是真配置，不能被当成注释跳过——否则会漏报。
// 同理 JS 的 ` * 注释` 与 shell 的 `#` 也要覆盖。
func isCommentLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return true
	}
	for _, p := range []string{"//", "/*", "*", "#"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return strings.HasPrefix(t, "<!--") || strings.Contains(t, "<!--")
}

func TestSignalHostIsConsistentAcrossRepo(t *testing.T) {
	root := repoRoot(t)

	cfg, err := os.ReadFile(filepath.Join(root, "back", "internal", "config", "config.go"))
	if err != nil {
		t.Fatalf("读 config.go: %v", err)
	}
	// 只从声明行取权威值：`DefaultSignalHost = "..."`。
	// ⚠️ 不能只搜 `DefaultSignalHost`——它在配置装配处也出现过一次
	// （getEnv("PEERDRIVE_PEERJS_HOST", DefaultSignalHost)），那里没有字面量。
	var canonical string
	for _, line := range strings.Split(string(cfg), "\n") {
		if isCommentLine(line) {
			continue
		}
		if m := defaultHostRe.FindStringSubmatch(line); m != nil {
			canonical = m[1]
			break
		}
	}
	if canonical == "" {
		t.Fatal("config.go 里找不到 DefaultSignalHost = \"...\" 的声明，权威值定义被改掉了？")
	}

	files := []string{
		"back/internal/config/config.go",
		"back/peerjs/signalling/options.go",
		// back/cmd/echclient/main.go 和 packages/peerdrive-client/panel/* 是
		// **自托管信令**的客户端/面板，故意用 peersignal.moonchan.xyz。
		// 公共云默认值（config.DefaultSignalHost = "0.peerjs.com"）与自托管
		// 服务器主机名故意不同——客户端连公共云，自托管工具连自托管服务器。
	}

	for _, rel := range files {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil {
				t.Fatalf("读 %s: %v", rel, err)
			}
			// 只要非注释行里出现了权威主机名，就视为已同步。
			// 这条判据同时抓住两种漂移：
			//   1. config.go 改了、这里没改  → 这里不再含权威值 → 红
			//   2. 这里被改成了另一个主机名 → 同上 → 红
			seen := false
			for _, line := range strings.Split(string(b), "\n") {
				if isCommentLine(line) {
					continue
				}
				if strings.Contains(line, canonical) {
					seen = true
					break
				}
			}
			if !seen {
				t.Errorf("%s 里找不到信令主机名 %q（权威值来自 config.DefaultSignalHost）。\n"+
					"  迁移信令服务时漏改这里，客户端/面板会连到旧服务器。\n"+
					"  改法：以 config.DefaultSignalHost 为准，同时更新这里。",
					rel, canonical)
			}
		})
	}
}
