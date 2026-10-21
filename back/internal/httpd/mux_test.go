package httpd

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These two tests moved here with the code they exercise (they used to sit in
// internal/services and call the unexported stripPrefix/withPrefix).

// TestWithPrefixKeepsMethod WithPrefix 不能靠切片拼路径——
// 方法名长度一变（POST/DELETE 比 GET 长）就拼错，且会悄悄丢掉方法词。
func TestWithPrefixKeepsMethod(t *testing.T) {
	cases := []struct{ in, want string }{
		{"GET /ping", "GET /_reg/ping"},
		{"POST /auth/register", "POST /_reg/auth/register"},
		{"DELETE /a-very-long-path", "DELETE /_reg/a-very-long-path"},
		{"/no-method", "/_reg/no-method"},
	}
	for _, c := range cases {
		t.Run("WithPrefix("+c.in+")", func(t *testing.T) {
			assert.Equal(t, c.want, WithPrefix("/_reg", c.in))
		})
	}
}

// TestStripPrefixRewritesPath 内层 handler 必须看到剥掉前缀后的路径，
// 否则注册服务自己的 ServeMux（写死 "/ping"）会匹配不到。
func TestStripPrefixRewritesPath(t *testing.T) {
	var seen string
	h := StripPrefix("/_reg", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/_reg/ping", nil))
	require.Equal(t, "/ping", seen)

	// 恰好等于前缀时应还原成根路径：signalserver 的 HandleDashboard 硬判
	// Path != "/" 就 404，空串会被它误判。
	var seen2 string
	h2 := StripPrefix("/_signal", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen2 = r.URL.Path
	}))
	h2.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/_signal", nil))
	require.Equal(t, "/", seen2, "an empty path would break HandleDashboard")
}

// TestStripPrefixLeavesForeignPathAlone 没带前缀的路径不能被改写——那是
// 「挂了前缀」而不是「删了一段路径」。
func TestStripPrefixLeavesForeignPathAlone(t *testing.T) {
	var seen string
	h := StripPrefix("/_reg", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/other/path", nil))
	assert.Equal(t, "/other/path", seen)
}

// TestStripPrefixHandlesEscapedPath 带转义分段的输入也要被剥掉前缀。
//
// 注意继承下来的语义：原实现把 RawPath 改写成「解码后的」stripped，因此
// RawPath 与 Path 相等，EscapedPath() 会丢掉 %2F 的转义。这是既有行为，
// 本包只做搬迁不改行为，所以这里只钉 Path。
func TestStripPrefixHandlesEscapedPath(t *testing.T) {
	var seen string
	h := StripPrefix("/_reg", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/_reg/a%2Fb", nil))
	assert.Equal(t, "/a/b", seen)
}

// TestStripPrefixDoesNotMutateIncomingRequest 改写必须发生在 request 的副本上。
// 原地改 r.URL.Path 会让上游（日志中间件、调用方自己的 defer）看到被改过的路径，
// 那正是「剥前缀」不该有的副作用。
func TestStripPrefixDoesNotMutateIncomingRequest(t *testing.T) {
	var seen string
	h := StripPrefix("/_reg", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
	}))
	req := httptest.NewRequest(http.MethodGet, "/_reg/ping", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	assert.Equal(t, "/ping", seen, "inner handler must see the stripped path")
	assert.Equal(t, "/_reg/ping", req.URL.Path, "the original request must not be mutated")
}

// TestStripPrefixAndWithPrefixRoundTrip 两个原语合起来必须把一套路由原样搬过去：
// 用 WithPrefix 改模式，用 StripPrefix 还原路径，最终内层看到的还是老路径。
func TestStripPrefixAndWithPrefixRoundTrip(t *testing.T) {
	var seen string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = r.URL.Path })

	mux := http.NewServeMux()
	mux.Handle(WithPrefix("/_signal", "GET /dashboard"), StripPrefix("/_signal", inner))
	req := httptest.NewRequest(http.MethodGet, "/_signal/dashboard", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "/dashboard", seen)
}
