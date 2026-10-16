package update

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"peerdrive/internal/config"
)

// TestStartupCheckOffByDefault 是默认关的核心保证：AutoUpdate=false 时
// StartupCheck 立即返回、不起 goroutine、不发任何网络请求。
func TestStartupCheckOffByDefault(t *testing.T) {
	// 监听一个端口并置为「来连接就判失败」的黑洞：如果关闭路径真的发请求，
	// 这个 Accept 会立即收到连接并 t.Error。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	hit := make(chan struct{}, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			c.Close()
			hit <- struct{}{}
		}
	}()

	t.Setenv("PEERDRIVE_AUTO_UPDATE", "false")
	t.Setenv("PEERDRIVE_UPDATE_URL", "http://"+ln.Addr().String())
	cfg := config.Load()
	if cfg.AutoUpdate {
		t.Fatal("config.Load with PEERDRIVE_AUTO_UPDATE=false → AutoUpdate=true")
	}

	StartupCheck(cfg) // 必须立即返回且不起 goroutine

	select {
	case <-hit:
		t.Fatal("StartupCheck dialed the network while AutoUpdate=false")
	case <-time.After(300 * time.Millisecond):
		// 无网络请求 —— 默认关路径干净。
	}
}

// TestStartupCheckEnabledDialOnce 打开路径：AutoUpdate=true + 间隔 0 →
// 恰好发一次检查请求（一次之后 goroutine 退出）。
func TestStartupCheckEnabledDialOnce(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(manifestFor(t, "v0.3.1")))
	}))
	defer srv.Close()

	t.Setenv("PEERDRIVE_AUTO_UPDATE", "true")
	t.Setenv("PEERDRIVE_UPDATE_URL", srv.URL)
	t.Setenv("PEERDRIVE_UPDATE_INTERVAL_HOURS", "0")
	cfg := config.Load()
	if !cfg.AutoUpdate {
		t.Fatal("config.Load with PEERDRIVE_AUTO_UPDATE=true → AutoUpdate=false")
	}

	origDelay := startupDelay
	startupDelay = 10 * time.Millisecond
	defer func() { startupDelay = origDelay }()

	StartupCheck(cfg)

	deadline := time.Now().Add(5 * time.Second)
	for hits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if hits.Load() == 0 {
		t.Fatal("StartupCheck with AutoUpdate=true never dialed the update URL")
	}
	// 间隔 0 = 只在启动时查一次；给足时间确认不会重复请求。
	time.Sleep(300 * time.Millisecond)
	if n := hits.Load(); n != 1 {
		t.Fatalf("StartupCheck made %d requests, want exactly 1 (interval 0)", n)
	}
}

// TestStartupCheckKeepsEnvironmentClean 确保测试不污染真实环境变量之外的读取。
func TestStartupCheckKeepsEnvironmentClean(t *testing.T) {
	prev, had := os.LookupEnv("PEERDRIVE_AUTO_UPDATE")
	defer func() {
		if had {
			os.Setenv("PEERDRIVE_AUTO_UPDATE", prev)
		} else {
			os.Unsetenv("PEERDRIVE_AUTO_UPDATE")
		}
	}()
	os.Unsetenv("PEERDRIVE_AUTO_UPDATE")
	if cfg := config.Load(); cfg.AutoUpdate {
		t.Fatal("unset PEERDRIVE_AUTO_UPDATE should default to false")
	}
}
