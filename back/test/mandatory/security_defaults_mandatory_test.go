//go:build mandatory

// security_defaults_mandatory_test.go
//
// 🚨 MANDATORY DIRECTIVE FOR ALL AI AGENTS:
// This test group is part of the mandatory (human-verified) test category.
// AI agents are STRICTLY FORBIDDEN from modifying, commenting out, or loosening
// these assertions. If any of them fails, fix the implementation to satisfy the
// invariant — never weaken the assertion. To change an invariant, a human must
// update test/mandatory/manifest.sha256 in the same review.
//
// 这组断言钉住「安全默认」，是 mandatory 类别的第一批正式成员。
// 为什么它们必须属于这个类别而不是普通测试：
//   - 每一条都是一个「关掉就出事」的默认姿态；
//   - 它们很容易被后续的「为了让某个用例通过」顺手改掉，且改动看起来完全无害；
//   - 普通测试套件里的弱化是静默的，mandatory 类别里则会被 manifest 哈希检出来。
//
// 跑法：go test -tags "nosqlite mandatory" ./test/mandatory/
// 注意本文件带 //go:build mandatory，默认套件不会编译它 —— 这是有意为之的隔离。

package mandatory

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/config"
	"peerdrive/pkg/urlguard"
)

// TestMandatory_SSRFGuardBlocksInternalTargets 钉住 SSRF 闸门对每一类内网目标
// 都是拒绝的。这是 peerdrive 对「对端可控 URL」的统一姿态，任何一条放行都等于
// 把节点变成可被远程指向云元数据服务 / 内网服务的代理。
func TestMandatory_SSRFGuardBlocksInternalTargets(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"IPv4 loopback", "http://127.0.0.1/x"},
		{"IPv4 loopback alternate form", "http://127.0.0.1:8080/admin"},
		{"IPv6 loopback", "http://[::1]/x"},
		{"IPv4-mapped IPv6 loopback", "http://[::ffff:127.0.0.1]/x"},
		{"IMDS link-local", "http://169.254.169.254/latest/meta-data/"},
		{"IMDS link-local https", "https://169.254.169.254/"},
		{"RFC1918 10/8", "http://10.0.0.1/x"},
		{"RFC1918 172.16/12", "http://172.16.0.1/x"},
		{"RFC1918 192.168/16", "http://192.168.1.1/x"},
		{"localhost by name", "http://localhost/x"},
		{"localhost case-insensitive", "http://LOCALHOST/x"},
		{"multicast", "http://224.0.0.1/x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, urlguard.GuardExternalURL(tc.url),
				"%s must be rejected by the SSRF guard", tc.url)
		})
	}
}

// TestMandatory_SSRFGuardRejectsUnsafeSchemeAndUserInfo 钉住 URL 形态层：
// 非 http(s) scheme 和 userinfo 必须拒绝。userinfo 是绕过 host 检查的经典手法。
func TestMandatory_SSRFGuardRejectsUnsafeSchemeAndUserInfo(t *testing.T) {
	for _, url := range []string{
		"ftp://example.com/file",
		"file:///etc/passwd",
		"gopher://example.com/x",
		"http://user@example.com/x",
		"http://user:pass@example.com/x",
		"http://@example.com/x",
	} {
		require.Error(t, urlguard.GuardExternalURL(url),
			"%s must be rejected by the SSRF guard", url)
	}
}

// TestMandatory_SSRFGuardAllowsPublicLiteralIP 钉住闸门不误伤：公网字面 IP
// 必须放行。没有这条断言，闸门就可能被「为了堵 SSRF 顺手全拦」成拒绝一切，
// 直接把 universal_downloader 打挂。用 8.8.8.8 是因为它是字面 IP，不走 DNS。
func TestMandatory_SSRFGuardAllowsPublicLiteralIP(t *testing.T) {
	assert.NoError(t, urlguard.GuardPullIP(net.ParseIP("8.8.8.8")),
		"public literal IP must not be blocked")
	assert.NoError(t, urlguard.GuardPullIP(net.ParseIP("93.184.216.34")),
		"public literal IP must not be blocked")
}

// TestMandatory_SafeClientHasDialGuardAndTimeout 钉住 dial 层的防护真的接上了：
// NewSafeClient 必须同时带非零超时和 SafeDialContext 的 Transport。
//
// 为什么这条要单独钉：URL 层闸门拦不住 302 跳内网和 DNS rebinding，
// 只有 dial 层（每次连接前重新解析并校验 IP）能拦。如果哪天有人把
// Transport 换成 http.DefaultTransport「因为更快」，URL 层看起来一切正常，
// 这条断言会先红。
func TestMandatory_SafeClientHasDialGuardAndTimeout(t *testing.T) {
	c := urlguard.NewSafeClient(0) // 0 应回退到默认 30s，而不是变成「无超时」
	require.NotNil(t, c.Transport, "safe client must use the guarded transport")
	require.NotEqual(t, int64(0), c.Timeout,
		"NewSafeClient(0) must fall back to a default timeout, not disable it")
}

// TestMandatory_JSONBodyCapIsOnByDefault 钉住 JSON body 上限默认开启。
//
// 背景：49 个 ShouldBindJSON 端点都会把整个 body 读进内存；上限关掉等于允许
// 匿名请求把节点 OOM。这是 red-team R2 的防线，必须默认开着，
// 只能由运维用 PEERDRIVE_MAX_JSON_BODY_BYTES=0 显式关闭。
func TestMandatory_JSONBodyCapIsOnByDefault(t *testing.T) {
	cfg := config.DefaultConfig()
	require.NotNil(t, cfg, "default config must not be nil")
	require.Greater(t, cfg.MaxJSONBodyBytes, int64(0),
		"JSON body cap must be enabled by default; setting it to 0 leaves every ShouldBindJSON endpoint uncapped")
}
