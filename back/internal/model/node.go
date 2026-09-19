// node.go：节点市场（目录）相关模型。
//
// 背景（2026-09-20 网盘目标，见 doc/NETDISK.md M1）：
// 用户要的是「有别人的节点，可以在市场里加入节点」。发现服务器的
// /discover/nodes 已能列出在线节点，但那是给程序看的原始列表；这里定义
// 面向界面的「市场条目」——把在线状态、直连状态、是否已加入、共享摘要
// 合并成一行，前端直接渲染卡片。
package model

import "time"

// NodeShares 节点共享摘要（只报数量，不报内容）。
//
// 为什么只报数量：announce 的 loadInfo 会经发现服务器广播给所有查询者，
// 具体 hash / 文件名一旦上报就等于公开「本节点持有什么」。数量足够支撑
// 市场卡片的「该节点共享了 3 个合集 / 12 个文件」这类引导信息，细节要等
// 用户真正加入并建立直连后，走 share 帧（点对点）再拿。
type NodeShares struct {
	Collections int `json:"collections"`
	Files       int `json:"files"`
	Dirs        int `json:"dirs,omitempty"`
}

// NodeSummary 市场/我的节点列表里的一行。
type NodeSummary struct {
	PeerID    string     `json:"peer_id"`
	NodeType  string     `json:"node_type,omitempty"`
	LastSeen  int64      `json:"last_seen,omitempty"` // Unix 秒（发现服务器给的时间）
	Uptime    int64      `json:"uptime,omitempty"`
	Online    bool       `json:"online"`    // 发现服务器认为在线
	Connected bool       `json:"connected"` // 本节点当前有 WebRTC 直连
	Joined    bool       `json:"joined"`    // 运营者已加入（持久化）
	JoinedAt  *time.Time `json:"joined_at,omitempty"`
	Self      bool       `json:"self,omitempty"` // 就是本节点自己
	Shares    NodeShares `json:"shares"`
}
