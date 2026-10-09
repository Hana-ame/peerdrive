package regserver

// handlers_relay.go — 中继节点登记（/p2p/relay/*）的 HTTP handler。
//
// 与 handlers_auth.go 对称：两个业务线各自的 HTTP 入口。relay 表只有这一个，
// 三个端点（register / heartbeat / list）共享。
//
// 安全注意：这三个端点**无认证**（既有对外行为，不能改）。伪造 peer_id 能污染
// relay_nodes 表，但 relay 表的消费方（discover API）本就把注册节点视为「自荐」，
// 故速率限制（Handler 装配处）是唯一防线。
//
// 与 service.NodeDirectory 的边界：NodeDirectory 是**本节点**维护的「我加入了
// 谁」清单（client 侧、joined_nodes.json）；relay 表是**中心服务器**维护的
// 「所有注册的中继节点」（server 侧、SQLite）。两者数据流向相反、消费方不同，
// 不合并。

import (
	"encoding/json"
	"net/http"
	"strings"
)

// relayRegister 登记或更新一个中继节点（按 peer_id 幂等 upsert）。
//
// 幂等是设计选择：节点重启后重新登记不应该报冲突，而是刷新地址/存储/负载。
// 顺带刷新 last_heartbeat（见 ON CONFLICT 子句），故 register 与 heartbeat
// 共用同一份「活跃」语义。
//
// addrs 是 any 而不是 []string：客户端可能发数组（["1.2.3.4:1"]）也可能发
// 单字符串（"1.2.3.4:1"），stringifyAddrs 统一规整成字符串入库。
func (s *Server) relayRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PeerID    string  `json:"peer_id"`
		Addrs     any     `json:"addrs"`
		StorageMB int64   `json:"storage_mb"`
		LoadPct   float64 `json:"load_pct"`
		Version   string  `json:"version"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if strings.TrimSpace(req.PeerID) == "" {
		writeErr(w, http.StatusBadRequest, "peer_id is required")
		return
	}
	addrs := stringifyAddrs(req.Addrs)
	if _, err := s.db.Exec(`INSERT INTO relay_nodes(peer_id, addrs, storage_mb, load_pct, version)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(peer_id) DO UPDATE SET
			addrs=excluded.addrs, storage_mb=excluded.storage_mb,
			load_pct=excluded.load_pct, version=excluded.version,
			last_heartbeat=CURRENT_TIMESTAMP`,
		req.PeerID, addrs, req.StorageMB, req.LoadPct, req.Version); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "registered"})
}

// relayHeartbeat 刷新一个已登记节点的心跳时间。
//
// 不存在的 peer_id 静默忽略（UPDATE 0 行不报错）：节点可能刚下线就被
// discover API 查到一次，heartbeat 不应让调用方知道节点是否还在。
// 错误一律忽略——心跳失败不应影响调用方的健康判定。
func (s *Server) relayHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PeerID string `json:"peer_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.PeerID == "" {
		writeErr(w, http.StatusBadRequest, "peer_id is required")
		return
	}
	_, _ = s.db.Exec(`UPDATE relay_nodes SET last_heartbeat = CURRENT_TIMESTAMP WHERE peer_id = ?`, req.PeerID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// relayList 列出所有已登记的中继节点。
//
// addrs 列在库里存的是 stringifyAddrs 后的字符串，parseAddrs 还原成 JSON 形态
// （数组则回数组，否则原样字符串）。前端两种都能读。
func (s *Server) relayList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`SELECT peer_id, addrs, storage_mb, load_pct, version, registered_at, last_heartbeat FROM relay_nodes`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	type node struct {
		PeerID        string  `json:"peer_id"`
		Addrs         any     `json:"addrs"`
		StorageMB     int64   `json:"storage_mb"`
		LoadPct       float64 `json:"load_pct"`
		Version       string  `json:"version"`
		RegisteredAt  string  `json:"registered_at"`
		LastHeartbeat string  `json:"last_heartbeat"`
	}
	out := []node{}
	for rows.Next() {
		var n node
		var addrs string
		if rows.Scan(&n.PeerID, &addrs, &n.StorageMB, &n.LoadPct, &n.Version, &n.RegisteredAt, &n.LastHeartbeat) != nil {
			continue
		}
		n.Addrs = parseAddrs(addrs)
		out = append(out, n)
	}
	writeJSON(w, http.StatusOK, map[string]any{"relays": out})
}

// stringifyAddrs 把 addrs 规整成入库用的字符串（数组则 JSON 编码）。纯函数。
//
// 为什么入库用字符串而不是 TEXT[]：SQLite 没有原生数组类型，JSON 编码是最
// 简单且向前兼容的形态（以后要加更多字段直接改 JSON schema 即可）。
func stringifyAddrs(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if b, err := json.Marshal(v); err == nil {
		return string(b)
	}
	return ""
}

// parseAddrs 把库里存的 addrs 还原成 JSON 形态：数组则回数组，否则原样字符串。
// 纯函数，不依赖 Server（参数名曾与 receiver 冲突，故不做方法）。
//
// 容错设计：库里若存了非法 JSON 或意外类型，parseAddrs 退回原字符串——
// 不让单条坏数据让整个 list 端点报错。
func parseAddrs(raw string) any {
	if raw == "" {
		return nil
	}
	var arr []string
	if json.Unmarshal([]byte(raw), &arr) == nil {
		return arr
	}
	return raw
}
