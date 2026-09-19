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

// SetShareProvider 注入本节点共享范围读取器（main 装配 service.NodeShare.Snapshot）。
// nil = 未启用共享 → share 帧回空快照。可在 Start() 之后调用（见字段注释）。
func (s *PeerJSService) SetShareProvider(p func() ShareSnapshot) {
	s.shareMu.Lock()
	s.shareProvider = p
	s.shareMu.Unlock()
}

// currentShareProvider 快照读取共享范围读取器（受锁保护）。
func (s *PeerJSService) currentShareProvider() func() ShareSnapshot {
	s.shareMu.RLock()
	defer s.shareMu.RUnlock()
	return s.shareProvider
}

// shareLoadInfo announce 时上报的共享摘要（loadInfo.shares），只含**数量**。
// 为什么不报具体 hash：announce 会经发现服务器广播给所有查询者，报 hash
// 等于公开"本节点持有什么"；数量足够支撑市场卡片的引导信息，细节等用户
// 加入并直连后走 share 帧（点对点）再拿。
//
// 注意：本函数作为回调注册给 HTTPDiscovery（announce 心跳每 30s 调用），
// 每次调用都重新读 provider —— 装配晚于 Start（main 的顺序）也能生效。
func (s *PeerJSService) shareLoadInfo() map[string]any {
	p := s.currentShareProvider()
	if p == nil {
		return nil
	}
	snap := p()
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
// 注意：不区分请求者身份（ROADMAP 硬约束：第 7 阶段前不引入账号依赖）。
// 因此这里只会返回**本来就允许公开**的内容（public 合集 + 运营者显式声明的
// 目录内文件），受限/私有的过滤在 service.NodeShare.Snapshot 内完成。
func (s *PeerJSService) serveShare(c Session, r dcResp) {
	snap := ShareSnapshot{}
	if p := s.currentShareProvider(); p != nil {
		snap = p()
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
