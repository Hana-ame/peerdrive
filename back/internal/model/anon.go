// Package model 定义匿集合（AnonCollection）及其条目的数据结构。
// 匿名集合通过 SHA256 内容寻址存储，支持版本递增和标签。
// Version=1: 旧格式 entries[].hash（向后兼容读取）
// Version=2: 新格式 entries[].providers[]（sha256/url 多源）
package model

import (
	"encoding/json"
	"time"
)

// Provider 表示一个文件来源，可以是 SHA256 哈希或 URL。
type Provider struct {
	Type     string `json:"type"`                // "sha256" | "url"
	Value    string `json:"value"`               // 64-char hex hash, or HTTP(S) URL
	MimeType string `json:"mime_type,omitempty"` // 显式 MIME，覆盖自动检测
}

// AnonCollectionEntry 表示合集中的一个文件条目，支持多 provider。
// 自定义 JSON 处理：旧格式 {path, hash} 自动归一化为 providers。
type AnonCollectionEntry struct {
	Path      string     `json:"path"`
	Providers []Provider `json:"providers"`
	Hash      string     `json:"hash,omitempty"` // 仅向后兼容反序列化
}

type anonCollectionEntryAlias AnonCollectionEntry

// UnmarshalJSON 支持旧格式 {path, hash} 和新格式 {path, providers}。
func (e *AnonCollectionEntry) UnmarshalJSON(data []byte) error {
	var raw struct {
		Path      string     `json:"path"`
		Hash      string     `json:"hash,omitempty"`
		Providers []Provider `json:"providers,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	e.Path = raw.Path
	if len(raw.Providers) > 0 {
		e.Providers = raw.Providers
	} else if raw.Hash != "" {
		e.Providers = []Provider{{Type: "sha256", Value: raw.Hash}}
	} else {
		e.Providers = []Provider{}
	}
	return nil
}

// MarshalJSON 输出 providers 格式，Version=1 时额外输出 hash 保证旧客户端兼容。
func (e AnonCollectionEntry) MarshalJSON() ([]byte, error) {
	type out struct {
		Path      string     `json:"path"`
		Providers []Provider `json:"providers"`
		Hash      string     `json:"hash,omitempty"`
	}
	o := out{Path: e.Path, Providers: e.Providers}
	if len(e.Providers) > 0 {
		for _, p := range e.Providers {
			if p.Type == "sha256" && p.Value != "" {
				o.Hash = p.Value
				break
			}
		}
	}
	if o.Providers == nil {
		o.Providers = []Provider{}
	}
	return json.Marshal(o)
}

// Normalize 将 struct literal 设置的 Hash 字段迁移到 Providers（如尚未设置）。
func (e *AnonCollectionEntry) Normalize() {
	if len(e.Providers) == 0 && e.Hash != "" {
		e.Providers = []Provider{{Type: "sha256", Value: e.Hash}}
	}
}

// GetPrimaryHash 返回第一个 sha256 provider 的 hash 值，没有则返回空串。
func (e *AnonCollectionEntry) GetPrimaryHash() string {
	for _, p := range e.Providers {
		if p.Type == "sha256" && p.Value != "" {
			return p.Value
		}
	}
	return ""
}

// GetPrimaryMime 返回显式 MIME 或第一个有 MIME 的 provider 的值。
func (e *AnonCollectionEntry) GetPrimaryMime() string {
	for _, p := range e.Providers {
		if p.MimeType != "" {
			return p.MimeType
		}
	}
	return ""
}

type AnonCollection struct {
	Version      int                   `json:"version"`
	FriendlyName string                `json:"friendly_name,omitempty"`
	Entries      []AnonCollectionEntry `json:"entries"`
	Tags         []string              `json:"tags,omitempty"`
	CreatedAt    string                `json:"created_at"`
	// Visibility 可见性：public=公开可广播 / restricted=仅 AccessList 内账号 / private=仅 Owner。
	// 背景：2026-09 前端「广播」改为三选项（公开访问 / 仅限指定权限 / 仅自己），
	// 旧匿名集合没有任何权限字段，空值一律按 public 处理以保持向后兼容。
	Visibility string `json:"visibility,omitempty"`
	// AccessList 是 restricted 时放行的账号清单（regserver 侧唯一用户名）。
	AccessList []string `json:"access_list,omitempty"`
	// Owner 是发布者账号（节点 operator），private 集合只对它放行。
	Owner string `json:"owner,omitempty"`
}

// visibility 取值常量：与前端三选项一一对应，后端只接受这三个字符串。
const (
	VisibilityPublic     = "public"
	VisibilityRestricted = "restricted"
	VisibilityPrivate    = "private"
)

// IsValidVisibility 校验可见性取值，空串视为未设置（合法，等价于 public）。
func IsValidVisibility(v string) bool {
	switch v {
	case "", VisibilityPublic, VisibilityRestricted, VisibilityPrivate:
		return true
	}
	return false
}

// EffectiveVisibility 返回兜底后的可见性：历史集合没有该字段 → public。
func (c *AnonCollection) EffectiveVisibility() string {
	if c == nil || c.Visibility == "" {
		return VisibilityPublic
	}
	return c.Visibility
}

// CanView 判断某个请求者（账号名）能否查看该集合。
// 语义：public 放行所有人；restricted 放行 Owner + AccessList；private 只放行 Owner。
// 坑：requester 为空代表未认证请求，不能因此放行 private/restricted——
// 否则任何没带身份的 P2P 同步都能拖走受限合集。
func (c *AnonCollection) CanView(requester string) bool {
	if c == nil {
		return false
	}
	switch c.EffectiveVisibility() {
	case VisibilityRestricted:
		if requester != "" && requester == c.Owner {
			return true
		}
		for _, a := range c.AccessList {
			if requester != "" && a == requester {
				return true
			}
		}
		return false
	case VisibilityPrivate:
		return requester != "" && requester == c.Owner
	default:
		return true
	}
}

// NormalizeEntries 将 Version=1 的旧格式条目升级为 providers 格式。
func (c *AnonCollection) NormalizeEntries() {
	if c.Version >= 2 {
		return
	}
	for i := range c.Entries {
		if len(c.Entries[i].Providers) == 0 && c.Entries[i].Hash != "" {
			c.Entries[i].Providers = []Provider{{Type: "sha256", Value: c.Entries[i].Hash}}
		}
	}
	c.Version = 2
}

// NewAnonCollection 创建一个新的匿名集合，版本为 2（providers 格式）。
func NewAnonCollection(name string, entries []AnonCollectionEntry, tags []string) *AnonCollection {
	if entries == nil {
		entries = []AnonCollectionEntry{}
	}
	if tags == nil {
		tags = []string{}
	}
	return &AnonCollection{
		Version:      2,
		FriendlyName: name,
		Entries:      entries,
		Tags:         tags,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
}

type AnonCollectionSummary struct {
	Hash         string   `json:"hash"`
	FriendlyName string   `json:"friendly_name,omitempty"`
	NamePreview  string   `json:"name_preview,omitempty"`
	Version      int      `json:"version"`
	Tags         []string `json:"tags,omitempty"`
	EntryCount   int      `json:"entry_count"`
	CreatedAt    string   `json:"created_at"`
	// Visibility / Owner 供列表页在合集卡片上直接显示权限档位（前端三选项回填）。
	Visibility string `json:"visibility,omitempty"`
	Owner      string `json:"owner,omitempty"`
}
