package transport

// pull_test.go：网络入库（pull）的行为契约。
//
// 这个文件存在的理由只有一条：pull 是本节点第一个「外部指定目标地址」的动词，
// 写错就不是功能坏掉，而是**把节点变成了别人手里的内网扫描器**。所以 SSRF 防护的
// 每条边界都要钉死（含 DNS 指内网、IPv4-mapped IPv6、重定向绕过）。成功路径
// 同样要覆盖——不然防护一收紧把好 URL 也拒了，没人会发现。

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// allowOnly 把 SSRF 判定临时换成「只放行 rawURL 的 host，其余仍按真判定」。
//
// 为什么不能写成 return nil：httptest 只能绑 loopback，而 gardPullURL 拒绝
// loopback 是对的。放行范围必须窄到单个 host，否则重定向用例把自己也放过去了，
// 那条用例就失去意义了。
func allowOnly(t *testing.T, rawURL string) {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	old := pullGuard
	pullGuard = func(x *url.URL) error {
		if x.Host == u.Host {
			return nil
		}
		return guardPullURL(x)
	}
	t.Cleanup(func() { pullGuard = old })
}

// TestPull_Success 正常拉取：内容落库，且回帧里的 hash 能在索引里反查到。
// 判定成功不能只看"回了 pulled"——回帧说成功但索引没落，是最容易漏的假成功。
func TestPull_Success(t *testing.T) {
	const body = "hello from remote url\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	allowOnly(t, srv.URL)
	initTestDB(t) // 内存 SQLite：WriteFile 最终要写 file_index 表
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{
		"type": "pull", "url": srv.URL + "/remote.txt", "name": "pulled.txt", "reqId": "r1",
	}))

	h, ok := waitSent(sess, "pulled", 3*time.Second)
	require.True(t, ok, "应回 pulled 帧")
	assert.Equal(t, "r1", h["reqId"])
	assert.Equal(t, int64(len(body)), int64(h["total"].(float64)))
	assert.Equal(t, "pulled.txt", h["name"])

	hash, _ := h["hash"].(string)
	require.NotEmpty(t, hash)
	fi, err := svc.fileIndex.Info(hash)
	require.NoError(t, err, "pulled 的 hash 必须能在索引里查到（内容寻址的承诺）")
	assert.Equal(t, int64(len(body)), fi.Size)
}

// TestPull_RejectsNonHTTP 非 http(s) 一律拒绝（file://、gopher://、ftp://）。
func TestPull_RejectsNonHTTP(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "gopher://example.com/1", "ftp://example.com/x"} {
		initTestDB(t) // 内存 SQLite：WriteFile 最终要写 file_index 表
		svc := newTestPeerJSService(t)
		sess := &fakeSession{id: "peer-x"}
		svc.bindConn(sess)

		svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{
			"type": "pull", "url": raw, "reqId": "r",
		}))
		h, ok := waitSent(sess, "err", 2*time.Second)
		require.True(t, ok, "%s 应被拒", raw)
		assert.Contains(t, h["msg"], "只支持 http/https", "%s 的拒绝理由要说清楚", raw)
	}
}

// TestPull_RejectsPrivateTargets 内网/本机/链路本地一律拒绝 —— SSRF 的核心面：
// 169.254.169.254 是各家云的 metadata endpoint，127.0.0.1 是本机管理口。
func TestPull_RejectsPrivateTargets(t *testing.T) {
	for _, host := range []string{
		"127.0.0.1", "127.0.0.1:8080", "[::1]", "0.0.0.0",
		"10.0.0.5", "192.168.1.1", "172.16.0.1", "172.31.255.254",
		"169.254.169.254", "localhost", "[::ffff:127.0.0.1]", "[fd00::1]",
	} {
		u, err := url.Parse("http://" + host + "/x")
		require.NoError(t, err, "%s 应能解析", host)
		err = guardPullURL(u)
		assert.Error(t, err, "%s 必须被拒绝", host)
		if err != nil {
			// 解析失败会被某些宿主/网络条件下的 DNS 异常掩盖过去，区别对待
			assert.NotContains(t, err.Error(), "域名解析失败", "%s 应被地址判定拒绝", host)
		}
	}
}

// TestPull_RejectsUserInfo user@host 是历史上有名的解析差异绕过手法，单独钉住。
func TestPull_RejectsUserInfo(t *testing.T) {
	u, err := url.Parse("http://user@127.0.0.1/x")
	require.NoError(t, err)
	assert.Error(t, guardPullURL(u))

	// 空 URL 走另一条分支（没有拿到 URL 就谈不上 SSRF）
	initTestDB(t) // 内存 SQLite：WriteFile 最终要写 file_index 表
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)
	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": "pull", "reqId": "r-empty"}))
	h, ok := waitSent(sess, "err", 2*time.Second)
	require.True(t, ok)
	assert.Equal(t, "url required", h["msg"])
}

// TestPull_RejectsRedirectToInternal 公网 URL 重定向到内网，必须在**跳之前**拦住。
// 只校验首跳的话，一个放在公网上的 302 就能穿透整面防护。
func TestPull_RejectsRedirectToInternal(t *testing.T) {
	pub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/secret", http.StatusFound)
	}))
	defer pub.Close()

	allowOnly(t, pub.URL) // 只放行首跳；重定向目标仍走真判定
	initTestDB(t)         // 内存 SQLite：WriteFile 最终要写 file_index 表
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{
		"type": "pull", "url": pub.URL + "/hop", "reqId": "r-redir",
	}))

	h, ok := waitSent(sess, "err", 3*time.Second)
	require.True(t, ok, "重定向到内网必须被拒")
	assert.Contains(t, h["msg"].(string), "重定向目标被拒")
}

// TestPull_HTTPErrorStatus 非 2xx 要回错，不能把错误页面当内容入库——
// 存进去的话 hash 会指向一张错误页，之后所有人拉到的都是垃圾。
func TestPull_HTTPErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	allowOnly(t, srv.URL)
	initTestDB(t) // 内存 SQLite：WriteFile 最终要写 file_index 表
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{
		"type": "pull", "url": srv.URL + "/missing", "reqId": "r-404",
	}))

	h, ok := waitSent(sess, "err", 3*time.Second)
	require.True(t, ok)
	assert.Contains(t, h["msg"].(string), "HTTP 404")
}

// TestPull_SizeCap 超上限必须中止，不落半个文件。
// 静默截断是最坏的结果：hash 对不上内容，而错要等到很久之后别人来拉才发现。
func TestPull_SizeCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 不打 Content-Length：逼 LimitReader 自己在流里发现超限
		for i := 0; i < 10; i++ {
			_, _ = w.Write(make([]byte, 32*1024)) // 320KB，超出 64KB 上限
		}
	}))
	defer srv.Close()

	allowOnly(t, srv.URL)
	initTestDB(t) // 内存 SQLite：WriteFile 最终要写 file_index 表
	svc := newTestPeerJSService(t)
	svc.cfg.MaxUploadBytes = 64 * 1024
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{
		"type": "pull", "url": srv.URL + "/big", "name": "big.bin", "reqId": "r-cap",
	}))

	h, ok := waitSent(sess, "err", 5*time.Second)
	require.True(t, ok, "超上限必须报错而不是截断入库")
	assert.Contains(t, h["msg"].(string), "上限")

	// 留下的半成品会让后续同名上传/Create 误判，必须已经清理掉
	files, err := svc.fileIndex.List(0, 100)
	require.NoError(t, err)
	for _, f := range files {
		assert.NotEqual(t, "big.bin", f.Name, "超限的拉取不该留下半成品文件")
	}
}

// TestPull_NameFallsBackToURLBasename 没给 name 时用 URL 路径的最后一段。
func TestPull_NameFallsBackToURLBasename(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fallback-name"))
	}))
	defer srv.Close()

	allowOnly(t, srv.URL)
	initTestDB(t) // 内存 SQLite：WriteFile 最终要写 file_index 表
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{
		"type": "pull", "url": srv.URL + "/deep/path/report.csv", "reqId": "r-name",
	}))

	h, ok := waitSent(sess, "pulled", 3*time.Second)
	require.True(t, ok)
	assert.Equal(t, "report.csv", h["name"])
}

// TestPull_GatedByPSK pull 必须过 PSK 门禁：配了密钥的节点，没出示密钥的对端
// **连 SSRF 尝试的机会都不该有**。
func TestPull_GatedByPSK(t *testing.T) {
	initTestDB(t) // 内存 SQLite：WriteFile 最终要写 file_index 表
	svc := newTestPeerJSService(t)
	svc.cfg.PeerPSK = "secret"
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{
		"type": "pull", "url": "http://169.254.169.254/latest/meta-data/", "reqId": "r-psk",
	}))

	h, ok := waitSent(sess, "err", 2*time.Second)
	require.True(t, ok)
	assert.Equal(t, pskErrCode, h["code"], "未出示密钥时应回 PSK_REQUIRED")
}
