package httpd

// Server lifecycle tests: construction validation, plain and TLS serving, port
// conflicts, signal-driven and context-driven graceful shutdown, and the
// "force close after the drain budget expires" boundary.
//
// Why these exist: the four pre-split listen implementations each handled these
// cases differently (one had no read-header timeout, one had no signal
// handling, one loaded TLS with a different call), so "the port comes up" was
// never pinned down anywhere. These tests pin the shared behaviour.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// echoHandler returns a handler that answers every request with 200 "ok".
func echoHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
}

// --- construction validation ---

func TestNewRequiresHandler(t *testing.T) {
	_, err := New(Config{Addr: "127.0.0.1:0"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "handler")
}

// TestNewRequiresAddr 空监听地址必须在构造期被拒。
//
// 发现背景：`net.Listen("tcp", "")` 不会报错，而是绑定到一个随机端口。
// 误配的结果是进程正常启动、日志显示在跑，实际没有任何人连得上——
// 这种故障比启动失败难查得多。
func TestNewRequiresAddr(t *testing.T) {
	for _, addr := range []string{"", " ", "\t"} {
		_, err := New(Config{Addr: addr}, echoHandler())
		require.Error(t, err, "Addr=%q", addr)
		assert.Contains(t, err.Error(), "Addr")
	}
}

// TestNewRejectsHalfTLSPair 只给一半 TLS 参数必须报错，不能静默降级成明文。
//
// 必须带超时守卫：校验被删掉后，Serve 会一路走到 net.Listen 真的去监听端口，
// 测试会挂住而不是失败。挂住的测试没有诊断信息，只会把 CI 占满。
func TestNewRejectsHalfTLSPair(t *testing.T) {
	cases := []Config{
		{Addr: "127.0.0.1:0", CertFile: "/tmp/nope.pem"},
		{Addr: "127.0.0.1:0", KeyFile: "/tmp/nope.pem"},
	}
	for _, cfg := range cases {
		done := make(chan error, 1)
		go func() {
			s, err := New(cfg, echoHandler())
			if err != nil {
				done <- err
				return
			}
			done <- s.Serve(context.Background())
		}()
		select {
		case err := <-done:
			require.Error(t, err)
			assert.Contains(t, err.Error(), "both")
		case <-time.After(5 * time.Second):
			t.Fatal("half TLS pair did not return: the both-required check is gone and the " +
				"server fell through to ListenAndServe, blocking on a real port")
		}
	}
}

// TestNewRejectsUnreadableTLSKeyPair 证书路径拼错必须立刻失败。
//
// 发现背景：若把密钥加载推迟到握手时才做，服务会先以明文起一段，
// 直到第一个 TLS 客户端连进来才炸——那时「以为在跑 HTTPS」的窗口已经存在。
func TestNewRejectsUnreadableTLSKeyPair(t *testing.T) {
	_, err := New(Config{
		Addr: "127.0.0.1:0", CertFile: "/tmp/no-such-cert.pem", KeyFile: "/tmp/no-such-key.pem",
	}, echoHandler())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "load TLS keypair")
}

// TestNewRejectsGarbageTLSKeyPair 文件存在但内容不是 TLS 密钥对，必须在构造期
// 失败——否则要等第一次握手才炸，那时进程已经在跑了。
func TestNewRejectsGarbageTLSKeyPair(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "cert.pem")
	key := filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(cert, []byte("not a certificate"), 0o600))
	require.NoError(t, os.WriteFile(key, []byte("not a key"), 0o600))

	_, err := New(Config{Addr: "127.0.0.1:0", CertFile: cert, KeyFile: key}, echoHandler())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "load TLS keypair")
}

// TestConfigDefaults 零值必须归一化成安全默认，不能产生零排空预算。
//
// 发现背景：ShutdownTimeout=0 会被 http.Server 当成「立刻放弃」，
// 进行中的大文件上传会被截成半个文件（对端拿到损坏残片却以为成功）；
// LogPrefix 的默认值是 "httpd"，serverapp 显式改成 "main" 以对齐旧日志。
func TestConfigDefaults(t *testing.T) {
	cfg := Config{Addr: "127.0.0.1:0", ShutdownTimeout: 0}
	s, err := New(cfg, echoHandler())
	require.NoError(t, err)
	// A zero ShutdownTimeout must not produce a zero drain budget.
	assert.Equal(t, DefaultShutdownTimeout, s.cfg.ShutdownTimeout)
	assert.Equal(t, "127.0.0.1:0", s.Addr())
	assert.False(t, s.TLS())
	assert.Equal(t, "httpd", s.prefix)
}

// TestReadHeaderTimeoutPassesThrough 超时值必须逐字保留：主服务一直用 15s，
// 信令/注册服务一直是 0（不设读头超时）。分解不得顺手把超时统一掉。
func TestReadHeaderTimeoutPassesThrough(t *testing.T) {
	cases := []struct {
		name string
		give time.Duration
		want time.Duration
	}{
		{"main surface uses the 15s default", DefaultReadHeaderTimeout, DefaultReadHeaderTimeout},
		{"signal surface keeps zero", 0, 0},
		{"explicit value wins", 3 * time.Second, 3 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(Config{Addr: "127.0.0.1:0", ReadHeaderTimeout: tc.give}, echoHandler())
			require.NoError(t, err)
			assert.Equal(t, tc.want, s.httpSrv.ReadHeaderTimeout)
		})
	}
}

// --- plain HTTP serving + context cancellation ---

func TestServePlainHTTPThenCancel(t *testing.T) {
	srv, err := New(Config{Addr: "127.0.0.1:0"}, echoHandler())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()

	addr := waitAddr(t, srv)
	assertResponse(t, addr, "http", "ok")

	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err, "a context cancel is a clean shutdown, not an error")
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after context cancellation")
	}
	// The listener must be closed afterwards.
	assert.Error(t, dial(addr), "listener should be closed after shutdown")
}

// --- port conflict ---

// TestServeReturnsPortConflict 端口被占用时必须返回错误，不能挂住。
//
// 这是运维最常遇到的失败模式（重启脚本没杀掉旧进程），所以它必须是一条清晰
// 的错误，而不是一个占满 CI 的挂起。
func TestServeReturnsPortConflict(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer held.Close()

	srv, err := New(Config{Addr: held.Addr().String()}, echoHandler())
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(context.Background()) }()
	select {
	case err := <-errCh:
		// 比 errno 而不是错误文本：同一个 EADDRINUSE，Linux 的措辞是
		// "address already in use"，Windows 是 "Only one usage of each socket
		// address (protocol/network address/port) is normally permitted"。
		// 比对字符串会让这条断言只在其中一个平台上成立。errors.Is 走的是
		// net.OpError → *os.SyscallError → syscall.Errno 这条链，跨平台一致。
		require.Error(t, err, "binding an occupied port must fail")
		require.Truef(t, errors.Is(err, syscall.EADDRINUSE),
			"binding an occupied port must fail with EADDRINUSE, got %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return on a port conflict")
	}
}

// --- multi-listen: two surfaces at once ---

func TestTwoServersListenIndependently(t *testing.T) {
	mk := func(tag string) *Server {
		s, err := New(Config{Addr: "127.0.0.1:0"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, tag)
		}))
		require.NoError(t, err)
		return s
	}
	a, b := mk("alpha"), mk("bravo")

	done := make(chan error, 2)
	go func() { done <- a.Serve(context.Background()) }()
	go func() { done <- b.Serve(context.Background()) }()

	aa, ab := waitAddr(t, a), waitAddr(t, b)
	require.NotEqual(t, aa, ab, "two ':0' servers must bind distinct ports")
	assertResponse(t, aa, "http", "alpha")
	assertResponse(t, ab, "http", "bravo")

	_ = a.Shutdown()
	_ = b.Shutdown()
	assert.Error(t, dial(aa))
	assert.Error(t, dial(ab))
}

// --- TLS with a self-signed cert ---

func TestServeTLSWithSelfSignedCert(t *testing.T) {
	certPath, keyPath := selfSignedPair(t)
	srv, err := New(Config{Addr: "127.0.0.1:0", CertFile: certPath, KeyFile: keyPath}, echoHandler())
	require.NoError(t, err)
	assert.True(t, srv.TLS())

	done := make(chan error, 1)
	go func() { done <- srv.Serve(context.Background()) }()
	addr := waitAddr(t, srv)

	// Plaintext HTTP must NOT be served as if it were plain HTTP. Go's TLS
	// server answers it with 400 "Client sent an HTTP request to an HTTPS
	// server" (Go >= 1.24); older versions just drop the connection. Either
	// way it is not a 200. Without tls.NewListener the answer would be
	// 200 "ok" in plaintext — a deployment believing it serves HTTPS.
	status, err := plainHTTPStatus(addr)
	if err == nil {
		assert.NotEqual(t, http.StatusOK, status, "a TLS listener served plaintext HTTP")
		assert.Equal(t, http.StatusBadRequest, status)
	}

	cl := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		Timeout:   5 * time.Second,
	}
	resp, err := cl.Get("https://" + addr + "/")
	require.NoError(t, err)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, "ok", string(b))

	_ = srv.Shutdown()
	assert.Error(t, dial(addr))
}

// TestServeTLSHandshakePresentsLeafCert 自签证书要真的在握手里出现——只
// LoadX509KeyPair 成功、TLSConfig 没装到 http.Server 上的话，握手会报
// unknown authority 或直接回 HTTP。
func TestServeTLSHandshakePresentsLeafCert(t *testing.T) {
	certPath, keyPath := selfSignedPair(t)
	srv, err := New(Config{Addr: "127.0.0.1:0", CertFile: certPath, KeyFile: keyPath}, echoHandler())
	require.NoError(t, err)

	go func() { _ = srv.Serve(context.Background()) }()
	defer func() { _ = srv.Shutdown() }()
	addr := waitAddr(t, srv)

	conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
	require.NoError(t, err)
	defer conn.Close()
	pairs := conn.ConnectionState().PeerCertificates
	require.Len(t, pairs, 1)
	assert.Contains(t, pairs[0].Subject.String(), "httpd-test")
}

// --- graceful drain vs forced close ---

func TestShutdownDrainsInFlightRequest(t *testing.T) {
	release := make(chan struct{})
	srv, err := New(Config{Addr: "127.0.0.1:0"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		_, _ = io.WriteString(w, "slow")
	}))
	require.NoError(t, err)

	go func() { _ = srv.Serve(context.Background()) }()
	addr := waitAddr(t, srv)

	// A request that holds a connection open: the drain must wait for it.
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		cl := &http.Client{Timeout: 0}
		resp, err := cl.Get("http://" + addr + "/")
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	time.Sleep(150 * time.Millisecond)

	closed := make(chan error, 1)
	go func() { closed <- srv.Shutdown() }()

	select {
	case <-finished:
		t.Fatal("Shutdown closed the request before it was released — the drain is not draining")
	case <-time.After(150 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-closed:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Shutdown did not return after the in-flight request finished")
	}
}

// TestShutdownForceClosesAfterBudget 排空超时后必须强制关闭，不能无限等。
//
// 这是「优雅关闭」的另一半语义：优雅是有预算的。一个卡死的 handler 不该让
// 进程永远退不掉——那正是 CI 挂住的经典原因。
func TestShutdownForceClosesAfterBudget(t *testing.T) {
	release := make(chan struct{})
	srv, err := New(Config{Addr: "127.0.0.1:0", ShutdownTimeout: 200 * time.Millisecond},
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-release
			_, _ = io.WriteString(w, "slow")
		}))
	require.NoError(t, err)

	go func() { _ = srv.Serve(context.Background()) }()
	addr := waitAddr(t, srv)

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		cl := &http.Client{Timeout: 0}
		resp, err := cl.Get("http://" + addr + "/")
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	time.Sleep(150 * time.Millisecond)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Shutdown() }()

	select {
	case err := <-errCh:
		assert.NoError(t, err, "forced close succeeds; the drain timeout is not an error")
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not force close")
	}

	close(release)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Log("in-flight request did not finish after force close (expected: it is dropped)")
	}
	assert.Error(t, dial(addr), "listener must be closed after a forced shutdown")
}

// --- helpers ---

// waitAddr polls until the server has bound, returning the bound address.
func waitAddr(t *testing.T, s *Server) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if a := s.ListenerAddr(); a != nil {
			return a.String()
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server did not bind within 5s (Addr=%q)", s.Addr())
	return ""
}

func dial(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

func assertResponse(t *testing.T, addr, scheme, want string) {
	t.Helper()
	cl := &http.Client{Timeout: 5 * time.Second}
	resp, err := cl.Get(scheme + "://" + addr + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, want, string(b))
}

// plainHTTPStatus speaks one GET in plaintext and returns the status it got.
// An error means the server refused to answer at all (handshake failure). Used
// to prove a TLS listener is really TLS rather than plain HTTP with a cert.
func plainHTTPStatus(addr string) (int, error) {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"); err != nil {
		return 0, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// selfSignedPair writes a self-signed cert+key pair valid for 127.0.0.1 and
// returns the two paths.
func selfSignedPair(t *testing.T) (string, string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "httpd-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: der,
	}), 0o600))
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}), 0o600))
	return certPath, keyPath
}
