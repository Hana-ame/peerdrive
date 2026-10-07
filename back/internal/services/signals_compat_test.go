package services

// signals_compat_test.go —— 信令路由的「对拍」测试。
//
// 这个文件名此前只出现在 services.go 的注释里（"有测试专门对拍，见
// signals_compat_test.go"），而文件**并不存在**：CI 以为三处信令路由有对拍在守，
// 实际一处都没有，于是任何一处漏登记/写错路径都不会红。
//
// 信令的 6 条共享路由登记在三处：
//
//  1. newSignalMux（services.go）—— `peerdrive signal` 单跑；
//  2. UnifiedMux（services.go）—— `peerdrive all` 合并模式；
//  3. back/signalserver/cmd/peersignal/main.go —— 独立模块。
//
// 1 与 2 已收敛到 registerSignalRoutes 同一份清单（结构上不可能分叉）；
// 3 是独立 go.mod 没法共享代码，本文件负责对拍守住它——
// services.go 直接读，main.go 跨模块只能读源码文本。

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// signalSharedRoutes 是三处装配点共同承诺登记的 6 条信令路由。
//
// 不含 "/"（面板）与 "/status/key"（运维端点）：两者只属于部分装配点，
// 见下面 TestSignalWiringParityAcrossAllThreePlaces 的说明。
var signalSharedRoutes = []string{
	"/peerjs", "/peerjs/id",
	"/discover/announce", "/discover/leave", "/discover/nodes",
	"/status",
}

// TestUnifiedMuxRegistersAllSignalRoutes `peerdrive all` 的 6 条信令路由一条都不能
// 落到主服务兜底上。
//
// 为什么单独写：已有 TestUnifiedMuxAllThreeMounted / TestUnifiedMuxNoRouteShadowing
// 只抽查了注册服务的路由 + 信令的 /status 一条——信令其余 5 条漏搬同样静默：
// 请求落到主服务兜底，不 crash、不报错，只是不对。
func TestUnifiedMuxRegistersAllSignalRoutes(t *testing.T) {
	t.Setenv("JWT_SECRET", "signals-compat-secret")
	t.Setenv("DB_PATH", t.TempDir()+"/reg.db")

	// 主服务对一切非自己路由返回 418：只要命中 418，就说明信令路由没接上。
	mainSvc := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("main-fallback"))
	})
	mux, reg, err := UnifiedMux(nil, mainSvc)
	if err != nil {
		t.Fatalf("UnifiedMux: %v", err)
	}
	defer reg.Close()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, p := range signalSharedRoutes {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTeapot {
			t.Errorf("%s 落到了主服务兜底（418）——UnifiedMux 漏登记了这条信令路由", p)
		}
		if resp.StatusCode == http.StatusNotFound {
			t.Errorf("%s 返回 404——UnifiedMux 没把它交给信令服务", p)
		}
	}
}

// TestSignalWiringParityAcrossAllThreePlaces 对拍三处装订点的信令路由，确保
// `peerdrive signal`、`peerdrive all`、独立 peersignal 登记的是同一组路径。
//
// 做法：把两处的路由**从源码里抽出来**再比，而不是只对着硬编码清单断言——
// 这样漏登记、拼写漂移两种失效都会被抓到，且改哪一处都要么同步、要么在这条红。
//
// 已知且**故意**的差异（不是漏登记）：
//   - "/"：面板。单跑时挂 "/"、合并模式挂 "/_signal"（主服务要占 "/"）。
//   - "/status/key"：独立 peersignal 的运维 key 端点。主仓二进制的 /status 已由
//     ops token 网关，而它根本没有配置 ops token 的入口，挂上也只会是永远 401 的
//     死端点——所以只在独立进程里登记。
func TestSignalWiringParityAcrossAllThreePlaces(t *testing.T) {
	root := repoRootFromServices(t)

	servicesRoutes := extractMuxRoutes(t, filepath.Join("services.go"))
	standaloneRoutes := extractMuxRoutes(t,
		filepath.Join(root, "back", "signalserver", "cmd", "peersignal", "main.go"))

	// services.go：registerSignalRoutes 登记 6 条共享路由；另外两处面板挂法
	//（单跑 "/"、合并 "/_signal"）不属于共享承诺，先剔掉。
	servicesShared := without(servicesRoutes, "/", "/_signal")
	if got, want := sorted(servicesShared), sorted(signalSharedRoutes); !equalStrings(got, want) {
		t.Errorf("services.go 登记的信令路由 = %v，期望 %v。\n"+
			"  这 6 条是 peerdrive signal / all 的共同承诺；\n"+
			"  改这里必须同时改独立 peersignal（back/signalserver/cmd/peersignal/main.go）。",
			got, want)
	}

	// main.go：6 条共享路由 + 面板 "/" + 运维端点 "/status/key"。
	standaloneShared := without(standaloneRoutes, "/", "/status/key")
	if got, want := sorted(standaloneShared), sorted(signalSharedRoutes); !equalStrings(got, want) {
		t.Errorf("独立 peersignal 登记的信令路由 = %v，期望 %v。\n"+
			"  它必须与 peerdrive signal/all 逐条一致（另加面板 \"/\" 与运维 \"/status/key\"）。",
			got, want)
	}
	if !contains(standaloneRoutes, "/status/key") {
		t.Errorf("独立 peersignal 少了运维端点 /status/key（它没有对应的对端，是预期存在的）")
	}
}

// extractMuxRoutes 从 Go 源码里抽出 mux.Handle / mux.HandleFunc 登记的路径。
//
// 只认字面量路径（"/foo"），动态拼出来的模式抽不到——本包与 main.go 都只用字面量。
// 抽不到任何一条就 Fatal：否则一个正则写错会让上面两条对拍永远假绿。
func extractMuxRoutes(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s: %v", path, err)
	}
	re := regexp.MustCompile(`mux\.Handle(?:Func)?\("(/[^"]*)"`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatalf("%s 里抽不到任何 mux.HandleFunc(\"/…\")，正则或源码结构变了", path)
	}
	return out
}

// repoRootFromServices 从本包目录向上找到仓库根（含 back/signalserver/cmd/peersignal/main.go）。
func repoRootFromServices(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "back", "signalserver", "cmd", "peersignal", "main.go")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("找不到仓库根（向上找不到 back/signalserver/cmd/peersignal/main.go）")
	return ""
}

func without(in []string, drop ...string) []string {
	skip := map[string]bool{}
	for _, d := range drop {
		skip[d] = true
	}
	var out []string
	for _, s := range in {
		if !skip[s] {
			out = append(out, s)
		}
	}
	return out
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	return strings.Join(a, "\x00") == strings.Join(b, "\x00")
}

func contains(in []string, want string) bool {
	for _, s := range in {
		if s == want {
			return true
		}
	}
	return false
}
