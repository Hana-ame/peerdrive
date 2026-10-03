package service

// node_directory.go：节点市场目录（doc/NETDISK.md M1）。
//
// 目标能力：前端「市场」页要列出别人的节点，能加入、能退出；加入后节点
// 成为本节点的常驻对端（重启后自动重连）。
//
// 三个数据来源合并成一行市场条目：
//  1. 发现服务器 /discover/nodes（在线 + nodeType + 共享摘要 loadInfo）
//  2. 本节点当前 WebRTC 直连表（connected）
//  3. 本地 joined_nodes.json（运营者加入过的节点，离线也保留——否则重启后
//     市场里看到的东西就全丢了，用户会以为"我加的节点没了"）
//
// 为什么 joined 用独立 JSON 文件而不是 SQLite：这是一份很小的运营者偏好
// （peerId + 时间），且要能在无 DB 的场景（纯 client 模式/单元测试/CI）
// 下工作。原子写（临时文件 + rename）保证进程被 kill 时不会留下半截文件。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/transport"
)

// joinedFileName 加入清单的相对文件名（位于 storageDir 下）。
const joinedFileName = "joined_nodes.json"

// joinedFile 磁盘格式。用对象包一层而不是裸数组：以后要加字段（备注、
// 别名、加入时的共享快照）时不用改顶层结构。
type joinedFile struct {
	Peers []joinedPeer `json:"peers"`
}

type joinedPeer struct {
	PeerID   string    `json:"peer_id"`
	JoinedAt time.Time `json:"joined_at"`
}

// discoveredNode 发现服务器 /discover/nodes 的单条响应。
// 字段与 back/signalserver 的 NodeInfo 对齐；缺失字段一律容忍（线上信令由
// 外部维护，不能假设它一定返回 nodeType/loadInfo）。
type discoveredNode struct {
	PeerID      string         `json:"peerId"`
	LastSeen    int64          `json:"lastSeen"`
	NodeType    string         `json:"nodeType"`
	Uptime      int64          `json:"uptime"`
	Collections []string       `json:"collections"`
	LoadInfo    map[string]any `json:"loadInfo"`
}

// NodeDirectory 节点市场目录服务。
type NodeDirectory struct {
	discoverURL string
	path        string // joined_nodes.json 绝对路径

	selfID    func() string
	connected func() map[string]bool
	dial      func(peerID string)
	// shareSummary 本节点共享摘要（M2 的 NodeShare 服务注入；nil = 未启用共享）
	shareSummary func() model.NodeShares

	http *http.Client

	mu     sync.Mutex
	joined map[string]time.Time // peerID → 加入时间
}

// NewNodeDirectory 创建目录服务并载入已加入清单。
// storageDir 为空时退回当前目录（与 storage 各处的容错一致：宁可写错位置
// 也不要让服务起不来）。discoverURL 为空表示没有发现服务器——市场只能看到
// 已加入的节点（不报错，前端显示空态 + 提示）。
func NewNodeDirectory(storageDir, discoverURL string) *NodeDirectory {
	if storageDir == "" {
		storageDir = "."
	}
	d := &NodeDirectory{
		discoverURL: strings.TrimRight(strings.TrimSpace(discoverURL), "/"),
		path:        filepath.Join(storageDir, joinedFileName),
		http:        &http.Client{Timeout: 10 * time.Second},
		joined:      make(map[string]time.Time),
	}
	d.load()
	return d
}

// SetSelfID 注入本节点 peer id 读取器（用于把自己从市场列表里剔除/标记）。
func (d *NodeDirectory) SetSelfID(fn func() string) { d.selfID = fn }

// SetConnected 注入当前直连表读取器（peerID → true）。
func (d *NodeDirectory) SetConnected(fn func() map[string]bool) { d.connected = fn }

// SetDial 注入拨号器：Join 后立即尝试建立连接（不等下一次发现轮询）。
func (d *NodeDirectory) SetDial(fn func(peerID string)) { d.dial = fn }

// ---- 已加入清单（持久化） ----

func (d *NodeDirectory) load() {
	raw, err := os.ReadFile(d.path)
	if err != nil {
		// 文件不存在是正常起点（首次运行），不告警
		if !os.IsNotExist(err) {
			log.LogWarn("node-directory: read %s failed: %v", d.path, err)
		}
		return
	}
	var f joinedFile
	if err := json.Unmarshal(raw, &f); err != nil {
		// 损坏的清单不阻塞服务：忽略并保留原文件（人工可查），下次 Join 覆盖
		log.LogWarn("node-directory: parse %s failed: %v", d.path, err)
		return
	}
	for _, p := range f.Peers {
		if p.PeerID == "" {
			continue
		}
		t := p.JoinedAt
		if t.IsZero() {
			t = time.Now()
		}
		d.joined[p.PeerID] = t
	}
}

// saveLocked 原子落盘（调用方持锁）：临时文件 + rename。
// 为什么必须原子：这个文件在每次 Join/Leave 都写，直接 WriteFile 在断电/
// SIGKILL 时可能留下空文件或半截 JSON，下次启动整份清单丢失。
func (d *NodeDirectory) saveLocked() error {
	f := joinedFile{Peers: make([]joinedPeer, 0, len(d.joined))}
	for id, at := range d.joined {
		f.Peers = append(f.Peers, joinedPeer{PeerID: id, JoinedAt: at})
	}
	// 稳定排序：让文件 diff 可读（map 迭代序随机）
	sort.Slice(f.Peers, func(i, j int) bool { return f.Peers[i].PeerID < f.Peers[j].PeerID })

	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	// storageDir 可能还没被创建（节点刚起、还没上传/拉取过任何文件），
	// 此时 WriteFile 会 ENOENT → Join 被判失败（内存里已加入，但重启即丢）。
	// 落盘前补一次 MkdirAll：这份清单很小，不该依赖存储目录已存在。
	if err := os.MkdirAll(filepath.Dir(d.path), 0o755); err != nil {
		return err
	}
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, d.path)
}

// Join 加入一个节点：校验 → 落盘 → 立即拨号。
func (d *NodeDirectory) Join(peerID string) error {
	peerID = strings.TrimSpace(peerID)
	if err := validatePeerID(peerID); err != nil {
		return err
	}
	if self := d.self(); self != "" && peerID == self {
		return fmt.Errorf("cannot join self")
	}
	d.mu.Lock()
	if _, ok := d.joined[peerID]; !ok {
		d.joined[peerID] = time.Now()
	}
	err := d.saveLocked()
	d.mu.Unlock()
	if err != nil {
		return fmt.Errorf("persist joined nodes: %w", err)
	}
	if d.dial != nil {
		// 异步：拨号可能要握手数秒，不该阻塞 HTTP 请求
		go d.dial(peerID)
	}
	log.LogInfo("node-directory: joined node %s", peerID)
	return nil
}

// Leave 移出一个节点。注意：不主动断开已有 WebRTC 连接——连接由发现/拨号
// 预算自然收敛（这条连接可能正在传输文件，硬断会让在途拉取失败）。
func (d *NodeDirectory) Leave(peerID string) error {
	peerID = strings.TrimSpace(peerID)
	if peerID == "" {
		return fmt.Errorf("peer is required")
	}
	d.mu.Lock()
	_, existed := d.joined[peerID]
	delete(d.joined, peerID)
	err := d.saveLocked()
	d.mu.Unlock()
	if err != nil {
		return fmt.Errorf("persist joined nodes: %w", err)
	}
	if !existed {
		return fmt.Errorf("peer %s is not joined", peerID)
	}
	log.LogInfo("node-directory: left node %s", peerID)
	return nil
}

// JoinedPeerIDs 已加入节点 id 列表（供 transport 在信令重连后自动拨号）。
func (d *NodeDirectory) JoinedPeerIDs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, 0, len(d.joined))
	for id := range d.joined {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// ---- 市场聚合 ----

// Market 汇总市场条目：发现服务器的在线节点 ∪ 已加入节点（离线也保留）。
// 发现服务器不可用不返回错误——已加入的节点仍要能显示，前端只多一个空态。
func (d *NodeDirectory) Market(ctx context.Context) []model.NodeSummary {
	self := d.self()
	conn := d.connectedMap()
	d.mu.Lock()
	joined := make(map[string]time.Time, len(d.joined))
	for id, at := range d.joined {
		joined[id] = at
	}
	d.mu.Unlock()

	byID := make(map[string]*model.NodeSummary)
	for _, n := range d.fetchOnline(ctx) {
		if n.PeerID == "" || n.PeerID == self {
			continue
		}
		s := &model.NodeSummary{
			PeerID:   n.PeerID,
			NodeType: n.NodeType,
			LastSeen: n.LastSeen,
			Uptime:   n.Uptime,
			Online:   true,
			Shares:   sharesFromLoadInfo(n.LoadInfo),
		}
		byID[n.PeerID] = s
	}
	// 已加入但当前不在发现列表（离线/发现服务器抽风）：补一条离线条目，
	// 否则用户会以为"我加的节点消失了"。
	for id, at := range joined {
		if id == self {
			continue
		}
		s, ok := byID[id]
		if !ok {
			joinedAt := at
			s = &model.NodeSummary{PeerID: id, JoinedAt: &joinedAt}
			byID[id] = s
		}
		if s.JoinedAt == nil {
			joinedAt := at
			s.JoinedAt = &joinedAt
		}
		s.Joined = true
	}

	out := make([]model.NodeSummary, 0, len(byID))
	for _, s := range byID {
		s.Connected = conn[s.PeerID]
		out = append(out, *s)
	}
	// 排序：直连 > 加入 > 在线，同级按 peerId 稳定排序（前端不跳动）
	sort.Slice(out, func(i, j int) bool {
		if out[i].Connected != out[j].Connected {
			return out[i].Connected
		}
		if out[i].Joined != out[j].Joined {
			return out[i].Joined
		}
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		return out[i].PeerID < out[j].PeerID
	})
	return out
}

// Joined 只列已加入节点（同样带在线/直连状态）。
func (d *NodeDirectory) Joined(ctx context.Context) []model.NodeSummary {
	all := d.Market(ctx)
	out := make([]model.NodeSummary, 0, len(all))
	for _, s := range all {
		if s.Joined {
			out = append(out, s)
		}
	}
	return out
}

// Self 返回本节点自身的市场条目（前端「我的节点」卡片用）。
func (d *NodeDirectory) Self(ctx context.Context) model.NodeSummary {
	s := model.NodeSummary{PeerID: d.self(), Self: true, Online: true, Connected: true}
	// 自己的共享摘要本地可算，不需要走发现服务器
	if d.shareSummary != nil {
		s.Shares = d.shareSummary()
	}
	return s
}

// SetShareSummary 注入本节点共享摘要读取器（M2 的 NodeShare 服务）。
// 用注入而不是直接依赖：service 内部避免新增包间耦合，也便于单测。
func (d *NodeDirectory) SetShareSummary(fn func() model.NodeShares) { d.shareSummary = fn }

// fetchOnline 查询发现服务器的在线节点。
//
// 查询策略（两层兜底）：
//  1. 不带 coll 的查询——signalserver 语义是"返回所有房间的去重节点"，
//     正是市场要的"所有在线节点"。
//  2. 第 1 步拿不到任何节点时再按「存在房间」查一次——线上信令由外部维护，
//     不能假设它支持空 coll 查询（不支持时会返回空列表而不是报错）。
func (d *NodeDirectory) fetchOnline(ctx context.Context) []discoveredNode {
	if d.discoverURL == "" {
		return nil
	}
	nodes := d.getNodes(ctx, "")
	if len(nodes) == 0 {
		nodes = d.getNodes(ctx, transport.PresenceRoom)
	}
	return nodes
}

func (d *NodeDirectory) getNodes(ctx context.Context, coll string) []discoveredNode {
	url := d.discoverURL + "/discover/nodes"
	if coll != "" {
		url += "?coll=" + coll
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	resp, err := d.http.Do(req)
	if err != nil {
		log.LogDebug("node-directory: discover query failed: %v", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.LogDebug("node-directory: discover status %d", resp.StatusCode)
		return nil
	}
	var out struct {
		Nodes []discoveredNode `json:"nodes"`
	}
	// 限 1MB：发现服务器被攻破时回巨大 JSON 不整包入内存（与 HTTPDiscovery 同思路）
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		log.LogDebug("node-directory: discover decode failed: %v", err)
		return nil
	}
	return out.Nodes
}

func (d *NodeDirectory) self() string {
	if d.selfID == nil {
		return ""
	}
	return d.selfID()
}

func (d *NodeDirectory) connectedMap() map[string]bool {
	if d.connected == nil {
		return nil
	}
	return d.connected()
}

// sharesFromLoadInfo 从 announce 的 loadInfo 里取共享摘要。
// 容错：外部节点可能不上报，或上报成字符串/浮点（JSON 数字解出来是 float64）。
func sharesFromLoadInfo(li map[string]any) model.NodeShares {
	if li == nil {
		return model.NodeShares{}
	}
	raw, ok := li["shares"]
	if !ok {
		return model.NodeShares{}
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return model.NodeShares{}
	}
	return model.NodeShares{
		Collections: asInt(m["collections"]),
		Files:       asInt(m["files"]),
		Dirs:        asInt(m["dirs"]),
	}
}

func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

// validatePeerID 节点 id 校验：非空、限长、无空白/控制字符。
// 为什么要校验：peerId 会进磁盘（joined_nodes.json）与信令查询参数，
// 放任换行/超长串会污染清单文件与日志。
func validatePeerID(peerID string) error {
	if peerID == "" {
		return fmt.Errorf("peer is required")
	}
	if len(peerID) > 128 {
		return fmt.Errorf("peer id too long")
	}
	for _, r := range peerID {
		if r <= 0x20 || r == 0x7f {
			return fmt.Errorf("peer id contains invalid character")
		}
	}
	return nil
}
