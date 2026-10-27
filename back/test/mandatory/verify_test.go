// verify_test.go
//
// 本文件刻意**不带** //go:build mandatory 标签 —— 它必须在默认的
// `go test ./...` 里就跑起来。mandatory 类别的自证能力依赖这一点：
// 即使有人把 mandatory 标签下的测试全删了、把 CI gate 全拆了，
// 只要 manifest 还在这个目录里，默认套件就会因为哈希不匹配而失败。
//
// 覆盖三种篡改：
//   1. 删除一个 mandatory 文件           → 条目找不到文件，FAIL
//   2. 改了 mandatory 文件但不更新 manifest → 哈希不匹配，FAIL
//   3. 删掉文件里的 MANDATORY 标记          → 同上（哈希变了），另外显式再断言一次
//
// 覆盖不了的情形（诚实声明，见 MANDATORY.md「本机制的边界」）：
// 同一个 PR 里同时改文件 + 改 manifest，哈希会匹配。那一层的防护是
// `.github/workflows/mandatory.yml` 的 environment required_reviewers ——
// 那条 gate 也需要人工才能放行，而这个 workflow 文件本身也在 manifest 里。

package mandatory

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// mandatoryDirective 是 mandatory 类别的识别标记。每个被列入 manifest 的测试文件
// 都必须含这一行 —— 它的作用不是防篡改（哈希已经覆盖了），而是让人工 reviewer
// 扫 diff 时一眼看出「这条测试是被保护的对象」。
const mandatoryDirective = "🚨 MANDATORY DIRECTIVE FOR ALL AI AGENTS"

// backRoot 用 runtime.Caller 从本文件位置反推 back/ 根目录，不依赖 CWD，
// 这样 `go test ./test/mandatory/` 和 `cd back && go test ./...` 两种跑法都正确。
func backRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed; cannot locate back root")
	}
	// thisFile = <back>/test/mandatory/verify_test.go
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

// TestMandatory_ManifestIntegrity 是 mandatory 类别的自校验器。
//
// 它本身不带 mandatory 标签：这是一个「看门狗」测试，默认套件每次都会跑。
// 如果它失败，说明 mandatory 类别的内容被改过而 manifest 没有同步更新 ——
// 这种情况必须由人判断是「合法的新增/修改」还是「被篡改」，所以不允许 agent 自行
// 更新 manifest 来让检查通过。
func TestMandatory_ManifestIntegrity(t *testing.T) {
	root := backRoot(t)
	manifestPath := filepath.Join(root, "test", "mandatory", "manifest.sha256")

	f, err := os.Open(manifestPath)
	if err != nil {
		t.Fatalf("manifest missing: %v\n"+
			"mandatory 类别的 manifest 是防篡改的唯一凭证，不能缺。", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var entries []struct{ hash, rel string }
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 || !strings.Contains(parts[1], "/") {
			t.Fatalf("malformed manifest line: %q", line)
		}
		entries = append(entries, struct{ hash, rel string }{parts[0], strings.TrimSpace(parts[1])})
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("reading manifest: %v", err)
	}

	if len(entries) == 0 {
		t.Fatalf("manifest is empty — mandatory 类别不能是空的，且不能通过删条目来缩小保护范围")
	}

	var failures []string
	for _, e := range entries {
		full := filepath.Join(root, filepath.FromSlash(e.rel))

		info, err := os.Stat(full)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: missing (%v)", e.rel, err))
			continue
		}
		if info.IsDir() {
			failures = append(failures, fmt.Sprintf("%s: is a directory, manifest 只登记文件", e.rel))
			continue
		}

		sum := sha256.Sum256(normalizeLineEndings(readMust(t, full)))
		if got := hex.EncodeToString(sum[:]); got != e.hash {
			note := ""
			if cr := countCR(readMust(t, full)); cr > 0 {
				note = fmt.Sprintf("\n      （文件里 %d 个 CRLF；哈希按 LF 归一化后计算，说明差异不是行尾）", cr)
			}
			failures = append(failures, fmt.Sprintf("%s: hash mismatch\n      manifest: %s\n      actual  : %s%s",
				e.rel, e.hash, got, note))
			continue
		}

		// 只要求 .go 测试文件带标记；workflow / manifest 自身不带。
		if strings.HasSuffix(e.rel, "_test.go") && !strings.Contains(
			string(normalizeLineEndings(readMust(t, full))), mandatoryDirective) {
			failures = append(failures, fmt.Sprintf("%s: mandatory 标记被移除", e.rel))
		}
	}

	if len(failures) > 0 {
		t.Fatalf("mandatory 类别的内容与 manifest 不一致（%d 项）:\n  %s\n\n"+
			"这是 mandatory 类别唯一的防篡改信号。如果这是你合法地新增/修改了测试，\n"+
			"必须由人工核验后再更新 test/mandatory/manifest.sha256 ——\n"+
			"AI agent 不得自行更新该文件来让本检查通过。",
			len(failures), strings.Join(failures, "\n  "))
	}

	t.Logf("mandatory manifest OK: %d entries verified", len(entries))
}

func readMust(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return b
}

// normalizeLineEndings 把 CRLF 归一成 LF 再哈希。
//
// 为什么必须有这一步：哈希是防篡改的唯一凭证，它的语义是「内容没变」，
// 不该被 checkout 的行尾策略绑架。Windows 贡献者用 autocrlf=true 或者
// GitHub Actions 的 Windows runner 把文本文件 checkout 成 CRLF 时，
// 内容没变但字节变了，逐字节哈希就会全线误报。2026-10-26 在
// go-build.yml 的 windows-latest 格子上第一次跑就命中了这个：
// 6 个条目全部 hash mismatch。归一化之后，LF/CRLF 都不影响判定，
// 而真正的篡改（改字符、删行、换标记）仍然会被检出。
func normalizeLineEndings(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

func countCR(b []byte) int {
	return bytes.Count(b, []byte("\r\n"))
}
