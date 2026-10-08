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
//
// 2026-10-06 补：第 1 条自身也翻车了。key 在 Go 代码里统一了，但**文档里的
// 12 处**（AGENTS.md 线上部署节 / README / 教程 / PEERSIGNAL.md / design/ /
// layers/ / VERIFICATION_CHECKLIST.md / media demo）还写着上一代的值——
// 那条测试只列了 7 个文件，doc/ 与 demo/ 压根不在扫描范围内。
// 按文档配置去部署的人会连上一台信令，而代码连的是另一台。
// 故扫描改成「按后缀全仓遍历」，并另加一条测试把**覆盖面本身**变成断言
// （见 TestSignalKeyScanCoversDocsAndDemos）。
package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
// 刻意用 {8,} 而非精确长度：key 轮换时只改一处，正则不跟着变。
// 下限 8 避免匹配误触（如 "pd-signal-test"），但不做上限约束。
// 刻意用 {8,} 而非精确长度：key 轮换时只改一处，正则不跟着变。
// 下限 8 避免匹配误触（如 "pd-signal-test"），但不做上限约束。
var signalKeyRe = regexp.MustCompile(`pd-signal-[0-9a-f]{8,}`)

// signalKeyScanExts 是要参与一致性检查的文本类型。
//
// 为什么要从「手写文件清单」换成「按后缀全仓遍历」：原实现只列了 7 个文件，
// 于是 key 在 Go 代码里漂了，但文档里的那 12 处（AGENTS/README/教程/demo/…）
// 一个都没被扫到——**漂移检测只覆盖了它写代码时想着的那几类文件**。
// 2026-10-06 实测：config.go 已经是 1edf5e05…，而线上部署文档（AGENTS.md）
// 还写着 b9447b40…，两者指向两台不同的信令，按文档配置会连不上。
// 列清单的写法天然漏：新增一个写死 key 的文件时没人会顺手去改这个数组。
var signalKeyScanExts = map[string]bool{
	".go":   true,
	".md":   true,
	".html": true,
	".js":   true,
	".jsx":  true,
	".ts":   true,
	".tsx":  true,
	".mjs":  true,
}

// signalKeySkipDirs 是遍历时跳过的目录（构建产物、依赖、版本控制）。
//
// ⚠️ node_modules 与 dist 必须跳过：它们体积巨大且内容由 lockfile / 构建决定，
// 扫它们只会让 CI 变慢，而产物里的 key 必然源自某个源文件——源文件已被覆盖。
var signalKeySkipDirs = map[string]bool{
	".git": true, "node_modules": true, "dist": true,
}

// signalKeyScanRoots 是遍历起点（仓库根下的一级目录 + 根级文档）。
//
// 根级文档单独列出：AGENTS.md / README.md / VERIFICATION_CHECKLIST.md
// 不在任何子目录里，按目录遍历会漏掉它们——而它们恰好是最常被直接照抄的地方。
var signalKeyScanRoots = []string{
	"back", "doc", "front", "packages", "scripts", "AGENTS.md",
	"README.md", "VERIFICATION_CHECKLIST.md",
}

// signalKeyEvidenceFiles 是「故意保留旧值」的文件：里面的字面量是**事故证据**，
// 不是会生效的配置，改掉反而抹掉了记录。
//
// back/signalserver/signalserver.go 的 HandleStatus 注释里贴着一段
// /status 响应体，里面是**上一代**的 key 字面量——那是 2026-10-06 那次
// /status 匿名泄漏的现场记录（注释明确写了「无需任何凭据」），
// 它的作用是解释「为什么 key 必须从 /status 里拿掉」以及为什么当时
// 「轮换 git 里的硬编码」这条路无效。把它改成当前 key 等于伪造证据——
// 读者会以为当时泄漏的就是今天这个值。
//
// ⚠️ 本文件自己也在扫描范围内（它在 back/ 下、后缀是 .go），所以这里
// **不能**把那个旧 key 原样写进注释，否则本测试会把自己判成漂移。
var signalKeyEvidenceFiles = map[string]bool{
	"back/signalserver/signalserver.go": true,
}

// collectSignalKeyFiles 递归收集所有待检查文件，返回仓库相对路径（/ 分隔）。
func collectSignalKeyFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	seen := map[string]bool{}
	add := func(rel string) {
		if !seen[rel] {
			seen[rel] = true
			out = append(out, rel)
		}
	}
	for _, entry := range signalKeyScanRoots {
		p := filepath.Join(root, filepath.FromSlash(entry))
		st, err := os.Stat(p)
		if err != nil {
			continue // 目录可能不存在（eg. 单模块 checkout），跳过而不是整个测试红
		}
		if !st.IsDir() {
			add(entry)
			continue
		}
		_ = filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if signalKeySkipDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			ext := strings.ToLower(filepath.Ext(d.Name()))
			if !signalKeyScanExts[ext] {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil
			}
			add(filepath.ToSlash(rel))
			return nil
		})
	}
	sort.Strings(out) // 顺序稳定，CI 输出可复现
	return out
}

// TestSignalKeyIsConsistentAcrossRepo 保证：**所有**写死信令 key 的地方用的是同一个值。
//
// 写法上刻意不硬编码期望值，而是要求「它们彼此相等」——
// 这样将来真的轮换 key，只需要改一处 + 改这一行期望，其余测试自动跟着走；
// 而漏改任何一处，这条立刻红。
func TestSignalKeyIsConsistentAcrossRepo(t *testing.T) {
	root := repoRoot(t)

	// 权威值取自 config.DefaultSignalKey 的定义处。
	canonical := canonicalSignalKey(t, root)

	// 权威定义本身也必须在扫描范围内，否则「只改 config.go」会绕过这条检查。
	files := collectSignalKeyFiles(t, root)
	if len(files) == 0 {
		t.Fatal("扫描到 0 个文件——遍历逻辑坏了，这条检查会永远假绿")
	}

	for _, rel := range files {
		rel := rel
		evidence := signalKeyEvidenceFiles[rel]
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
					// 事故证据文件里的旧值是记录，不是配置（见上面的说明）。
					if evidence {
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

// TestSignalKeyScanCoversDocsAndDemos 是**防退化**护栏：确保上面那条遍历
// 真的覆盖到了文档与 demo，而不是某次重构让遍历悄悄退化成「只扫 back/」。
//
// 发现背景：原实现是手写的 7 个文件数组，doc/ 与 demo/ 从来没进去过，
// 于是「所有客户端/文档用同一个 key」这条约定只在 Go 代码里成立。
// 加了新文件进数组也救不回来——没人会记得往数组里加。
// 这条测试把「覆盖面」本身变成断言：以后谁把遍历范围改窄，先在这里红。
func TestSignalKeyScanCoversDocsAndDemos(t *testing.T) {
	root := repoRoot(t)
	files := collectSignalKeyFiles(t, root)

	// 每类必须至少命中一个文件，否则「扩到 doc/」这件事会被一次重构悄悄回退。
	prefixes := map[string]string{
		"back/":                  "back/ 下的 Go 代码",
		"doc/":                   "doc/ 下的设计文档（线上部署 / 教程最容易抄错）",
		"packages/":              "packages/ 下的面板与 demo",
		"front/":                 "front/ 下的前端面板",
		"AGENTS.md":              "AGENTS.md 线上部署节",
		"README.md":              "README.md",
		"VERIFICATION_CHECKLIST.md": "VERIFICATION_CHECKLIST.md",
	}
	for prefix, why := range prefixes {
		hit := false
		for _, f := range files {
			if f == prefix || strings.HasPrefix(f, prefix) {
				hit = true
				break
			}
		}
		if !hit {
			t.Errorf("扫描范围没有覆盖 %s（%s）。\n"+
				"  这正是 key 漂移当初能溜过去的原因：检查只覆盖了写代码时\n"+
				"  想到的那几类文件，文档与 demo 全在扫描之外。",
				prefix, why)
		}
	}

	// 反向断言：node_modules / dist 这类目录不能被扫进来（否则 CI 慢到没法用）。
	for _, f := range files {
		if strings.Contains(f, "node_modules/") || strings.Contains(f, "/dist/") {
			t.Errorf("扫描范围混入了 %s —— 构建产物与依赖不该参与检查", f)
		}
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

// TestWorkflowStepsStillHaveRunnableBody 保证每个 GitHub Actions step 要么有 run，
// 要么有 uses —— 不能只有一个 name 和一堆注释。
//
// 2026-10-06 亲历：更新 e2e.yml 里那段 known-flaky 注释时，用 python 做
// 字符串切片替换，把紧跟其后的 `run: | …` 整块吃掉了。剩下的 step 有 name、
// 有 env、有注释，唯独没有 run —— **本地 yaml.safe_load 照样解析通过**，
// 因为 YAML 层面它仍然合法；只有 GitHub 的 schema 校验才会拒绝，
// 表现为整个 workflow 显示为 failure 且没有任何日志。
//
// 这类错误本地几乎发现不了，所以必须落成断言。
func TestWorkflowStepsStillHaveRunnableBody(t *testing.T) {
	root := repoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, ".github", "workflows"))
	if err != nil {
		t.Fatalf("读 .github/workflows: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") && !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		e := e
		t.Run(e.Name(), func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(root, ".github", "workflows", e.Name()))
			if err != nil {
				t.Fatalf("读 %s: %v", e.Name(), err)
			}
			lines := strings.Split(string(b), "\n")
			inSteps := false
			var curName string
			hasBody := false
			flush := func(stepNo int) {
				if curName == "" {
					return
				}
				if !hasBody {
					t.Errorf("%s:%d step %q 既没有 run 也没有 uses —— 只有 name 和注释。"+
						"\n  YAML 层面仍合法，所以本地解析发现不了；GitHub schema 校验会让整个 workflow 直接 failure 且无日志。",
						e.Name(), stepNo, curName)
				}
				curName, hasBody = "", false
			}
			for i, line := range lines {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "#") {
					continue
				}
				if !strings.HasPrefix(line, " ") && strings.HasSuffix(trimmed, ":") && strings.HasSuffix(trimmed, "s:") {
					inSteps = true // 顶层 xxx: 块（jobs: 之类），下一步找 steps:
					continue
				}
				if strings.HasPrefix(trimmed, "steps:") {
					inSteps = true
					continue
				}
				if !inSteps {
					continue
				}
				// 新 step 开始
				if strings.HasPrefix(trimmed, "- name:") || trimmed == "- uses:" {
					flush(i + 1)
					inSteps = true
					curName = strings.TrimSpace(strings.TrimPrefix(trimmed, "- name:"))
					hasBody = trimmed == "- uses:"
					continue
				}
				if strings.HasPrefix(trimmed, "- uses:") {
					flush(i + 1)
					curName, hasBody = "<uses>", true
					continue
				}
				// 顶格的 key（离开当前 step / block）
				if len(line) > 0 && line[0] != ' ' && strings.HasSuffix(trimmed, ":") {
					flush(i + 1)
					inSteps = strings.HasSuffix(trimmed, "steps:")
					continue
				}
				if strings.HasPrefix(trimmed, "run:") || strings.HasPrefix(trimmed, "uses:") {
					hasBody = true
				}
			}
			flush(len(lines))
		})
	}
}

// canonicalSignalKey 取权威信令 key：config.DefaultSignalKey 的定义处。
//
// 刻意不硬编码期望值，而是从定义处读——将来真轮换 key 只需改 config.go 一处。
func canonicalSignalKey(t *testing.T, root string) string {
	t.Helper()
	cfg, err := os.ReadFile(filepath.Join(root, "back", "internal", "config", "config.go"))
	if err != nil {
		t.Fatalf("读 config.go: %v", err)
	}
	if m := signalKeyRe.FindAllString(string(cfg), -1); len(m) > 0 {
		return m[0]
	}
	t.Fatal("config.go 里找不到 pd-signal-<hex>，key 约定被改掉了？")
	return ""
}

// TestSignalKeyDefaultsAreUnified —— C-8 的**默认值一致性**闸门。
//
// 发现背景：上面那条 TestSignalKeyIsConsistentAcrossRepo 只按 `pd-signal-<hex>`
// 字面量扫描，看不见「默认值是 peerjs」这类分叉——peerjs 不长成 pd-signal-<hex>
// 的样子。实测后果（C-8）：同一个二进制里 `peerdrive signal` 的 -key 默认是
// peerjs，而 `peerdrive all` 走 config.PeerJSKey（权威值），两个子命令连不上彼此，
// 默认配置的节点也连不上，而 CI 一直绿。
//
// 补的是默认值这一层：
//
//	1. 装配点的默认值必须**引用权威常量**（单一真相源），不能各自写死字面量；
//	2. 旧默认值 peerjs 不得以「信令 key 默认 / -key 示例」的形态残留在
//	   back/ 的 Go 代码与后端信令文档里。
//
// 范围：只扫 back/（.go/.md）与 doc/（.md）——后端默认值才是 C-8 的事故面。
// scripts/、packages/、front/ 里大量 `key: 'peerjs'` 是**本机演示 / 面板**连自托管
// 信令的显式值（连同对应的 -key 一起显式给），不是后端默认值，故意不扫；否则
// 这条会对着几十处与 C-8 无关的客户端演示误报。
func TestSignalKeyDefaultsAreUnified(t *testing.T) {
	root := repoRoot(t)
	canonical := canonicalSignalKey(t, root)

	// 1) 三个装配点的默认值必须引用权威常量。
	//    standalone 是独立 go.mod（github.com/Hana-ame/go-peerserver），拿不到主仓
	//    config，只能用它自己那侧、同样指向权威值的 signalserver.DefaultKey。
	for _, c := range []struct{ rel, want, why string }{
		{"back/cmd/peerdrive/subcommands.go",
			`cfg.PeerJSKey`,
			"peerdrive signal 的 -key 默认值（via config.Load → DefaultSignalKey）"},
		{"back/internal/config/config.go",
			`PeerJSKey:    getEnv("PEERDRIVE_PEERJS_KEY", DefaultSignalKey)`,
			"config.Load 的权威默认值"},
		{"back/signalserver/cmd/peersignal/main.go",
			`flag.String("key", signalserver.DefaultKey`,
			"独立 peersignal 的 -key 默认值"},
	} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.rel)))
		if err != nil {
			t.Errorf("读 %s: %v", c.rel, err)
			continue
		}
		if !strings.Contains(string(b), c.want) {
			t.Errorf("%s 没有用权威常量作默认值（找不到 %q）。\n"+
				"  这是 %s：必须引用单一真相源，不能自己写死一个值——\n"+
				"  否则 signal / all / 独立 peersignal 三处又会各自漂移（C-8）。",
				c.rel, c.want, c.why)
		}
	}

	// standalone 的 DefaultKey 必须就是权威值本身（它在独立模块里，引用不了 config）。
	// 字面量一致性另由 TestSignalKeyIsConsistentAcrossRepo 覆盖，这里只确认它存在。
	if b, err := os.ReadFile(filepath.Join(root, "back", "signalserver", "defaults.go")); err != nil {
		t.Errorf("读 back/signalserver/defaults.go: %v", err)
	} else if !strings.Contains(string(b), `DefaultKey = "`+canonical+`"`) {
		t.Errorf("back/signalserver/defaults.go 的 DefaultKey 不是权威值 %q", canonical)
	}

	// 2) 旧默认值 peerjs 的残留形态（默认表达式 + 文档示例）。
	stale := []*regexp.Regexp{
		regexp.MustCompile(`envOr\("PEERSIGNAL_KEY",\s*"peerjs"\)`), // Go：env 默认
		regexp.MustCompile(`String\("key",\s*"peerjs"`),              // Go：flag 默认
		regexp.MustCompile(`sigKey\s*:=\s*"",\s*"peerjs"`),           // Go：UnifiedMux nil-cfg 兜底
		regexp.MustCompile(`-key\s+peerjs`),                          // 文档/用法注释里的 -key 示例
		// ⚠️ 后两条的 "peer" + "js" 拼接是必须的：本文件自己也在扫描范围内，
		// 把 `peerjs` 原样写进 backtick 模式里，这两行会先把自己判成漂移。
		regexp.MustCompile("`-key`.*`peer" + "js`"),                      // 文档表格里的默认值单元格
		regexp.MustCompile("`-key`.*default.*peer" + "js"),               // 文档里「default 旧值」的英文表述
	}

	files := collectSignalKeyFiles(t, root)
	scanned := 0
	for _, rel := range files {
		// 只扫后端：back/ 的 .go 与 .md、doc/ 的 .md（见函数头「范围」）。
		ext := strings.ToLower(filepath.Ext(rel))
		if ext != ".go" && ext != ".md" {
			continue
		}
		switch {
		case strings.HasPrefix(rel, "back/"), strings.HasPrefix(rel, "doc/"):
		default:
			continue
		}
		scanned++
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("读 %s: %v", rel, err)
			continue
		}
		for i, line := range strings.Split(string(b), "\n") {
			for _, re := range stale {
				if re.MatchString(line) {
					t.Errorf("%s:%d 残留了旧的 peerjs 信令-key 默认值/示例:\n    %s\n"+
						"  默认值必须统一到 %q（C-8）。",
						rel, i+1, strings.TrimSpace(line), canonical)
					break
				}
			}
		}
	}
	if scanned == 0 {
		t.Fatal("默认值扫描到 0 个文件——范围过滤写坏了，这条会永远假绿")
	}
}

