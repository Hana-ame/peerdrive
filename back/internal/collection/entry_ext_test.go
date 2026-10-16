package collection

// entry_ext_test.go: 多备选 source + metadata 扩展的校验/round-trip/取文件语义
// 与文件源访问监视测试。

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestEntry_Validation_Alternatives drives the multi-alternative-source rules:
// an entry needs at least one fetchable alternative, http alternatives must be
// absolute http(s) URLs, and the two sha fields must agree when both set.
// 发现背景: 这些输入会来自外部生成的集合 JSON（twitter-pic 生成器、对端文档），
// 校验失败必须在进 sha-文件系统前拦住（sha 用于拼 CAS 路径，URL 会被原样请求）。
func TestEntry_Validation_Alternatives(t *testing.T) {
	valid := shaOf("file")
	urlOnly := []Entry{{Path: "a.jpg", Source: &SourceRef{URL: "https://pbs.twimg.com/media/x?format=jpg"}}}
	echOnly := []Entry{{Path: "a.jpg", Source: &SourceRef{ECHURL: "https://pbs.moonchan.xyz/media/x?format=jpg"}}}
	privateOnly := []Entry{{Path: "a.jpg", Source: &SourceRef{Private: &PrivateURL{URL: "https://priv.example.com/x"}}}}
	srcSHA := []Entry{{Path: "a.jpg", Source: &SourceRef{SHA: valid}}}

	tests := []struct {
		name    string
		entries []Entry
		wantErr string
	}{
		{name: "url only ok", entries: urlOnly, wantErr: ""},
		{name: "ech-url only ok", entries: echOnly, wantErr: ""},
		{name: "private only ok", entries: privateOnly, wantErr: ""},
		{name: "source sha ok", entries: srcSHA, wantErr: ""},
		{name: "no alternative", entries: []Entry{{Path: "a"}}, wantErr: "no fetchable alternative"},
		{name: "empty source", entries: []Entry{{Path: "a", Source: &SourceRef{}}}, wantErr: "no fetchable alternative"},
		{name: "conflicting shas", entries: []Entry{{Path: "a", SHA: valid, Source: &SourceRef{SHA: shaOf("other")}}}, wantErr: "conflicting sha"},
		{name: "bad source sha", entries: []Entry{{Path: "a", Source: &SourceRef{SHA: "zz"}}}, wantErr: "invalid source sha"},
		{name: "bad url scheme", entries: []Entry{{Path: "a", Source: &SourceRef{URL: "ftp://x/y"}}}, wantErr: "scheme must be http or https"},
		{name: "url missing host", entries: []Entry{{Path: "a", Source: &SourceRef{URL: "https:///x"}}}, wantErr: "missing host"},
		{name: "url with newline", entries: []Entry{{Path: "a", Source: &SourceRef{URL: "https://x/y\r\nInjected: 1"}}}, wantErr: "line breaks"},
		{name: "bad ech-url", entries: []Entry{{Path: "a", Source: &SourceRef{ECHURL: "not-a-url"}}}, wantErr: "invalid source ech-url"},
		{name: "bad private url", entries: []Entry{{Path: "a", Source: &SourceRef{Private: &PrivateURL{URL: "file:///etc/passwd"}}}}, wantErr: "invalid source private.url"},
		{name: "name with slash", entries: []Entry{{Path: "a", SHA: valid, Name: "x/y.jpg"}}, wantErr: "invalid name"},
		{name: "name with backslash", entries: []Entry{{Path: "a", SHA: valid, Name: `x\y.jpg`}}, wantErr: "invalid name"},
		{name: "negative created_at", entries: []Entry{{Path: "a", SHA: valid, CreatedAt: -1}}, wantErr: "negative metadata"},
		{name: "negative size", entries: []Entry{{Path: "a", SHA: valid, Size: -5}}, wantErr: "negative metadata"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.entries)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestEntry_SourceMetadataRoundTrip verifies the extended fields survive the
// marshal → unmarshal round trip byte-identically.
// 发现背景: 集合 JSON 是线上/存储格式（对端逐字节交换），扩展字段在 round-trip
// 里丢失（omitempty 误配、字段漂移）会静默改变 sha 与语义，必须显式钉住。
func TestEntry_SourceMetadataRoundTrip(t *testing.T) {
	valid := shaOf("file")
	c, err := New([]Entry{{
		Path:       "tweets/HQQFyW8aIAAwogx.jpg",
		SHA:        valid,
		Preview:    valid,
		Name:       "HQQFyW8aIAAwogx.jpg",
		MIME:       "image/jpeg",
		CreatedAt:  1700000000,
		ModifiedAt: 1700000000,
		Size:       12345,
		Source: &SourceRef{
			SHA:    valid,
			URL:    "https://pbs.twimg.com/media/HQQFyW8aIAAwogx?format=jpg&name=medium",
			ECHURL: "https://pbs.moonchan.xyz/media/HQQFyW8aIAAwogx?format=jpg&name=medium",
			Private: &PrivateURL{URL: "https://priv.example.com/HQQFyW8aIAAwogx"},
		},
	}})
	require.NoError(t, err)

	data, err := c.JSON()
	require.NoError(t, err)
	require.Contains(t, string(data), `"ech-url"`)
	require.Contains(t, string(data), `"private"`)

	got, err := Unmarshal(data)
	require.NoError(t, err)
	require.Equal(t, c.Entries, got.Entries)
	require.Equal(t, "HQQFyW8aIAAwogx.jpg", got.Entries[0].Name)
	require.Equal(t, "image/jpeg", got.Entries[0].MIME)
	require.Equal(t, int64(12345), got.Entries[0].Size)
	require.Equal(t, valid, got.Entries[0].Source.SHA)
	require.Equal(t, "https://priv.example.com/HQQFyW8aIAAwogx", got.Entries[0].Source.Private.URL)
}

// TestEntry_Fetch_PriorityAndFallback drives the "any one available suffices"
// semantics with the exact degradation trajectory visible in the monitor:
// sha 失败 → ech-url 失败 → url 成功。条目不携带 sha 时跳过 sha 备选直接走远端。
// 发现背景: 这是 twitter-pic 整合与任何远端集合消费方的核心语义，
// 也是文件源访问监视的轨迹来源——断言 Recent 的顺序就是断言降级路径。
func TestEntry_Fetch_PriorityAndFallback(t *testing.T) {
	var shaCalled, echCalled bool
	openSHA := func(ctx context.Context, sha string) (io.ReadCloser, error) {
		shaCalled = true
		return nil, errors.New("not in CAS")
	}
	echSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		echCalled = true
		http.Error(w, "502", http.StatusBadGateway)
	}))
	defer echSrv.Close()
	urlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("image-bytes"))
	}))
	defer urlSrv.Close()

	mon := NewFetchMonitor(0)
	e := &Entry{
		Path: "a.jpg",
		SHA:  shaOf("x"),
		Source: &SourceRef{
			URL:    urlSrv.URL + "/a.jpg",
			ECHURL: echSrv.URL + "/a.jpg",
		},
	}
	rc, alt, err := e.Fetch(context.Background(), FetchOptions{OpenSHA: openSHA, Monitor: mon})
	require.NoError(t, err)
	defer rc.Close()
	body, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, "image-bytes", string(body))
	require.Equal(t, AltURL, alt)
	require.True(t, shaCalled, "sha alternative must be tried first")
	require.True(t, echCalled, "ech-url alternative must be tried second")

	// 降级轨迹可见：sha 失败 → ech-url 失败 → url 成功。
	recent := mon.Recent(3)
	require.Len(t, recent, 3)
	require.Equal(t, []string{AltSHA, AltECHURL, AltURL},
		[]string{recent[0].Alt, recent[1].Alt, recent[2].Alt})
	require.False(t, recent[0].OK)
	require.False(t, recent[1].OK)
	require.True(t, recent[2].OK)
}

// TestEntry_Fetch_PrivateLast verifies private.url is the last resort and is used
// only when everything above it failed.
func TestEntry_Fetch_PrivateLast(t *testing.T) {
	privSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("private-bytes"))
	}))
	defer privSrv.Close()

	e := &Entry{Path: "a", Source: &SourceRef{
		URL:     "http://127.0.0.1:1/never", // 不可达端口，快速失败
		ECHURL:  "http://127.0.0.1:1/never2",
		Private: &PrivateURL{URL: privSrv.URL + "/a"},
	}}
	rc, alt, err := e.Fetch(context.Background(), FetchOptions{Client: &http.Client{Timeout: 2 * time.Second}})
	require.NoError(t, err)
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	require.Equal(t, "private-bytes", string(body))
	require.Equal(t, AltPrivate, alt)
}

// TestEntry_Fetch_AllFail drives the all-fail aggregation and monitor recording
// of every attempt.
func TestEntry_Fetch_AllFail(t *testing.T) {
	e := &Entry{Path: "a", Source: &SourceRef{
		URL:     "http://127.0.0.1:1/never",
		ECHURL:  "http://127.0.0.1:1/never2",
		Private: &PrivateURL{URL: "http://127.0.0.1:1/never3"},
	}}
	mon := NewFetchMonitor(0)
	_, _, err := e.Fetch(context.Background(), FetchOptions{Client: &http.Client{Timeout: 2 * time.Second}, Monitor: mon})
	require.Error(t, err)
	require.Contains(t, err.Error(), "all alternatives failed")

	recent := mon.Recent(0)
	require.Len(t, recent, 3)
	for _, a := range recent {
		require.False(t, a.OK)
		require.NotEmpty(t, a.Err)
		// 发现背景: Windows 上 time.Now 是 100ns 粒度，loopback 拒绝连接可在一个
		// tick 内完成，Duration 会实测为 0——断言「耗时被记录」不能依赖时钟粒度，
		// 改为断言尝试时刻 At 已记录（绝对时刻不会为 0）。
		require.False(t, a.At.IsZero(), "attempt time must be recorded")
		require.GreaterOrEqual(t, a.Duration, time.Duration(0))
	}
}

// TestEntry_Fetch_RemoteOnlyWithoutOpenSHA verifies an entry without a sha can be
// fetched purely from its remote alternative even when no SHAOpener is configured.
func TestEntry_Fetch_RemoteOnlyWithoutOpenSHA(t *testing.T) {
	urlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("remote"))
	}))
	defer urlSrv.Close()
	e := &Entry{Path: "a.jpg", Source: &SourceRef{URL: urlSrv.URL + "/a.jpg"}}
	rc, alt, err := e.Fetch(context.Background(), FetchOptions{}) // OpenSHA nil
	require.NoError(t, err)
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	require.Equal(t, "remote", string(body))
	require.Equal(t, AltURL, alt)
}

// TestFetchMonitor_Snapshot verifies the window stats answer "past N attempts,
// per-source access counts and success rates", plus cumulative counters.
// 发现背景: 监视器要能回答「时间窗内各源访问次数与成功率分布」——快照的窗口
// 统计来自 recent 环，累计统计来自按源计数，两者必须同时可查。
func TestFetchMonitor_Snapshot(t *testing.T) {
	mon := NewFetchMonitor(4) // 窗口 = 最近 4 次
	now := time.Now()
	mon.Record(Attempt{Alt: AltSHA, OK: false, At: now.Add(-4 * time.Second), Duration: time.Millisecond, Err: "miss"})
	mon.Record(Attempt{Alt: AltECHURL, OK: true, At: now.Add(-3 * time.Second), Duration: time.Millisecond, Bytes: 10})
	mon.Record(Attempt{Alt: AltURL, OK: false, At: now.Add(-2 * time.Second), Duration: time.Millisecond, Err: "500"})
	mon.Record(Attempt{Alt: AltSHA, OK: true, At: now.Add(-time.Second), Duration: time.Millisecond, Bytes: 5})
	// 超出窗口的一条（总 5 次尝试，窗口 4）
	mon.Record(Attempt{Alt: AltSHA, OK: false, At: now, Duration: time.Millisecond, Err: "miss2"})

	stats := mon.Snapshot()
	require.Len(t, stats, 3)

	var shaStat, echStat, urlStat *AltStat
	for i := range stats {
		switch stats[i].Alt {
		case AltSHA:
			shaStat = &stats[i]
		case AltECHURL:
			echStat = &stats[i]
		case AltURL:
			urlStat = &stats[i]
		}
	}
	require.NotNil(t, shaStat)
	require.NotNil(t, echStat)
	require.NotNil(t, urlStat)

	// sha 窗口内 2 次（1 成功 1 失败），累计 3 次（1 成功 2 失败）
	require.Equal(t, int64(2), shaStat.Attempts)
	require.Equal(t, int64(1), shaStat.Success)
	require.Equal(t, int64(1), shaStat.Fail)
	require.InDelta(t, 0.5, shaStat.SuccessRate, 1e-9)
	require.Equal(t, int64(3), shaStat.TotalAttempts)
	require.Equal(t, int64(1), shaStat.TotalSuccess)
	require.Equal(t, "miss2", shaStat.LastErr)

	// ech-url 窗口内 1 次全成功，成功率 1
	require.Equal(t, int64(1), echStat.Attempts)
	require.InDelta(t, 1.0, echStat.SuccessRate, 1e-9)
	require.Equal(t, int64(10), echStat.Bytes)

	// url 窗口内 1 次全失败，成功率 0
	require.Equal(t, int64(1), urlStat.Attempts)
	require.InDelta(t, 0.0, urlStat.SuccessRate, 1e-9)
}

// TestFetchMonitor_Concurrent verifies counting stays exact under concurrent
// recording (the -race detector must stay quiet here).
// 发现背景: 多条媒体摄取/多个消费方会并发取文件，计数错位会让监视数据失真；
// 并发下总数必须等于记录数。
func TestFetchMonitor_Concurrent(t *testing.T) {
	mon := NewFetchMonitor(0)
	const perG = 50
	const g = 8
	var wg sync.WaitGroup
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			alt := AltURL
			if i%2 == 0 {
				alt = AltSHA
			}
			for j := 0; j < perG; j++ {
				mon.Record(Attempt{Alt: alt, OK: j%3 != 0, At: time.Now(), Bytes: int64(j)})
			}
		}(i)
	}
	wg.Wait()

	stats := mon.Snapshot()
	totalAttempts, totalSuccess := int64(0), int64(0)
	for _, s := range stats {
		totalAttempts += s.TotalAttempts
		totalSuccess += s.TotalSuccess
	}
	require.Equal(t, int64(g*perG), totalAttempts)
	// 每个 goroutine 内 j%3!=0 → 0..49 中 3 的倍数有 17 个 → 33 成功
	require.Equal(t, int64(g*33), totalSuccess)
	// 窗口（默认 256）不应吞掉累计计数
	require.Len(t, mon.Recent(0), 256)
}

// TestStoreFile_ReadFile_RoundTrip verifies arbitrary (non-collection) content
// can be stored into and read back from the sha-file system.
// 发现背景: StoreFile/ReadFile 是 twitter-pic 摄取媒体（图库数据面）写入与
// 读出的基座——集合 JSON 之外的任何字节都要能按内容地址存取。
func TestStoreFile_ReadFile_RoundTrip(t *testing.T) {
	storageDir := t.TempDir()
	data := []byte("media-bytes-内容-12345")
	sha, err := StoreFile(storageDir, data)
	require.NoError(t, err)
	require.Len(t, sha, 64)

	got, err := ReadFile(storageDir, sha)
	require.NoError(t, err)
	require.Equal(t, data, got)

	// 幂等：同内容同地址
	sha2, err := StoreFile(storageDir, data)
	require.NoError(t, err)
	require.Equal(t, sha, sha2)

	// 内容与地址不符：ReadFile 不校验（它读任意字节），但地址拼出的文件必须
	// 真实存在——不存在要报 not found。
	_, err = ReadFile(storageDir, shaOf("never-stored"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found locally")

	// 非法 sha 在拼路径前被拦（sha[:2] 对短串会 panic）。
	_, err = ReadFile(storageDir, "abc")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid sha")
}

// TestCollection_SHA256_ExtendedFormatDeterministic verifies that extended
// entries still produce a deterministic, order-independent content address, and
// that the pre-extension locked format is byte-identical (the locked digest in
// TestCollection_SHA256_LocksFormat must keep passing — this test guards the
// determinism property for the new fields).
func TestCollection_SHA256_ExtendedFormatDeterministic(t *testing.T) {
	valid := shaOf("file")
	mk := func(order int) *Collection {
		a := Entry{Path: "b.txt", SHA: valid, Name: "b.txt", MIME: "text/plain", CreatedAt: 1,
			Source: &SourceRef{URL: "https://x.example/b"}}
		b := Entry{Path: "a.jpg", SHA: shaOf("other"), Name: "a.jpg", MIME: "image/jpeg", CreatedAt: 2,
			Source: &SourceRef{URL: "https://x.example/a", ECHURL: "https://p.example/a"}}
		es := []Entry{a, b}
		if order == 1 {
			es = []Entry{b, a}
		}
		c, err := New(es)
		require.NoError(t, err)
		return c
	}
	s1, err := mk(0).SHA256()
	require.NoError(t, err)
	s2, err := mk(1).SHA256()
	require.NoError(t, err)
	require.Equal(t, s1, s2)
}

