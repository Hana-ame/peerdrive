// Package twitterpic integrates the twitter-pic image gallery (twitter-pic-go
// 的延伸；旧版界面入口 x.4545810.xyz，数据面跑在 bwh 的 twitter-pic 服务上)
// into peerdrive as "one user = one collection".
//
// Data surface (investigated 2026-10-09, see the PR report):
//   - The frontend hard-codes its API base to https://x.moonchan.xyz/api/twitter
//     (x.4545810.xyz itself is only the old SPA entry; every path on it returns
//     the SPA fallback HTML today).
//   - GET {base}/<user>.json.gz?t=<date>  → per-user JSON:
//     { "account_info": {name, nick, profile_image},
//       "timeline": [ {url, type, date?}, ... ] }
//     where url is the original twimg media URL
//     (https://pbs.twimg.com/media/<mediaID>?format=jpg&name=medium) and type is
//     photo | video | animated_gif. There is no per-item media hash in this API —
//     the base32 "01LLWEUU…" hashes live on upload.moonchan.xyz's /api/<hash>/
//     surface, which the timeline does not reference. So entry sha = sha256 of
//     the ingested bytes (content addressing), preview = the media itself for
//     photos (the API has no separate thumbnail).
//   - GET {base}/?list=users&after=<cursor> → user list, paginated by the
//     `after` cursor; empty array ends the list.
//   - Media is served through the fixed image proxy https://pbs.moonchan.xyz +
//     path (the frontend strips pbs.twimg.com's origin and prepends the proxy).
//
// Mapping "user → collection":
//   BuildUserCollection fetches the user JSON, dedups timeline items by URL,
//   ingests each media file into the sha-file system (content-addressed, via
//   collection.StoreFile), and emits one collection.Entry per media with
//   path (tweets/<mediaID>.<ext>), name, mime, created_at/modified_at, size and
//   a multi-alternative source (sha + original url + ech-url through the proxy).
//   The resulting collection JSON is saved to the sha-file system via
//   collection.Save — read it back by its sha with collection.ReadJSON. A
//   per-user pointer file (storageDir/twitterpic/index.json) records the latest
//   collection sha per username (name → latest-sha mapping; the collection body
//   itself is content-addressed).
//
// Source-interface relation (PR #59): this integration is a collection
// generator + entry fetcher, not a hash-addressed Source — the gallery's media
// URLs are not content-addressed until ingested. After ingest the bytes live in
// the CAS, so they are served by the existing sha sources (ShaSource /
// LocalSource) with zero registration changes; the cold-path fetches go through
// the same entry Fetch semantics as any collection entry (sha → ech-url → url
// → private.url) with a shared FetchMonitor.
package twitterpic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// 默认端点来自前端源码（src/api/endpoints.ts），不是猜的：
//   ENDPOINT = "https://x.moonchan.xyz/api/twitter"
//   FIXED_IMAGE_PROXY = "https://pbs.moonchan.xyz"（图片唯一来源；视频候选链
//   首项 twimg.l.moonchan.xyz:8443 在 CN 更可达，但 26-10-08 起图片已收敛到
//   pbs.moonchan.xyz 单一源）。
const (
	DefaultBaseURL   = "https://x.moonchan.xyz/api/twitter"
	DefaultProxyBase = "https://pbs.moonchan.xyz"
)

// Client 是 twitter-pic 数据面 API 的最小客户端。
type Client struct {
	base string // 无尾斜杠
	hc   *http.Client
}

// NewClient 构造 API 客户端；base 为空用 DefaultBaseURL，client 为空用
// http.DefaultClient（注入 ech-proxy Transport 即可让 API/媒体请求走出口）。
func NewClient(base string, hc *http.Client) (*Client, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	if _, err := url.ParseRequestURI(base); err != nil {
		return nil, fmt.Errorf("twitterpic: base url %q: %w", base, err)
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{base: base, hc: hc}, nil
}

// User 是用户列表里的一条。字段来自 ?list=users 返回（前端 HeaderV2 只读
// username 与 last_modify）。
type User struct {
	Username   string `json:"username"`
	LastModify string `json:"last_modify,omitempty"`
	Nick       string `json:"nick,omitempty"`
}

// AccountInfo 是单个用户 JSON 里的账户信息（昵称头像等）。
type AccountInfo struct {
	Name         string `json:"name"`
	Nick         string `json:"nick"`
	ProfileImage string `json:"profile_image"`
}

// TimelineItem 是用户 JSON 里的一条推文媒体。url 是原始 twimg 媒体直链，
// type 是 photo / video / animated_gif，date 是推文时间（数值或字符串，
// 前端用 new Date(item.date || Date.now())，故保留原始字节由 parseItemDate 解释）。
type TimelineItem struct {
	URL  string          `json:"url"`
	Type string          `json:"type"`
	Date json.RawMessage `json:"date,omitempty"`
}

// UserMeta 是 /<user>.json.gz 的响应体。error 非空表示服务端侧错误（该用户
// 不存在/被封禁等），前端对 e.error 有专门处理。
type UserMeta struct {
	AccountInfo *AccountInfo    `json:"account_info"`
	Timeline    []TimelineItem  `json:"timeline"`
	Error       json.RawMessage `json:"error,omitempty"`
}

// ListUsers 拉全量用户列表：/ ?list=users&after=<cursor>，跟随 after 游标直到
// 空页（分页收敛；前端 LoadMoreButton 用 userList.at(-1).username 作 after）。
func (c *Client) ListUsers(ctx context.Context) ([]User, error) {
	var out []User
	after := ""
	for {
		page, err := c.listUsersPage(ctx, after)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) == 0 {
			return out, nil
		}
		after = page[len(page)-1].Username
	}
}

// listUsersPage 拉一页用户列表。
func (c *Client) listUsersPage(ctx context.Context, after string) ([]User, error) {
	u := c.base + "/?list=users"
	if after != "" {
		u += "&after=" + url.QueryEscape(after)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("twitterpic: list users: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("twitterpic: list users: HTTP %d", resp.StatusCode)
	}
	var page []User
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("twitterpic: list users: decode: %w", err)
	}
	return page, nil
}

// GetUser 拉单个用户 JSON：/<user>.json.gz?t=<t>。t 是日期级 cache-buster
// （前端传 last_modify 或今天日期）；空 t 时用今天日期（与前端一致）。
func (c *Client) GetUser(ctx context.Context, username, t string) (*UserMeta, error) {
	if strings.TrimSpace(username) == "" {
		return nil, fmt.Errorf("twitterpic: empty username")
	}
	u := c.base + "/" + url.PathEscape(username) + ".json.gz"
	if t == "" {
		t = time.Now().Format("2006-01-02")
	}
	u += "?t=" + url.QueryEscape(t)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("twitterpic: get user %q: %w", username, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("twitterpic: get user %q: HTTP %d", username, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("twitterpic: get user %q: read: %w", username, err)
	}
	var meta UserMeta
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, fmt.Errorf("twitterpic: get user %q: decode: %w", username, err)
	}
	return &meta, nil
}

// parseItemDate 把 TimelineItem.Date 的原始字节解释成 Unix 秒；无法解释时
// 返回 0（= 未知，与条目元数据约定一致）。
//   - 数值：>1e12 视为毫秒（new Date(ms) 语义），否则视为秒。
//   - 字符串：优先 RFC3339 / 日期串；纯数字串按秒。
func parseItemDate(raw json.RawMessage) int64 {
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	var num float64
	if err := json.Unmarshal(raw, &num); err == nil {
		if num > 1e12 {
			return int64(num / 1000)
		}
		return int64(num)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if tm, err := time.Parse(layout, s); err == nil {
			return tm.Unix()
		}
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	return 0
}
