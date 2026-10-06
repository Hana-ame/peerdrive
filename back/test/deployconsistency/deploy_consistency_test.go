// deploy_consistency_test.go — 给三处**已经出过一次事**的部署约定加 CI 闸门。
//
// 背景（2026-10-06，全部是实测撞出来的，不是预防性检查）：
//
//  1. 信令 key 曾是硬编码在 6 处客户端代码里的字面量，部署脚本却每次重新随机生成，
//     于是**每部署一次，所有已发布客户端就全部连不上**（实测 13 次 bad handshake）。
//     key 改了而客户端没改，只有这样一条测试能发现。
//
//  2. 面板信令 key 我改错了文件——改的是 dist/ 与 back/internal/panel/ 下的**构建产物**，
//     真源是 packages/peerdrive-client/panel/{template.html,app.js}。
//     产物会被 build-panel.mjs 整个覆盖，所以那次改动等于没做。
//
//  3. back/internal/panel/panel.html 是**内嵌进二进制的副本**，必须与
//     packages/peerdrive-client/dist/panel.html 逐字节相同，否则 CI 的
//     client-package job 会红（它比的是 cmp，不是语义）。
//
// 这三条都是「改了一处、忘了另一处」型缺陷。人靠自觉会漏，CI 不会。
package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot 从测试文件位置向上找到仓库根（含 go.mod 的 back/ 的父目录）。
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Dir(dir)
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("找不到仓库根（向上 6 层都没有 go.mod）")
	return ""
}

// signalKeyRe 匹配形如 pd-signal-<hex> 的信令 key 字面量。
var signalKeyRe = regexp.MustCompile(`pd-signal-[0-9a-f]{8,}`)

// TestSignalKeyIsConsistentAcrossRepo 保证：**所有**写死信令 key 的地方用的是同一个值。
//
// 写法上刻意不硬编码期望值，而是要求「它们彼此相等」——
// 这样将来真的轮换 key，只需要改一处 + 改这一行期望，其余测试自动跟着走；
// 而漏改任何一处，这条立刻红。
func TestSignalKeyIsConsistentAcrossRepo(t *testing.T) {
	root := repoRoot(t)

	// 权威值取自 config.DefaultSignalKey 的定义处。
	cfg, err := os.ReadFile(filepath.Join(root, "back", "internal", "config", "config.go"))
	if err != nil {
		t.Fatalf("读 config.go: %v", err)
	}
	canonical := ""
	if m := signalKeyRe.FindAllString(string(cfg), -1); len(m) > 0 {
		canonical = m[0]
	}
	if canonical == "" {
		t.Fatal("config.go 里找不到 pd-signal-<hex>，key 约定被改掉了？")
	}

	files := []string{
		"back/internal/config/config.go",
		"back/peerjs/peer.go",
		"back/cmd/echclient/main.go",
		"back/cmd/media-node/main.go",
		"back/test/integration/live_test.go",
		"packages/peerdrive-client/panel/template.html",
		"packages/peerdrive-client/panel/app.js",
	}

	for _, rel := range files {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil {
				t.Fatalf("读 %s: %v", rel, err)
			}
			for i, line := range strings.Split(string(b), "\n") {
				for _, k := range signalKeyRe.FindAllString(line, -1) {
					if k == canonical {
						continue
					}
					// signalserver.go 里有 921 行的注释贴的是历史泄漏响应（真实 key），
					// 它是证据不是配置，不参与一致性检查——下面单独排除。
					if strings.HasPrefix(rel, "back/signalserver/") {
						continue
					}
					t.Errorf("%s:%d 用了不同的 key %q（权威值 %q）\n"+
						"  客户端连不上信令时，通常就是这里漏改了。\n"+
						"  改法：以 config.DefaultSignalKey 为准，同时更新这里。",
						rel, i+1, k, canonical)
				}
			}
		})
	}
}

// TestPanelSourceContainsSignalKey 保证面板**源文件**里真的有 key。
//
// 这条专治「改在产物上」：我只改过 back/internal/panel/panel.html 与 dist/panel.html，
// 而 build-panel.mjs 会从 panel/{template.html,app.js} 重建 dist/panel.html ——
// 改动被整个覆盖，CI 直到下一次构建才发现「不一致」。
func TestPanelSourceContainsSignalKey(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range []string{
		"packages/peerdrive-client/panel/template.html",
		"packages/peerdrive-client/panel/app.js",
	} {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil {
				t.Fatalf("读 %s: %v", rel, err)
			}
			if !signalKeyRe.Match(b) {
				t.Errorf("%s 里找不到 pd-signal-<hex>。\n"+
					"  key 必须写在**源文件**里：build-panel.mjs 会从 panel/ 重建 dist/panel.html，\n"+
					"  直接改 dist/ 或 back/internal/panel/ 下的产物会被下一次构建覆盖。", rel)
			}
		})
	}
}

// TestEmbeddedPanelIsByteIdenticalToDist 保证内嵌副本与 dist 逐字节相同。
//
// CI 的 client-package job 用 cmp 比对并会在不一致时红；但那要等 npm/Node 那条链路。
// 这里在 Go 侧再挡一道，让改面板的人**最早**在 go test 里就看到。
func TestEmbeddedPanelIsByteIdenticalToDist(t *testing.T) {
	root := repoRoot(t)
	pairs := [][2]string{
		{"packages/peerdrive-client/dist/panel.html", "back/internal/panel/panel.html"},
		{"packages/peerdrive-client/dist/peerjs.min.js", "back/internal/panel/peerjs.min.js"},
	}
	for _, p := range pairs {
		p := p
		t.Run(filepath.Base(p[1]), func(t *testing.T) {
			a, errA := os.ReadFile(filepath.Join(root, filepath.FromSlash(p[0])))
			b, errB := os.ReadFile(filepath.Join(root, filepath.FromSlash(p[1])))
			if errA != nil || errB != nil {
				t.Skipf("产物不存在（dist 未构建？）：%v / %v", errA, errB)
			}
			if string(a) != string(b) {
				t.Errorf("%s 与 %s 不一致（%d vs %d 字节）。\n"+
					"  修法：cd packages/peerdrive-client && npm run build:panel &&\n"+
					"        node scripts/vendor-peerjs.mjs && cp dist/panel.html ../../back/internal/panel/",
					p[0], p[1], len(a), len(b))
			}
		})
	}
}
