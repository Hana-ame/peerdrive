package twitterpic

// client_test.go: API 客户端（用户列表分页、单用户 json.gz、日期解析）。

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestClient_ListUsers_Pagination drives the after-cursor pagination loop:
// the list is fetched page by page until an empty page, preserving order.
// 发现背景: 前端 LoadMoreButton 用「上一页最后一个 username」作 after 游标，
// 后端返回空数组表示结束——客户端必须跟随游标收敛，否则大图库的用户列表
// 只拿得到第一页。
func TestClient_ListUsers_Pagination(t *testing.T) {
	f := newFakeAPI(t)
	f.users = []User{
		{Username: "u1", LastModify: "2026-01-01"},
		{Username: "u2", LastModify: "2026-01-02"},
		{Username: "u3", LastModify: "2026-01-03"},
		{Username: "u4", LastModify: "2026-01-04"},
		{Username: "u5", LastModify: "2026-01-05"},
	}
	f.pageSize = 2

	c, err := NewClient(f.url(), nil)
	require.NoError(t, err)
	users, err := c.ListUsers(context.Background())
	require.NoError(t, err)
	require.Len(t, users, 5)
	got := make([]string, 0, 5)
	for _, u := range users {
		got = append(got, u.Username)
	}
	require.Equal(t, []string{"u1", "u2", "u3", "u4", "u5"}, got)

	// 3 页 + 1 次空页终止 → 4 次请求
	f.mu.Lock()
	calls := f.userCalls
	f.mu.Unlock()
	require.Equal(t, 4, calls)
}

// TestClient_GetUser_GzipAndFields verifies the per-user json.gz fetch
// transparently decodes gzip and maps the account_info/timeline fields.
// 发现背景: 数据面按 .json.gz 后缀返回 gzip 内容；字段名来自前端源码
// （account_info.name/nick/profile_image、timeline[].url/type/date）。
func TestClient_GetUser_GzipAndFields(t *testing.T) {
	f := newFakeAPI(t)
	f.metas["userA"] = &UserMeta{
		AccountInfo: &AccountInfo{Name: "userA", Nick: "用户A", ProfileImage: "https://pbs.twimg.com/profile/a.jpg"},
		Timeline: []TimelineItem{
			{URL: "https://pbs.twimg.com/media/HQQFyW8aIAAwogx?format=jpg&name=medium", Type: "photo", Date: json.RawMessage(`1700000000`)},
		},
	}

	c, err := NewClient(f.url(), nil)
	require.NoError(t, err)
	meta, err := c.GetUser(context.Background(), "userA", "")
	require.NoError(t, err)
	require.NotNil(t, meta.AccountInfo)
	require.Equal(t, "userA", meta.AccountInfo.Name)
	require.Equal(t, "用户A", meta.AccountInfo.Nick)
	require.Len(t, meta.Timeline, 1)
	require.Equal(t, "photo", meta.Timeline[0].Type)
	require.Equal(t, int64(1700000000), parseItemDate(meta.Timeline[0].Date))
}

// TestClient_GetUser_ServerError verifies a server-side error field is surfaced
// to the caller (build 阶段据此判「该用户不可用」)。
func TestClient_GetUser_ServerError(t *testing.T) {
	f := newFakeAPI(t)
	f.metas["blocked"] = &UserMeta{Error: json.RawMessage(`"banned"`)}
	c, err := NewClient(f.url(), nil)
	require.NoError(t, err)
	meta, err := c.GetUser(context.Background(), "blocked", "")
	require.NoError(t, err)
	require.NotNil(t, meta.Error)
}

// TestParseItemDate covers the number/string/millisecond interpretations.
// 发现背景: 前端用 new Date(item.date || Date.now())——date 可能是 unix 秒、
// unix 毫秒、RFC3339 或日期串；解析失败返回 0（= 未知）而不是报错，保证
// 单个脏数据不拖垮整个集合构建。
func TestParseItemDate(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int64
	}{
		{name: "empty", raw: ``, want: 0},
		{name: "null", raw: `null`, want: 0},
		{name: "unix seconds", raw: `1700000000`, want: 1700000000},
		{name: "unix milliseconds", raw: `1700000000000`, want: 1700000000},
		{name: "rfc3339", raw: `"2023-11-15T01:33:20Z"`, want: time.Date(2023, 11, 15, 1, 33, 20, 0, time.UTC).Unix()},
		{name: "date only", raw: `"2023-11-15"`, want: time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC).Unix()},
		{name: "numeric string", raw: `"1700000000"`, want: 1700000000},
		{name: "junk", raw: `"not a date"`, want: 0},
		{name: "object junk", raw: `{"a":1}`, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, parseItemDate(json.RawMessage(tt.raw)))
		})
	}
}

// TestMediaHelpers pins the URL → id/name/mime/proxy derivations that entries
// are built from.
// 发现背景: 这些派生镜像前端 extractFileName/extractMediaPath 的规则——路径
// 最后一段是媒体 id、扩展名取 format 参数、代理链 = 代理基址 + 相对路径。
func TestMediaHelpers(t *testing.T) {
	p, err := parseMediaURL("https://pbs.twimg.com/media/HQQFyW8aIAAwogx?format=jpg&name=medium")
	require.NoError(t, err)
	require.Equal(t, "HQQFyW8aIAAwogx", p.ID)
	require.Equal(t, "jpg", p.Format)
	require.Equal(t, "/media/HQQFyW8aIAAwogx?format=jpg&name=medium", p.Path)

	require.Equal(t, "HQQFyW8aIAAwogx.jpg", fileName(p.ID, p.Format, "photo"))
	require.Equal(t, "HQQFyW8aIAAwogx.mp4", fileName(p.ID, "", "video"))
	require.Equal(t, "HQQFyW8aIAAwogx.mp4", fileName(p.ID, "", "animated_gif"))
	require.Equal(t, "HQQFyW8aIAAwogx.webp", fileName(p.ID, "webp", "photo"))

	require.Equal(t, "image/jpeg", mimeFor("jpg", "photo"))
	require.Equal(t, "image/webp", mimeFor("webp", "photo"))
	require.Equal(t, "video/mp4", mimeFor("", "video"))
	require.Equal(t, "video/mp4", mimeFor("", "animated_gif"))
	require.Equal(t, "", mimeFor("", "quote"))

	require.Equal(t, "https://pbs.moonchan.xyz/media/HQQFyW8aIAAwogx?format=jpg&name=medium",
		proxyMediaURL("https://pbs.twimg.com/media/HQQFyW8aIAAwogx?format=jpg&name=medium", "https://pbs.moonchan.xyz"))
	require.Equal(t, "", proxyMediaURL("not-a-url", "https://pbs.moonchan.xyz"))
}
