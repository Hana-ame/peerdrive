// twitterpic_test.go: twitter-pic 模块 HTTP 暴露面的测试——未启用（默认）时
// 全部 503（开关关 = 路由不注册，controller 级兜底 503），启用时走通
// build → respond-by-sha → fetch-stats 的端到端。
package controller

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"peerdrive/internal/twitterpic"
)

// ginTestWithParams 调用 handler 并注入任意路由参数（ginTest 只支持 :id，
// 而 /twitterpic/users/:username/build 的参数键是 username）。
func ginTestWithParams(t *testing.T, r gin.HandlerFunc, method, target string, params gin.Params) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	c.Params = params
	r(c)
	return w
}

// TestTwitterPic_Disabled_503 pins the "开关关 = 零变化" contract at the HTTP
// surface: with no service injected (the default node never constructs one),
// every /twitterpic endpoint answers 503 "not enabled" instead of half-working.
func TestTwitterPic_Disabled_503(t *testing.T) {
	orig := twitterSvc
	twitterSvc = nil
	t.Cleanup(func() { twitterSvc = orig })

	for _, tt := range []struct {
		name   string
		handle gin.HandlerFunc
		method string
		target string
	}{
		{"users", TwitterPicUsers, http.MethodGet, "/twitterpic/users"},
		{"build", TwitterPicBuild, http.MethodPost, "/twitterpic/users/userA/build"},
		{"collection", TwitterPicCollection, http.MethodGet, "/twitterpic/collections/" + "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90a1"},
		{"fetch-stats", TwitterPicFetchStats, http.MethodGet, "/twitterpic/fetch-stats"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := ginTest(t, tt.handle, tt.method, tt.target, "")
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body=%s", w.Code, w.Body.String())
			}
		})
	}
}

// TestTwitterPic_BuildAndRespondBySHA drives the enabled module end-to-end over
// a fake gallery backend: POST build → 200 with collection sha; GET by sha →
// canonical JSON bytes; GET fetch-stats → per-alternative monitor snapshot.
func TestTwitterPic_BuildAndRespondBySHA(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/" && r.URL.Query().Get("list") == "users":
			w.Header().Set("Content-Type", "application/json")
			page := []map[string]string{}
			if r.URL.Query().Get("after") == "" {
				page = []map[string]string{{"username": "userA"}}
			}
			_ = json.NewEncoder(w).Encode(page)
		case len(r.URL.Path) > 1 && r.URL.Path[len(r.URL.Path)-8:] == ".json.gz":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			_ = json.NewEncoder(gz).Encode(map[string]any{
				"account_info": map[string]string{"name": "userA"},
				"timeline": []map[string]any{
					{"url": "https://pbs.twimg.com/media/CTRL1?format=jpg&name=medium", "type": "photo", "date": 1700000000},
				},
			})
			_ = gz.Close()
		case r.URL.Path == "/media/CTRL1":
			_, _ = w.Write([]byte("ctrl-media-bytes"))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer fake.Close()

	svc, err := twitterpic.NewService(twitterpic.ServiceConfig{
		BaseURL:    fake.URL,
		ProxyBase:  fake.URL,
		StorageDir: t.TempDir(),
		Timeout:    5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	orig := twitterSvc
	twitterSvc = svc
	t.Cleanup(func() { twitterSvc = orig })

	// build
	w := ginTestWithParams(t, TwitterPicBuild, http.MethodPost, "/twitterpic/users/userA/build",
		gin.Params{{Key: "username", Value: "userA"}})
	if w.Code != http.StatusOK {
		t.Fatalf("build status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var res struct {
		Username string `json:"username"`
		SHA      string `json:"sha"`
		Entries  int    `json:"entries"`
		Ingested int    `json:"ingested"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("build body not JSON: %v", err)
	}
	if res.SHA == "" || len(res.SHA) != 64 || res.Entries != 1 || res.Ingested != 1 {
		t.Fatalf("build result = %+v", res)
	}

	// respond by sha（媒体已进 sha-文件系统，集合 JSON 原样可读）
	w = ginTestWithParams(t, TwitterPicCollection, http.MethodGet, "/twitterpic/collections/"+res.SHA,
		gin.Params{{Key: "sha", Value: res.SHA}})
	if w.Code != http.StatusOK {
		t.Fatalf("collection status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var coll struct {
		Version int `json:"version"`
		Entries []struct {
			Path string `json:"path"`
			SHA  string `json:"sha"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &coll); err != nil {
		t.Fatalf("collection body not JSON: %v", err)
	}
	// 集合版本/条目数与媒体 sha（条目 sha 是媒体的内容地址，与集合 sha 不同）
	if coll.Version != 1 || len(coll.Entries) != 1 ||
		coll.Entries[0].Path != "tweets/CTRL1.jpg" ||
		len(coll.Entries[0].SHA) != 64 {
		t.Fatalf("collection = %+v", coll)
	}

	// fetch-stats：监视器快照可查
	w = ginTest(t, TwitterPicFetchStats, http.MethodGet, "/twitterpic/fetch-stats", "")
	if w.Code != http.StatusOK {
		t.Fatalf("fetch-stats status = %d, want 200", w.Code)
	}
	var stats struct {
		ByAlt []struct {
			Alt    string `json:"alt"`
			Total  int64  `json:"total_attempts"`
			OK     int64  `json:"total_success"`
			Rate   float64 `json:"success_rate"`
		} `json:"by_alt"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("fetch-stats body not JSON: %v", err)
	}
	if len(stats.ByAlt) == 0 {
		t.Fatal("fetch-stats: empty snapshot")
	}

	// 读取一个已有集合时传非法 sha → 400（respond-by-sha 的入口校验）
	w = ginTestWithParams(t, TwitterPicCollection, http.MethodGet, "/twitterpic/collections/not-a-sha",
		gin.Params{{Key: "sha", Value: "not-a-sha"}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad sha status = %d, want 400", w.Code)
	}

	// ListUsers 也走通
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	users, err := svc.ListUsers(ctx)
	if err != nil || len(users) != 1 || users[0].Username != "userA" {
		t.Fatalf("ListUsers = %v, %v", users, err)
	}
}
