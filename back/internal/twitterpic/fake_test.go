package twitterpic

// fake_test.go: 假 twitter-pic 数据面后端（httptest），供 client/builder/service
// 测试离线使用。忠实还原前端源码确认的接口契约：
//   - GET /?list=users&after=<cursor>：按 after 游标分页的用户列表；
//   - GET /<user>.json.gz?t=<date>：gzip 的单个用户 JSON（Content-Encoding: gzip）；
//   - GET /media/<id>?...：媒体字节（ech-url 备选指向这里）。

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeAPI 是一个可配置的假后端。
type fakeAPI struct {
	t *testing.T

	mu         sync.Mutex
	users      []User      // 全量用户（按 after 游标切页）
	pageSize   int         // 0 = 一次全给
	metas      map[string]*UserMeta
	media      map[string][]byte // path → bytes（媒体按路径命中）
	media404   map[string]bool   // path → 返回 404（模拟媒体缺失）
	userCalls  int
	mediaCalls map[string]int

	srv *httptest.Server
}

// newFakeAPI 启动假后端。
func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{
		t:          t,
		metas:      make(map[string]*UserMeta),
		media:      make(map[string][]byte),
		media404:   make(map[string]bool),
		mediaCalls: make(map[string]int),
		pageSize:   2,
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) url() string { return f.srv.URL }

func (f *fakeAPI) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/media/"):
		f.handleMedia(w, r)
	case strings.HasSuffix(r.URL.Path, ".json.gz"):
		f.handleUser(w, r)
	case r.URL.Query().Get("list") == "users":
		f.handleUsers(w, r)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (f *fakeAPI) handleUsers(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userCalls++
	after := r.URL.Query().Get("after")
	start := 0
	if after != "" {
		for i, u := range f.users {
			if u.Username == after {
				start = i + 1
				break
			}
		}
	}
	end := len(f.users)
	if f.pageSize > 0 && start+f.pageSize < end {
		end = start + f.pageSize
	}
	page := f.users[start:end]
	if page == nil {
		page = []User{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(page)
}

func (f *fakeAPI) handleUser(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".json.gz")
	f.mu.Lock()
	meta, ok := f.metas[username]
	f.mu.Unlock()
	if !ok {
		http.Error(w, "no such user", http.StatusNotFound)
		return
	}
	data, err := json.Marshal(meta)
	if err != nil {
		http.Error(w, "marshal", http.StatusInternalServerError)
		return
	}
	// 忠实于 .json.gz 后缀：gzip + Content-Encoding 头；http.Transport 会自动解压。
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write(data)
	_ = gz.Close()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Encoding", "gzip")
	_, _ = w.Write(buf.Bytes())
}

func (f *fakeAPI) handleMedia(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// 媒体按 path+query 命中（媒体 URL 带 format/name 参数，入口按完整请求
	// 路径区分不同变体；与前端 extractMediaPath 保留 query 的约定一致）。
	key := r.URL.RequestURI()
	f.mediaCalls[key]++
	if f.media404[key] {
		http.Error(w, "media gone", http.StatusNotFound)
		return
	}
	data, ok := f.media[key]
	if !ok {
		http.Error(w, "no media", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	_, _ = w.Write(data)
}

// mediaURL 构造指向假后端的 twimg 形态媒体 URL：/media/<id>?format=jpg&name=medium。
func (f *fakeAPI) mediaURL(id string) string {
	return f.srv.URL + "/media/" + id + "?format=jpg&name=medium"
}
