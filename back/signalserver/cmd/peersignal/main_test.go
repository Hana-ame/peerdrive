package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hana-ame/go-peersignal"
)

// 发现背景：2026-09-20 把公共面板（packages/peerdrive-client/dist/panel.html）
// 部署到 GitHub Pages。Pages 强制 HTTPS，浏览器会把 HTTPS 页面发起的 ws:// 当
// 混合内容拦掉，而 PeerJS 侧只表现为「连不上」，完全没有提示。所以自托管信令
// 必须能提供 wss:// —— 这才有的 -tls-cert/-tls-key。

// writeSelfSigned 生成一张自签证书，够测试用（生产请用 Let's Encrypt / 反代）。
func writeSelfSigned(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成私钥失败: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("签发证书失败: %v", err)
	}
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

// freeAddr 取一个当前空闲的地址（Serve 会自己在上面 listen）。
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func TestServe_TLSHalfConfig(t *testing.T) {
	// 只给一半必须报错：静默降级回 http 的话，对面 HTTPS 面板会被混合内容拦截，
	// 而服务端日志看起来一切正常，这种"半配置"最难排查。
	mux := http.NewServeMux()
	mux.HandleFunc("/peerjs/id", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("x")) })

	if err := Serve(freeAddr(t), "cert.pem", "", mux); err == nil {
		t.Fatal("只给 -tls-cert 不给 -tls-key 时应报错")
	}
	if err := Serve(freeAddr(t), "", "key.pem", mux); err == nil {
		t.Fatal("只给 -tls-key 不给 -tls-cert 时应报错")
	}
}

func TestServe_WSS(t *testing.T) {
	certPath, keyPath := writeSelfSigned(t, t.TempDir())
	srv := signalserver.NewServer("peerjs")
	mux := http.NewServeMux()
	// 公共面板连上来第一件事就是 GET /peerjs/id 取临时 id，所以拿它当探针
	mux.HandleFunc("/peerjs/id", srv.HandleID)

	addr := freeAddr(t)
	errCh := make(chan error, 1)
	go func() { errCh <- Serve(addr, certPath, keyPath, mux) }()

	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // 自签证书
		},
	}

	// Serve 是阻塞的，等它真的 listen 起来（最多 3s）
	var resp *http.Response
	var err error
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err = client.Get("https://" + addr + "/peerjs/id")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("wss 端点不可达: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200", resp.StatusCode)
	}
	// 面板在别的源（Pages）上，跨域头是硬要求——没有它浏览器一样拿不到 id
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, 期望 *（公共面板跨域取 id 依赖它）", got)
	}

	// 反向确认：同一地址用明文 http 访问拿不到 id（说明确实是 TLS 在提供服务）。
	// Go 的 TLS server 对明文请求会直接回 400（"Client sent an HTTP request to an
	// HTTPS server"），所以这里既要接受 err，也要接受 400——只有拿到 200 才算漏。
	if plain, perr := (&http.Client{Timeout: 2 * time.Second}).Get("http://" + addr + "/peerjs/id"); perr == nil {
		plain.Body.Close()
		if plain.StatusCode == http.StatusOK {
			t.Fatal("已启用 TLS 时明文 http 不应能取到 id")
		}
	}

}
