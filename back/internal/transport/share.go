package transport

// share.go：节点共享范围帧（doc/NETDISK.md M2 / ROADMAP 阶段 5「文件范围管理」）。
//
// 语义：share 帧回答「本节点对外提供了什么」——打包好的合集（含条目清单）
// 与单独的文件。这是「市场 → 加入节点 → 看到文件链接 → 选中保存」链路里
// "看到文件链接"那一步。
//
// 与既有 list verb 的区别（**不要合并**）：
//   - list = 本地文件管理索引（file_index 全量，含本机绝对路径），语义是
//     "本节点在管理哪些文件"，只应对可信对端/本地会话开放；
//   - share = **显式声明**的共享范围，默认关闭，是唯一对外发布内容的入口。
// 把 list 当成"共享清单"用，等于默认全盘对外公开（也正是 ROADMAP 里
// "广播本地合集 hash 等于公开本节点持有什么"那条待办的成因）。
//
// 帧序列：
//
//	请求: {"type":"share","reqId":"<optional>"}
//	响应: {"type":"share-resp","collections":[...],"files":[...],"dirs":[...],
//	       "total":N,"reqId":"..."}
//
// total = len(collections)+len(files)，服务端算好下发（前端卡片直接显示
// "共 N 项"，不必自己按两类计数相加）。未开启共享时回**空** share-resp
// 而不是 err：空态是合法业务状态（对方未共享任何内容），前端直接渲染
// "该节点没有共享内容"，不必走错误分支。

// ShareFileInfo 共享清单里的一个单独文件。
type ShareFileInfo struct {
	Hash string `json:"hash"`
	Name string `json:"name"`
	Path string `json:"path,omitempty"` // 相对根目录的展示路径（不含本机绝对路径）
	Size int64  `json:"size"`
	Mime string `json:"mime,omitempty"`
}

// ShareEntryInfo 合集内一个条目的文件链接。
type ShareEntryInfo struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
	Mime string `json:"mime,omitempty"`
}

// ShareCollectionInfo 一个被打包共享的合集。
type ShareCollectionInfo struct {
	Hash    string            `json:"hash"`
	Name    string            `json:"name,omitempty"`
	Size    int64             `json:"size,omitempty"` // 条目数（沿用 size 命名与前端卡片统一）
	Tags    []string          `json:"tags,omitempty"`
	Entries []ShareEntryInfo  `json:"entries"`
}

// ShareSnapshot 一次 share 查询的完整结果。
type ShareSnapshot struct {
	Collections []ShareCollectionInfo `json:"collections"`
	Files       []ShareFileInfo       `json:"files"`
	Dirs        []string              `json:"dirs,omitempty"`
}

// shareResp share 帧响应。内嵌 ShareSnapshot 让 JSON 平铺
// （{type,collections,files,dirs,total,reqId}），前端一层解析即可。
type shareResp struct {
	Type string `json:"type"`
	ShareSnapshot
	Total int    `json:"total"`
	ReqID string `json:"reqId,omitempty"`
}

// SetShareProvider 注入本节点共享范围读取器（main 装配
// service.NodeShare.SnapshotFor）。入参是请求者节点 ID——好友能看到 private 条目。
// nil = 未启用共享 → share 帧回空快照。可在 Start() 之后调用（见字段注释）。
func (s *PeerJSService) SetShareProvider(p func(peerID string) ShareSnapshot) {
	s.shareMu.Lock()
	s.shareProvider = p
	s.shareMu.Unlock()
}

// currentShareProvider 快照读取共享范围读取器（受锁保护）。
func (s *PeerJSService) currentShareProvider() func(peerID string) ShareSnapshot {
	s.shareMu.RLock()
	defer s.shareMu.RUnlock()
	return s.shareProvider
}

// ShareGate 下载门禁：判断某个 hash 能否发给请求者（doc/NETDISK.md §12.6）。
//
// 为什么清单与下载要分开：三档级别的本质区别就在"列不列"和"给不给"两件事上
// （unlisted = 不列但给）。一个 provider 只能回答"清单里有什么"，答不了
// "这个 hash 能不能取"。
//
// 只有 private 会挡人：public / unlisted / 未声明都放行——内容寻址取回是这套
// 系统的既有行为，PSK 才是准入门禁。把"没声明"也挡掉会连"上传→按 hash 取回
// 校验"这种基本自检都过不去。
//
// self = 本节点的本地通道（HTTP 管理 API / 本机 WS 直连），永远是"自己"。
type ShareGate interface {
	AllowsDownload(peerID, hash string, self bool) bool
}

// SetShareGate 注入下载门禁（main 装配 service.NodeShare）。nil = 不门禁。
func (s *PeerJSService) SetShareGate(g ShareGate) {
	s.shareMu.Lock()
	s.shareGate = g
	s.shareMu.Unlock()
}

// currentShareGate 快照读取下载门禁（受锁保护）。
func (s *PeerJSService) currentShareGate() ShareGate {
	s.shareMu.RLock()
	defer s.shareMu.RUnlock()
	return s.shareGate
}

// isSelfSession 判断会话是否来自"自己"（本机 WS 直连，即管理台/面板走
// /ws/peer 的本地连接）。
//
// 为什么需要它：private 的语义是"只有自己和好友能下载"。P2P 连接上只有对端
// 自报的 peer id，运营者自己的面板拿到的也是一个随机 id（每次可能不同），
// 没法靠 id 认出"这是我"。而走本机 WS 进来的连接本来就是本节点的管理通道，
// 它就是"自己"。
func isSelfSession(c Session) bool {
	ls, ok := c.(interface{ IsLocal() bool })
	return ok && ls.IsLocal()
}

// shareLoadInfo announce 时上报的共享摘要（loadInfo.shares），只含**数量**。
// 为什么不报具体 hash：announce 会经发现服务器广播给所有查询者，报 hash
// 等于公开"本节点持有什么"；数量足够支撑市场卡片的引导信息，细节等用户
// 加入并直连后走 share 帧（点对点）再拿。
//
// 注意：本函数作为回调注册给 HTTPDiscovery（announce 心跳每 30s 调用），
// 每次调用都重新读 provider —— 装配晚于 Start（main 的顺序）也能生效。
//
// 这里以**匿名视角**（空 peerID）统计：announce 的数量会被发现服务器广播给
// 所有查询者，不能因为"某个查询者恰好是好友"就把 private 的条目数报出去——
// 那等于把"我有 N 个只给好友的东西"公开了。
func (s *PeerJSService) shareLoadInfo() map[string]any {
	p := s.currentShareProvider()
	if p == nil {
		return nil
	}
	snap := p("")
	return map[string]any{
		"shares": map[string]any{
			"collections": len(snap.Collections),
			"files":       len(snap.Files),
			"dirs":        len(snap.Dirs),
		},
	}
}

// serveShare 应答对端的 share 查询（入站角色）。
//
// 请求者身份只有一个**自报的** peer id（ROADMAP 硬约束：第 7 阶段前不引入账号
// 依赖，也没有签名可校验）。它能做的只有一件事：让好友名单里的人看到 private
// 条目——再多就是假装自己有身份了。unlisted 永不列出，public 全部列出，
// 这些过滤在 service.NodeShare.SnapshotFor 内完成。
func (s *PeerJSService) serveShare(c Session, r dcResp) {
	snap := ShareSnapshot{}
	if p := s.currentShareProvider(); p != nil {
		snap = p(c.ID())
	}
	// 保证 JSON 里是 [] 而不是 null：前端列表渲染不必判空
	if snap.Collections == nil {
		snap.Collections = []ShareCollectionInfo{}
	}
	if snap.Files == nil {
		snap.Files = []ShareFileInfo{}
	}
	total := len(snap.Collections) + len(snap.Files)
	_ = c.SendJSON(shareResp{Type: "share-resp", ShareSnapshot: snap, Total: total, ReqID: r.ReqID})
}
