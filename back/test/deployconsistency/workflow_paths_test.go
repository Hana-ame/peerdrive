// workflow_paths_test.go — workflow `paths:` 过滤器指向的文件必须真实存在。
//
// 2026-10-06 的现场观察：.github/workflows/e2e.yml 的触发条件是
//
//	on:
//	  push:
//	    paths:
//	      - 'back/**'
//	      - 'packages/peerdrive-client/**'
//	      - 'scripts/netdisk-local-demo.sh'
//	      - '.github/workflows/e2e.yml'
//
// 前面三条是通配符，末两条是**具体文件**。如果 `scripts/netdisk-local-demo.sh`
// 被重命名或删除，这条过滤器就匹配不到任何东西——而 GitHub 不会报错，
// **整个 E2E workflow 从此静默不再触发**，代码照样能合。
//
// 这与 deploy_consistency_test.go 里 TestWorkflowStepsStillHaveRunnableBody
// 是同一族：YAML 层面完全合法，本地解析发现不了，只有实际触发行为会暴露，
// 而那时已经晚了。
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isGlobPath 判断一个 paths 条目是否是通配符模式。
func isGlobPath(p string) bool {
	return strings.ContainsAny(p, "*?[")
}

// parseWorkflowPaths 提取一个 workflow 文件里所有 `paths:` 条目。
//
// ⚠️ 不用 YAML 库解析，手写扫描：仓库没有 YAML 依赖，
// 而这个结构（`paths:` 下若干 `- 'xxx'`）足够规整，逐行扫更可靠也更轻。
func parseWorkflowPaths(content string) []string {
	lines := strings.Split(content, "\n")
	var out []string
	inPaths := false
	pathsIndent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		trim := strings.TrimSpace(line)

		if !strings.HasSuffix(trim, "paths:") && !strings.HasPrefix(trim, "-") {
			// 顶层或浅层的 key，可能离开 paths 块
			if inPaths && indent <= pathsIndent {
				inPaths = false
			}
		}
		if strings.HasSuffix(trim, "paths:") {
			inPaths = true
			pathsIndent = indent
			continue
		}
		if !inPaths {
			continue
		}
		// paths 块内的条目：缩进必须严格大于 paths: 的缩进
		if indent <= pathsIndent {
			inPaths = false
			continue
		}
		if strings.HasPrefix(trim, "- ") || trim == "-" {
			entry := strings.TrimSpace(strings.TrimPrefix(trim, "-"))
			entry = strings.Trim(entry, `'"`)
			if entry != "" {
				out = append(out, entry)
			}
			continue
		}
	}
	return out
}

func TestWorkflowPathFiltersPointAtExistingFiles(t *testing.T) {
	root := repoRoot(t)
	wd, err := os.ReadDir(filepath.Join(root, ".github", "workflows"))
	if err != nil {
		t.Fatalf("读 .github/workflows: %v", err)
	}
	if len(wd) == 0 {
		t.Fatal(".github/workflows 为空，可能路径解析错了")
	}
	for _, e := range wd {
		e := e
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".yml") && !strings.HasSuffix(e.Name(), ".yaml")) {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(root, ".github", "workflows", e.Name()))
			if err != nil {
				t.Fatalf("读 %s: %v", e.Name(), err)
			}
			paths := parseWorkflowPaths(string(b))
			if len(paths) == 0 {
				t.Skip("没有 paths 过滤器，不适用")
			}
			for _, p := range paths {
				if isGlobPath(p) {
					continue // 通配符无法在提交时验证，靠 CI 实际触发兜底
				}
				if p == "." || p == "/" {
					continue
				}
				full := filepath.Join(root, filepath.FromSlash(p))
				if _, err := os.Stat(full); err != nil {
					t.Errorf("%s 引用了不存在的路径 %q。\n"+
						"  GitHub 不会报错，但这个过滤器从此匹配不到任何东西——\n"+
						"  改动该文件时整个 workflow **静默不再触发**。\n"+
						"  修法：把路径改成真实存在的，或删掉这条。",
						e.Name(), p)
				}
			}
		})
	}
}

// TestWorkflowRunsItsOwnCoveredScripts 保证：
// workflow 里实际执行的脚本，必须落在它自己 paths 过滤器的覆盖范围内。
//
// 否则编辑那个脚本不会触发这个 workflow，等于该脚本没有 CI。
//
// 现场依据：e2e.yml 跑 verify-panel.mjs，而它只覆盖
// `packages/peerdrive-client/**`；如果脚本挪出这个目录而 paths 没跟着改，
// 编辑它就永远不触发 E2E。
func TestWorkflowRunsItsOwnCoveredScripts(t *testing.T) {
	root := repoRoot(t)
	wd, err := os.ReadDir(filepath.Join(root, ".github", "workflows"))
	if err != nil {
		t.Fatalf("读 .github/workflows: %v", err)
	}
	for _, e := range wd {
		e := e
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".yml") && !strings.HasSuffix(e.Name(), ".yaml")) {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(root, ".github", "workflows", e.Name()))
			if err != nil {
				t.Fatalf("读 %s: %v", e.Name(), err)
			}
			content := string(b)
			paths := parseWorkflowPaths(content)

			// 找出 workflow 里实际引用的 .mjs / .sh 脚本路径
			var refs []string
			for _, line := range strings.Split(content, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "#") {
					continue
				}
				for _, tok := range strings.Fields(line) {
					tok = strings.Trim(tok, `"`)
					if (strings.HasSuffix(tok, ".mjs") || strings.HasSuffix(tok, ".sh")) &&
						(strings.Contains(tok, "/") || strings.HasPrefix(tok, "scripts/")) {
						refs = append(refs, tok)
					}
				}
			}
			if len(refs) == 0 {
				t.Skip("没引用脚本")
			}
			// ⚠️ 没有 paths 过滤器 = 每次 push 都触发 = 任何路径都算被覆盖。
			// 实测：ci.yml 就没有 paths，它跑 scripts/check-doc-refs.mjs，
			// 那个脚本不在任何 paths 里，但 ci.yml 本来就每次都会跑——不叫缺口。
			if len(paths) == 0 {
				t.Skip("没有 paths 过滤器，每次 push 都触发，不适用")
			}
			for _, ref := range refs {
				// 去掉可能的 $GITHUB_WORKSPACE/ 前缀与行尾标点
				ref = strings.TrimSuffix(ref, "|")
				ref = strings.TrimSuffix(ref, ";")
				ref = strings.Trim(ref, "`")
				ref = strings.TrimPrefix(ref, "$GITHUB_WORKSPACE/")
				ref = strings.TrimPrefix(ref, "./")
				if ref == "" || isGlobPath(ref) {
					continue
				}
				// 该 workflow 必须有一条 paths 规则覆盖它
				covered := false
				for _, p := range paths {
					if p == "." || !isGlobPath(p) && p == ref {
						covered = true
						break
					}
					if !isGlobPath(p) {
						continue
					}
					// 简单前缀匹配：`back/**` 覆盖 `back/foo.go`
					dir := strings.TrimSuffix(p, "**")
					if strings.HasPrefix(ref, dir) {
						covered = true
						break
					}
				}
				if !covered {
					t.Errorf("%s 执行了 %s，但它的 paths 过滤器没有覆盖这个路径：\n"+
						"  编辑该脚本不会触发 %s，等于这条链路没有 CI。\n"+
						"  修法：把它加进 paths，或移到已被覆盖的目录下。",
						e.Name(), ref, e.Name())
				}
			}
		})
	}
}
