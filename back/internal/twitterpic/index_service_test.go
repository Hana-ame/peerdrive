package twitterpic

// index_service_test.go: 用户指针文件 + Service 门面 + httptest 端到端。

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"peerdrive/internal/collection"
)

// TestUserIndex_SetGetList verifies the user → collection-sha pointer file
// round-trip (write / read / list).
// 发现背景: 集合本体内容寻址（按 sha 读出），但「user 最近一次构建的 sha」
// 是名字→地址映射，落在 storageDir/twitterpic/index.json——这是本整合的
// 「按 user 找回集合」入口。
func TestUserIndex_SetGetList(t *testing.T) {
	ui := NewUserIndex(t.TempDir())
	_, ok, err := ui.Get("userA")
	require.NoError(t, err)
	require.False(t, ok, "首次查询不存在")

	require.NoError(t, ui.Set("userA", "a1b2c3", 4))
	rec, ok, err := ui.Get("userA")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "a1b2c3", rec.SHA)
	require.Equal(t, 4, rec.Entries)
	require.NotZero(t, rec.BuiltAt)

	require.NoError(t, ui.Set("userB", "d4e5f6", 2))
	all, err := ui.List()
	require.NoError(t, err)
	require.Len(t, all, 2)
	require.Equal(t, "a1b2c3", all["userA"].SHA)

	// 覆盖写同 user。
	require.NoError(t, ui.Set("userA", "newsha", 5))
	rec, _, _ = ui.Get("userA")
	require.Equal(t, "newsha", rec.SHA)
}

// TestService_EndToEnd drives the whole integration over a fake backend:
// ListUsers → BuildUserCollection (media ingest + pointer update) →
// ReadCollection by sha → monitor snapshot showing per-alternative access
// counts and the degradation trajectory.
// 发现背景: 这是「httptest 端到端（假 twitter-pic 后端）」验收——集合按 sha
// 读出、监视器能回答「各源访问次数与成功率分布」。
func TestService_EndToEnd(t *testing.T) {
	f := newFakeAPI(t)
	f.users = []User{{Username: "userA", LastModify: "2026-01-01"}, {Username: "userB"}}
	f.metas["userA"] = &UserMeta{
		AccountInfo: &AccountInfo{Name: "userA", Nick: "用户A"},
		Timeline: []TimelineItem{
			{URL: "https://pbs.twimg.com/media/A1?format=jpg&name=medium", Type: "photo", Date: json.RawMessage(`1700000000`)},
			{URL: "https://pbs.twimg.com/media/A2?format=jpg&name=medium", Type: "photo"},
		},
	}
	f.metas["userB"] = &UserMeta{AccountInfo: &AccountInfo{Name: "userB"}, Timeline: []TimelineItem{}}
	f.media["/media/A1?format=jpg&name=medium"] = []byte("a1-bytes")
	f.media["/media/A2?format=jpg&name=medium"] = []byte("a2-bytes")

	svc, err := NewService(ServiceConfig{BaseURL: f.url(), ProxyBase: f.url(), StorageDir: t.TempDir(), Timeout: 5 * time.Second})
	require.NoError(t, err)
	require.Equal(t, f.url(), svc.BaseURL())

	// 1) 用户列表
	users, err := svc.ListUsers(context.Background())
	require.NoError(t, err)
	require.Len(t, users, 2)
	require.Equal(t, "userA", users[0].Username)

	// 2) 构建 userA 集合
	res, err := svc.BuildUserCollection(context.Background(), "userA")
	require.NoError(t, err)
	require.Equal(t, 2, res.Ingested)
	require.Len(t, res.Collection.Entries, 2)

	// 指针文件已更新
	rec, ok, err := svc.UserIndex().Get("userA")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, res.CollectionSHA, rec.SHA)

	// 3) 按 sha 读出（respond-by-sha 路径）
	raw, err := svc.ReadCollection(context.Background(), res.CollectionSHA)
	require.NoError(t, err)
	got, err := collection.Unmarshal(raw)
	require.NoError(t, err)
	require.Equal(t, res.Collection.Entries, got.Entries)

	// 4) 监视快照：摄取走了 ech-url（假后端），0 次 url/private 尝试
	stats := svc.FetchStats()
	require.NotEmpty(t, stats)
	foundECH := false
	for _, s := range stats {
		if s.Alt == collection.AltECHURL {
			foundECH = true
			require.Equal(t, int64(2), s.TotalAttempts, "2 条媒体都命中 ech-url")
			require.Equal(t, int64(2), s.TotalSuccess)
			require.InDelta(t, 1.0, s.SuccessRate, 1e-9)
		}
	}
	require.True(t, foundECH, "监视器必须记录 ech-url 备选的访问")

	// 降级轨迹可见：每条媒体一次 ech-url 成功
	recent := svc.RecentAttempts(0)
	require.Len(t, recent, 2)
	for _, a := range recent {
		require.Equal(t, collection.AltECHURL, a.Alt)
		require.True(t, a.OK)
		// 发现背景: Windows 上 time.Now 可能是粗粒度系统时钟，快速 loopback
		// 请求实测 Duration 为 0——断言「尝试被记录」用绝对时刻 At，不依赖
		// 时钟粒度。
		require.False(t, a.At.IsZero(), "attempt time must be recorded")
		require.GreaterOrEqual(t, a.Duration, time.Duration(0))
		require.Equal(t, int64(len("a1-bytes")), a.Bytes)
	}

	// 5) 空用户也构建成功
	resB, err := svc.BuildUserCollection(context.Background(), "userB")
	require.NoError(t, err)
	require.Empty(t, resB.Collection.Entries)
}

// TestService_NewService_RequiresStorage verifies StorageDir is enforced at
// construction (a misconfigured module must fail at startup, not on first use).
func TestService_NewService_RequiresStorage(t *testing.T) {
	_, err := NewService(ServiceConfig{BaseURL: "https://x.moonchan.xyz/api/twitter"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "StorageDir")
}

// TestService_Build_ServerErrorAndMonitor verifies a failing build still leaves
// the monitor able to answer "what was tried and how it failed".
func TestService_Build_ServerErrorAndMonitor(t *testing.T) {
	f := newFakeAPI(t)
	f.metas["banned"] = &UserMeta{Error: json.RawMessage(`"banned"`)}
	svc, err := NewService(ServiceConfig{BaseURL: f.url(), ProxyBase: f.url(), StorageDir: t.TempDir(), Timeout: 5 * time.Second})
	require.NoError(t, err)
	_, err = svc.BuildUserCollection(context.Background(), "banned")
	require.Error(t, err)
	// 没有媒体尝试 → 监视器保持空（不产生噪音统计）
	require.Empty(t, svc.FetchStats())
}
