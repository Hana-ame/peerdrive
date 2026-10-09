package twitterpic

// builder_test.go: user → collection 生成器（正常/空用户/缺媒体/数量上限/去重/
// 服务端错误），全部离线（假后端）。

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"peerdrive/internal/collection"
)

// buildOptsFor 构造指向假后端的构建参数。
func buildOptsFor(t *testing.T, f *fakeAPI) (BuildOptions, *Client) {
	c, err := NewClient(f.url(), nil)
	require.NoError(t, err)
	return BuildOptions{
		StorageDir: t.TempDir(),
		ProxyBase:  f.url(),
	}, c
}

// TestBuildUserCollection_Normal drives the happy path: entries carry
// path/name/mime/times/size and a multi-alternative source (sha + url +
// ech-url); photos get preview = own sha (the API has no separate thumbnail);
// the collection is saved into the sha-file system and reads back by sha.
// 发现背景: 这是「一个 user 作为一个 collection」的主链路——条目 sha 来自摄取
// 字节的 sha256（数据面不提供每媒体 hash），preview 用原图自身（无缩略图）。
func TestBuildUserCollection_Normal(t *testing.T) {
	f := newFakeAPI(t)
	f.metas["userA"] = &UserMeta{
		AccountInfo: &AccountInfo{Name: "userA", Nick: "用户A"},
		Timeline: []TimelineItem{
			{URL: "https://pbs.twimg.com/media/HQQFyW8aIAAwogx?format=jpg&name=medium", Type: "photo", Date: json.RawMessage(`1700000000`)},
			{URL: "https://pbs.twimg.com/media/HQQFyW8aIAAwogx2?format=jpg&name=medium", Type: "photo", Date: json.RawMessage(`1700000100`)},
			{URL: "https://pbs.twimg.com/media/VID123?format=mp4&name=orig", Type: "video", Date: json.RawMessage(`1700000200`)},
			{URL: "https://pbs.twimg.com/media/GIF1?format=mp4&name=orig", Type: "animated_gif", Date: json.RawMessage(`"2023-11-15T01:33:20Z"`)},
		},
	}
	// ech-url 备选指向假后端 → 摄取命中 ech-url，不触外网。
	f.media["/media/HQQFyW8aIAAwogx?format=jpg&name=medium"] = []byte("photo-a-bytes")
	f.media["/media/HQQFyW8aIAAwogx2?format=jpg&name=medium"] = []byte("photo-b-bytes")
	f.media["/media/VID123?format=mp4&name=orig"] = []byte("video-bytes")
	f.media["/media/GIF1?format=mp4&name=orig"] = []byte("gif-bytes")

	opts, c := buildOptsFor(t, f)
	res, err := BuildUserCollection(context.Background(), c, "userA", opts)
	require.NoError(t, err)
	require.Equal(t, 4, res.Ingested)
	require.Len(t, res.Collection.Entries, 4)

	// 按 path 排序后断言每条条目。
	byPath := map[string]collection.Entry{}
	for _, e := range res.Collection.Entries {
		byPath[e.Path] = e
	}
	photo := byPath["tweets/HQQFyW8aIAAwogx.jpg"]
	require.Equal(t, "HQQFyW8aIAAwogx.jpg", photo.Name)
	require.Equal(t, "image/jpeg", photo.MIME)
	require.Equal(t, int64(1700000000), photo.CreatedAt)
	require.Equal(t, int64(1700000000), photo.ModifiedAt)
	require.Len(t, photo.SHA, 64)
	require.Equal(t, photo.SHA, photo.Preview, "photo 的 preview = 自身 sha（原图即预览）")
	require.Equal(t, int64(len("photo-a-bytes")), photo.Size)
	require.NotNil(t, photo.Source)
	require.Equal(t, "https://pbs.twimg.com/media/HQQFyW8aIAAwogx?format=jpg&name=medium", photo.Source.URL)
	require.Equal(t, f.url()+"/media/HQQFyW8aIAAwogx?format=jpg&name=medium", photo.Source.ECHURL)
	require.Equal(t, photo.SHA, photo.Source.SHA)

	video := byPath["tweets/VID123.mp4"]
	require.Equal(t, "video/mp4", video.MIME)
	require.Len(t, video.SHA, 64)
	require.Empty(t, video.Preview, "非 photo 无 preview（无缩略图）")
	require.Equal(t, int64(len("video-bytes")), video.Size)

	gif := byPath["tweets/GIF1.mp4"]
	require.Equal(t, "video/mp4", gif.MIME)
	require.Empty(t, gif.Preview)
	require.Equal(t, time.Date(2023, 11, 15, 1, 33, 20, 0, time.UTC).Unix(), gif.CreatedAt, "RFC3339 解析为 unix 秒")

	// 媒体已进 sha-文件系统，按 sha 可读回。
	data, err := collection.ReadFile(opts.StorageDir, photo.SHA)
	require.NoError(t, err)
	require.Equal(t, "photo-a-bytes", string(data))

	// 集合按 sha 可读回，且 round-trip 一致。
	raw, err := collection.ReadJSON(opts.StorageDir, res.CollectionSHA)
	require.NoError(t, err)
	got, err := collection.Unmarshal(raw)
	require.NoError(t, err)
	require.Equal(t, res.Collection.Entries, got.Entries)
}

// TestBuildUserCollection_EmptyUser verifies an empty timeline still yields a
// valid, saveable collection (zero entries is a normal state, not an error).
func TestBuildUserCollection_EmptyUser(t *testing.T) {
	f := newFakeAPI(t)
	f.metas["lurker"] = &UserMeta{AccountInfo: &AccountInfo{Name: "lurker"}, Timeline: []TimelineItem{}}
	opts, c := buildOptsFor(t, f)
	res, err := BuildUserCollection(context.Background(), c, "lurker", opts)
	require.NoError(t, err)
	require.Empty(t, res.Collection.Entries)
	require.Zero(t, res.Ingested)
	require.Len(t, res.CollectionSHA, 64)

	raw, err := collection.ReadJSON(opts.StorageDir, res.CollectionSHA)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"entries":[]`)
}

// TestBuildUserCollection_MissingPreview_Media404 verifies the "缺 preview"
// path: media download failure keeps the entry with its remote alternatives
// (sha/preview empty) instead of dropping it — the collection still saves.
// 发现背景: 远端媒体可能 404/超时；条目要「能解析、能展示、可稍后补取」，
// 而不是把用户集合砍成残缺。
func TestBuildUserCollection_MissingPreview_Media404(t *testing.T) {
	f := newFakeAPI(t)
	f.metas["userA"] = &UserMeta{
		AccountInfo: &AccountInfo{Name: "userA"},
		Timeline: []TimelineItem{
			{URL: f.mediaURL("GONE"), Type: "photo", Date: json.RawMessage(`1700000000`)},
		},
	}
	f.media404["/media/GONE?format=jpg&name=medium"] = true

	opts, c := buildOptsFor(t, f)
	res, err := BuildUserCollection(context.Background(), c, "userA", opts)
	require.NoError(t, err)
	require.Zero(t, res.Ingested)
	require.Len(t, res.Collection.Entries, 1)
	e := res.Collection.Entries[0]
	require.Empty(t, e.SHA, "媒体下载失败 → sha 为空")
	require.Empty(t, e.Preview, "无预览")
	require.NotEmpty(t, e.Source.URL, "保留远端备选")
	require.NotEmpty(t, e.Source.ECHURL)
	require.Equal(t, "tweets/GONE.jpg", e.Path)
	require.Equal(t, "image/jpeg", e.MIME)
}

// TestBuildUserCollection_MaxFilesCap verifies MaxFiles bounds media ingest only:
// entries beyond the cap stay remote-only, but every timeline item is present
// (the collection stays complete while ingest cost is bounded).
func TestBuildUserCollection_MaxFilesCap(t *testing.T) {
	f := newFakeAPI(t)
	meta := &UserMeta{AccountInfo: &AccountInfo{Name: "userA"}}
	for i := 1; i <= 5; i++ {
		id := "IDX" + string(rune('0'+i))
		meta.Timeline = append(meta.Timeline, TimelineItem{
			URL:  "https://pbs.twimg.com/media/" + id + "?format=jpg&name=medium",
			Type: "photo",
		})
		f.media["/media/"+id+"?format=jpg&name=medium"] = []byte("b" + id)
	}
	f.metas["userA"] = meta

	opts, c := buildOptsFor(t, f)
	opts.MaxFiles = 2
	res, err := BuildUserCollection(context.Background(), c, "userA", opts)
	require.NoError(t, err)
	require.Len(t, res.Collection.Entries, 5, "所有 timeline 条目都在集合里")
	require.Equal(t, 2, res.Ingested, "只摄取前 2 条")

	ingestedPaths, remotePaths := 0, 0
	for _, e := range res.Collection.Entries {
		if e.SHA != "" {
			ingestedPaths++
		} else {
			remotePaths++
			require.NotEmpty(t, e.Source.URL)
		}
	}
	require.Equal(t, 2, ingestedPaths)
	require.Equal(t, 3, remotePaths)
}

// TestBuildUserCollection_DedupURL verifies duplicate URLs collapse to one
// entry (the frontend dedups the timeline by url for the same reason).
func TestBuildUserCollection_DedupURL(t *testing.T) {
	f := newFakeAPI(t)
	f.metas["userA"] = &UserMeta{
		AccountInfo: &AccountInfo{Name: "userA"},
		Timeline: []TimelineItem{
			{URL: "https://pbs.twimg.com/media/SAME?format=jpg&name=medium", Type: "photo"},
			{URL: "https://pbs.twimg.com/media/SAME?format=jpg&name=medium", Type: "photo"},
		},
	}
	f.media["/media/SAME?format=jpg&name=medium"] = []byte("same")

	opts, c := buildOptsFor(t, f)
	res, err := BuildUserCollection(context.Background(), c, "userA", opts)
	require.NoError(t, err)
	require.Len(t, res.Collection.Entries, 1)
}

// TestBuildUserCollection_ServerError verifies a server-side error on the user
// (banned / not exist) aborts the build instead of emitting a hollow collection.
func TestBuildUserCollection_ServerError(t *testing.T) {
	f := newFakeAPI(t)
	f.metas["banned"] = &UserMeta{Error: json.RawMessage(`"account suspended"`)}
	opts, c := buildOptsFor(t, f)
	_, err := BuildUserCollection(context.Background(), c, "banned", opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "server error")
}

// TestBuildUserCollection_MaxBytes verifies the per-file cap rejects oversized
// media (entry keeps remote alternatives).
func TestBuildUserCollection_MaxBytes(t *testing.T) {
	f := newFakeAPI(t)
	f.metas["userA"] = &UserMeta{
		AccountInfo: &AccountInfo{Name: "userA"},
		Timeline:    []TimelineItem{{URL: "https://pbs.twimg.com/media/BIG?format=jpg&name=medium", Type: "photo"}},
	}
	f.media["/media/BIG?format=jpg&name=medium"] = []byte("0123456789")

	opts, c := buildOptsFor(t, f)
	opts.MaxBytes = 4
	res, err := BuildUserCollection(context.Background(), c, "userA", opts)
	require.NoError(t, err)
	require.Zero(t, res.Ingested)
	require.Empty(t, res.Collection.Entries[0].SHA)
	require.NotEmpty(t, res.Collection.Entries[0].Source.URL)
}
