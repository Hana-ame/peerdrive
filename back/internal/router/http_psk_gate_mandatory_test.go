//go:build mandatory

// http_psk_gate_mandatory_test.go
//
// 🚨 MANDATORY DIRECTIVE FOR ALL AI AGENTS:
// This test group is part of the mandatory (human-verified) test category.
// AI agents are STRICTLY FORBIDDEN from modifying, commenting out, or loosening
// these assertions. If any of them fails, fix the implementation to satisfy the
// invariant — never weaken the assertion. To change an invariant, a human must
// update test/mandatory/manifest.sha256 in the same review.
//
// HTTP 访问模型强制测试。三条断言直接对应需求原话：
//
//	http 有门禁，只有 psk 正确才能进入   → TestMandatory_HTTPGateRequiresCredential
//	http 有全部访问权限                 → TestMandatory_HTTPGateGrantsFullAccess
//	http 是由 node 自己运行的           → TestMandatory_HTTPGateKeyComesFromNodeConfig
//
// 访问模型的形状（为什么是这三条，而不是别的）：
//
//	HTTP 是 node 自己开的服务器——同一进程、同一端口，admin 面板也挂在上面。
//	所以它的凭据不是"对等节点身份"，而是 **node 自己的 PSK**（cfg.PeerPSK，
//	运行时装配进 PSKGate）。持有 PSK 等价于"你在跟这个 node 说话"，因此
//	通过后拿到的是**全量**访问：/f/、/sha256sum/、/download/、/ipfs/、/files
//	上**不做 share 作用域检查**。
//
//	对照 P2P 通道：调用方是外部对等节点，所以走 share 作用域
//	（public / unlisted / private + 好友名单）——见
//	internal/transport/visibility_matrix_mandatory_test.go。两个通道凭据不同、
//	授权模型不同，这是设计而非漏洞；本文件把 HTTP 这一侧钉死。
//
// 为什么端到端 httptest、而不单测 PSKGate：单测只证明"中间件函数在给定请求
// 上返回 401 / 放行"，证明不了"门禁真的挂在这条路由上"。把 router.go 里的
// pskGate 参数摘掉，PSKGate 单测仍然全绿，而下面的断言会翻红。接线本身就是
// 被验证的东西。
//
// 不在范围内：/c/、/anon/collections/* 走**合集自身**的 visibility / PSK /
// passcode（controller/anon.go），和 node 级 PSK 是两套独立授权。给合集路由
// 再加 node 门禁，会打断"运营方之间没交换过 node PSK"的正常分享。
//
// 为什么没有 t.Parallel()：repository.InitDB 替换包级 DB，
// controller.InitFileController 替换包级 fileSvc，而 router.Engine() 里的
// initControllers 又会重写一遍这两个全局。本包现有测试按此前提串行。

package router_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"peerdrive/internal/config"
	"peerdrive/internal/repository"
	"peerdrive/internal/router"
	"peerdrive/internal/service"
)

const (
	httpGatePSK      = "http-gate-secret-key-31"
	httpGateAdminTok = "panel-admin-token-231"
	httpGateFilename = "gate-target.bin"
	httpGateContent  = "peerdrive http 访问模型:持有 node PSK 即全量访问,不受 share 作用域约束"
)

// buildHTTPGateRouter 搭一个真实 Router：已入库一个文件 + 已配置 node PSK。
//
// 用真实的 service.FileService.Upload 让 hash 由内容算出（sha256(content)），
// 绝不预设 hash——否则测的是字符串匹配，不是"按 hash 取到内容"。
func buildHTTPGateRouter(t *testing.T, psk, adminToken string) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	storageDir, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatalf("Abs storage dir: %v", err)
	}
	if real, err := filepath.EvalSymlinks(storageDir); err == nil {
		storageDir = real
	}
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		t.Fatalf("mkdir storage: %v", err)
	}

	// file_index / file_meta 走真实 repository：/f/ 的 200 依赖 GetMeta 命中。
	if err := repository.InitDB(":memory:"); err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	cfg := &config.Config{
		StorageDir:     storageDir,
		DownloadDir:    storageDir + "/downloads",
		StorageEnable:  true,
		DisableSwagger: true,
		RateLimitRPS:   0,
		PeerPSK:        psk,
		AdminToken:     adminToken,
	}

	fileSvc := service.NewFileService(cfg)
	meta, err := fileSvc.Upload(bytes.NewReader([]byte(httpGateContent)), httpGateFilename)
	if err != nil {
		t.Fatalf("Upload gate file: %v", err)
	}
	if meta == nil || meta.Hash == "" {
		t.Fatalf("Upload returned no hash")
	}

	rt, err := router.NewRouter(router.Deps{Cfg: cfg})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return rt.Engine(), meta.Hash
}

func doGet(t *testing.T, engine *gin.Engine, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

// TestMandatory_HTTPGateRequiresCredential — "http 有门禁，只有 psk 正确才能进入"。
//
// 覆盖面：无凭据 / 空凭据 / 错 PSK（头与查询参数两条呈现路径）/ 前缀 / 超集 /
// 大小写差异，以及"管理员 Bearer 令牌也能进"这条 panel 兼容路径。
func TestMandatory_HTTPGateRequiresCredential(t *testing.T) {
	engine, hash := buildHTTPGateRouter(t, httpGatePSK, httpGateAdminTok)
	body := []byte(httpGateContent)
	target := "/f/" + hash

	cases := []struct {
		name    string
		headers map[string]string
		path    string
	}{
		{"no-credential", nil, target},
		{"empty-header", map[string]string{"X-PSK": ""}, target},
		{"wrong-psk-header", map[string]string{"X-PSK": "definitely-not-the-key"}, target},
		{"wrong-psk-query", nil, target + "?psk=definitely-not-the-key"},
		{"prefix-of-psk", map[string]string{"X-PSK": "http-gate-secret-key-3"}, target},
		{"superset-of-psk", map[string]string{"X-PSK": httpGatePSK + "!"}, target},
		{"case-mismatch", map[string]string{"X-PSK": "HTTP-GATE-SECRET-KEY-31"}, target},
		{"wrong-bearer", map[string]string{"Authorization": "Bearer nope-nope"}, target},
		{"bearer-garbage", map[string]string{"Authorization": "Token nope"}, target},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doGet(t, engine, tc.path, tc.headers)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("「%s」想拿 node 内容却没给对凭据，必须 401；实际 %d，body=%.140q",
					tc.name, w.Code, w.Body.String())
			}
			// 不能静默泄漏字节：401 的 body 是错误 JSON，不是文件内容。
			if bytes.Contains(w.Body.Bytes(), body) {
				t.Fatalf("「%s」被拒但响应体里出现了文件字节（泄漏）", tc.name)
			}
		})
	}
}

// TestMandatory_HTTPGateGrantsFullAccess — "http 有全部访问权限"。
//
// 两条呈现路径 + 四个出口族 + 元数据模式都要放行。关键是断言**原始字节完整**
// （http.ServeContent 返回的就是上传的那一段），这才是"全部访问权限"的
// 可观测含义——不是"状态码 200"，是"拿到了全部内容"。
func TestMandatory_HTTPGateGrantsFullAccess(t *testing.T) {
	engine, hash := buildHTTPGateRouter(t, httpGatePSK, httpGateAdminTok)
	body := []byte(httpGateContent)
	pskHeader := map[string]string{"X-PSK": httpGatePSK}

	// 四个出口族：/f/ 与 /sha256sum/ 都是本地 sha-寻址字节。
	for _, tc := range []struct {
		name string
		path string
	}{
		{"f-by-hash", "/f/" + hash},
		{"f-by-hash-filename", "/f/" + hash + "/" + httpGateFilename},
		{"sha256sum-by-hash", "/sha256sum/" + hash},
		{"sha256sum-by-hash-filename", "/sha256sum/" + hash + "/" + httpGateFilename},
	} {
		t.Run(tc.name+"-header", func(t *testing.T) {
			w := doGet(t, engine, tc.path, pskHeader)
			if w.Code != http.StatusOK {
				t.Fatalf("「%s」持正确 PSK 必须 200；实际 %d，body=%.140q", tc.name, w.Code, w.Body.String())
			}
			if !bytes.Equal(w.Body.Bytes(), body) {
				t.Fatalf("「%s」内容不符\nwant %d 字节 %q\n got %d 字节 %q",
					tc.name, len(body), body, w.Body.Len(), w.Body.String())
			}
		})
		// 查询参数是另一条呈现路径，语义等价。
		t.Run(tc.name+"-query", func(t *testing.T) {
			w := doGet(t, engine, tc.path+"?psk="+httpGatePSK, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("「%s」?psk= 必须 200；实际 %d，body=%.140q", tc.name, w.Code, w.Body.String())
			}
			if !bytes.Equal(w.Body.Bytes(), body) {
				t.Fatalf("「%s」查询参数路径内容不符", tc.name)
			}
		})
	}

	// 元数据模式也是"全部访问"的一部分：?meta=1 返回文件元信息而非字节。
	t.Run("meta-mode", func(t *testing.T) {
		w := doGet(t, engine, "/f/"+hash+"?meta=1", pskHeader)
		if w.Code != http.StatusOK {
			t.Fatalf("?meta=1 必须 200；实际 %d", w.Code)
		}
		if bytes.Contains(w.Body.Bytes(), body) {
			t.Fatalf("?meta=1 应返回元数据，却返回了文件字节")
		}
		var meta map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil {
			t.Fatalf("?meta=1 不是 JSON：%v\nbody=%.140q", err, w.Body.String())
		}
		if got := fmt.Sprintf("%v", meta["hash"]); !strings.Contains(got, hash[:16]) {
			t.Fatalf("?meta=1 的 hash 字段异常：%v", meta["hash"])
		}
	})

	// 边界：门禁**只覆盖文件字节出口**，不覆盖 /files 索引枚举。
	// 这是有意的范围，不是遗漏——hash 本身不构成泄漏，把它变成字节还需要
	// PSK；把 /files 关上门会打断运营者的目录浏览和
	// scripts/netdisk-traversal-probe.sh（它按 PSK 未配置的面板假设去
	// GET /files/browse）。这里把范围双向钉住，防止以后被悄悄扩宽或收窄。
	t.Run("boundary-files-index-not-gated", func(t *testing.T) {
		if w := doGet(t, engine, "/files", nil); w.Code != http.StatusOK {
			t.Fatalf("/files 应保持开放（门禁只管字节出口）；实际 %d，body=%.140q",
				w.Code, w.Body.String())
		}
		// 对照：同一个 hash 走字节出口就必须有凭据。
		if w := doGet(t, engine, "/f/"+hash, nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("/f/ 必须关门；实际 %d（范围被收窄了）", w.Code)
		}
	})
}

// TestMandatory_HTTPGateKeyComesFromNodeConfig — "http 是由 node 自己运行的"。
//
// 可观测含义：门禁比对的密钥来自**本 node 自己的运行时配置**（cfg.PeerPSK 在
// 构造 Router 时装配进 PSKGate），而不是某个对等节点报上来的值。两条证据：
//
//  1. 两个配置了**不同** PSK 的 node 实例，同一个请求在 A 上被放行、在 B 上
//     被拒——凭据是逐 node 的。
//  2. A 的 PSK 拿去配在 B 的实例上仍然被拒——密钥不可传递。
//
// 另附一条兼容路径：已配置 PEERDRIVE_ADMIN_TOKEN 时，Bearer 管理员令牌也能
// 进入（这是 admin 面板的凭据来源；不加这条，只要运营方一设 PeerPSK，
// 面板下载就全断）。
func TestMandatory_HTTPGateKeyComesFromNodeConfig(t *testing.T) {
	const pskA = "node-a-own-psk-99"
	const pskB = "node-b-own-psk-88"

	engineA, hashA := buildHTTPGateRouter(t, pskA, "")
	engineB, hashB := buildHTTPGateRouter(t, pskB, "")

	// A 用自己的 PSK → 放行。
	if w := doGet(t, engineA, "/f/"+hashA, map[string]string{"X-PSK": pskA}); w.Code != 200 {
		t.Fatalf("node A 用自己的 PSK 必须 200；实际 %d", w.Code)
	}
	// B 用自己的 PSK → 放行。
	if w := doGet(t, engineB, "/f/"+hashB, map[string]string{"X-PSK": pskB}); w.Code != 200 {
		t.Fatalf("node B 用自己的 PSK 必须 200；实际 %d", w.Code)
	}
	// 交叉：A 的 PSK 在 B 上无效——密钥不传递。
	if w := doGet(t, engineB, "/f/"+hashB, map[string]string{"X-PSK": pskA}); w.Code != 401 {
		t.Fatalf("node A 的 PSK 在 node B 上必须 401；实际 %d", w.Code)
	}
	if w := doGet(t, engineA, "/f/"+hashA, map[string]string{"X-PSK": pskB}); w.Code != 401 {
		t.Fatalf("node B 的 PSK 在 node A 上必须 401；实际 %d", w.Code)
	}
}

// TestMandatory_HTTPGateAdminTokenAdmitted — 面板兼容路径。
func TestMandatory_HTTPGateAdminTokenAdmitted(t *testing.T) {
	engine, hash := buildHTTPGateRouter(t, httpGatePSK, httpGateAdminTok)

	w := doGet(t, engine, "/f/"+hash, map[string]string{"Authorization": "Bearer " + httpGateAdminTok})
	if w.Code != http.StatusOK {
		t.Fatalf("「Bearer 管理员令牌」是面板的凭据，必须放行；实际 %d，body=%.140q",
			w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), []byte(httpGateContent)) {
		t.Fatalf("Bearer 路径拿到内容不全")
	}
}

// TestMandatory_HTTPGateOffWithoutPeerPSK — 向后兼容。
//
// PeerPSK 未配置时门禁关闭：这是历史默认（没有任何密钥门禁），不能因为"默认
// 收紧"打断现网部署。收紧是运营方主动设 PeerPSK 的动作。
func TestMandatory_HTTPGateOffWithoutPeerPSK(t *testing.T) {
	engine, hash := buildHTTPGateRouter(t, "", "")

	w := doGet(t, engine, "/f/"+hash, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("PeerPSK 未配置时不应设门禁；实际 %d，body=%.140q", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), []byte(httpGateContent)) {
		t.Fatalf("未配置 PSK 时应完整取到内容")
	}
}
