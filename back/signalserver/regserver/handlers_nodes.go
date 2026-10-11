package regserver

// handlers_nodes.go — 用户与节点名录（/nodes/* 与 /users/:username/nodes）HTTP handler。
//
// 对应 doc/modules/auth/API-DESIGN.md 第 6 节：
//   - POST /nodes/register       节点带用户凭证登记（幂等 upsert，防占坑校验）
//   - POST /nodes/heartbeat      心跳刷新 last_seen
//   - DELETE /nodes/{peer_id}    注销节点（需属主或 admin）
//   - GET /nodes/{peer_id}       查询节点信息与属主（公开/需认证/私有三级可见性）
//   - GET /users/{username}/nodes 查指定账号名下的在线节点清单
//
// 与 handlers_relay.go（/p2p/relay/*）的严格区别：
//   - relay 是中继节点匿名登记（无账号、只记 addrs/storage/load）；
//   - 本文件是节点全量身份目录，与运营者账号强绑定。

import (
	"encoding/json"
	"net/http"
	"strings"
)

// optionalAuth 从 Authorization 头提取已验证的身份（若有）。
// 未携带或非法时返回零值，不打断请求流程（供公开/分级可见端点使用）。
func (s *Server) optionalAuth(r *http.Request) (string, string) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", ""
	}
	user, role, err := s.verifyToken(parts[1])
	if err != nil {
		return "", ""
	}
	return user, role
}

// nodeRegister 登记或更新节点（需 Bearer 认证）。
func (s *Server) nodeRegister(w http.ResponseWriter, r *http.Request) {
	info := authInfoOf(r)
	if info.username == "" {
		writeErr(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req struct {
		PeerID       string `json:"peer_id"`
		Endpoints    any    `json:"endpoints"`
		Capabilities any    `json:"capabilities"`
		Version      string `json:"version"`
		Visibility   string `json:"visibility"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	peerID := strings.TrimSpace(req.PeerID)
	if peerID == "" {
		writeErr(w, http.StatusBadRequest, "peer_id is required")
		return
	}

	vis := strings.ToLower(strings.TrimSpace(req.Visibility))
	if vis == "" {
		vis = "public"
	}
	if vis != "public" && vis != "registered" && vis != "private" {
		writeErr(w, http.StatusBadRequest, "invalid visibility; must be public, registered, or private")
		return
	}

	// 防占坑检查（API-DESIGN.md §6.3 #2）：
	// 若 peer_id 已被其他账号登记，且仍在活跃期（5分钟内有心跳），拒绝抢占（409 Conflict）。
	// 若属于同一账号或已超时过期，允许更新/换绑。
	var existingUser string
	var isActive int
	err := s.db.QueryRow(`SELECT username, (last_seen >= datetime('now', '-5 minutes')) FROM user_nodes WHERE peer_id = ?`, peerID).Scan(&existingUser, &isActive)
	if err == nil {
		if existingUser != info.username && isActive == 1 {
			writeErr(w, http.StatusConflict, "peer_id is registered to another active user")
			return
		}
	}

	endpointsStr := stringifyJSONField(req.Endpoints, "[]")
	capsStr := stringifyJSONField(req.Capabilities, "{}")

	if _, err := s.db.Exec(`INSERT INTO user_nodes(peer_id, username, endpoints, capabilities, version, visibility, last_seen)
		VALUES(?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(peer_id) DO UPDATE SET
			username=excluded.username,
			endpoints=excluded.endpoints,
			capabilities=excluded.capabilities,
			version=excluded.version,
			visibility=excluded.visibility,
			last_seen=CURRENT_TIMESTAMP`,
		peerID, info.username, endpointsStr, capsStr, req.Version, vis); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "registered", "peer_id": peerID})
}

// nodeHeartbeat 刷新节点心跳（更新 last_seen）。
func (s *Server) nodeHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PeerID string `json:"peer_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	peerID := strings.TrimSpace(req.PeerID)
	if peerID == "" {
		writeErr(w, http.StatusBadRequest, "peer_id is required")
		return
	}

	_, _ = s.db.Exec(`UPDATE user_nodes SET last_seen = CURRENT_TIMESTAMP WHERE peer_id = ?`, peerID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// nodeDelete 注销节点（需 Bearer 认证，仅属主或 admin 可执行）。
func (s *Server) nodeDelete(w http.ResponseWriter, r *http.Request) {
	info := authInfoOf(r)
	if info.username == "" {
		writeErr(w, http.StatusUnauthorized, "authentication required")
		return
	}

	peerID := strings.TrimSpace(r.PathValue("peer_id"))
	if peerID == "" {
		writeErr(w, http.StatusBadRequest, "peer_id is required")
		return
	}

	var owner string
	if err := s.db.QueryRow(`SELECT username FROM user_nodes WHERE peer_id = ?`, peerID).Scan(&owner); err != nil {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}

	if info.username != owner && info.role != "admin" {
		writeErr(w, http.StatusForbidden, "not authorized to delete this node")
		return
	}

	_, _ = s.db.Exec(`DELETE FROM user_nodes WHERE peer_id = ?`, peerID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// nodeGet 查询指定节点的信息与属主。
func (s *Server) nodeGet(w http.ResponseWriter, r *http.Request) {
	peerID := strings.TrimSpace(r.PathValue("peer_id"))
	if peerID == "" {
		writeErr(w, http.StatusBadRequest, "peer_id is required")
		return
	}

	caller, role := s.optionalAuth(r)

	var (
		nPeerID, username, endpoints, caps, version, visibility, regAt, lastSeen string
		isActive int
	)
	err := s.db.QueryRow(`SELECT peer_id, username, endpoints, capabilities, version, visibility, registered_at, last_seen, (last_seen >= datetime('now', '-5 minutes'))
		FROM user_nodes WHERE peer_id = ?`, peerID).Scan(&nPeerID, &username, &endpoints, &caps, &version, &visibility, &regAt, &lastSeen, &isActive)
	if err != nil {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}

	switch visibility {
	case "private":
		if caller == "" || (caller != username && role != "admin") {
			writeErr(w, http.StatusNotFound, "node not found")
			return
		}
	case "registered":
		if caller == "" {
			writeErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
	}

	type nodeResp struct {
		PeerID       string `json:"peer_id"`
		Username     string `json:"username"`
		Endpoints    any    `json:"endpoints"`
		Capabilities any    `json:"capabilities"`
		Version      string `json:"version"`
		Visibility   string `json:"visibility"`
		RegisteredAt string `json:"registered_at"`
		LastSeen     string `json:"last_seen"`
		Online       bool   `json:"online"`
	}

	writeJSON(w, http.StatusOK, nodeResp{
		PeerID:       nPeerID,
		Username:     username,
		Endpoints:    parseJSONField(endpoints, []string{}),
		Capabilities: parseJSONField(caps, map[string]any{}),
		Version:      version,
		Visibility:   visibility,
		RegisteredAt: regAt,
		LastSeen:     lastSeen,
		Online:       isActive == 1,
	})
}

// userNodesGet 查询指定用户拥有的在线节点清单。
func (s *Server) userNodesGet(w http.ResponseWriter, r *http.Request) {
	targetUser := strings.TrimSpace(r.PathValue("username"))
	if targetUser == "" {
		writeErr(w, http.StatusBadRequest, "username is required")
		return
	}

	caller, role := s.optionalAuth(r)

	rows, err := s.db.Query(`SELECT peer_id, username, endpoints, capabilities, version, visibility, registered_at, last_seen
		FROM user_nodes
		WHERE username = ? AND last_seen >= datetime('now', '-5 minutes')
		ORDER BY registered_at ASC`, targetUser)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type nodeSummary struct {
		PeerID       string `json:"peer_id"`
		Username     string `json:"username"`
		Endpoints    any    `json:"endpoints"`
		Capabilities any    `json:"capabilities"`
		Version      string `json:"version"`
		Visibility   string `json:"visibility"`
		RegisteredAt string `json:"registered_at"`
		LastSeen     string `json:"last_seen"`
		Online       bool   `json:"online"`
	}

	nodes := []nodeSummary{}
	for rows.Next() {
		var n nodeSummary
		var endpoints, caps string
		if err := rows.Scan(&n.PeerID, &n.Username, &endpoints, &caps, &n.Version, &n.Visibility, &n.RegisteredAt, &n.LastSeen); err != nil {
			continue
		}
		if n.Visibility == "private" && (caller == "" || (caller != targetUser && role != "admin")) {
			continue
		}
		if n.Visibility == "registered" && caller == "" {
			continue
		}
		n.Endpoints = parseJSONField(endpoints, []string{})
		n.Capabilities = parseJSONField(caps, map[string]any{})
		n.Online = true
		nodes = append(nodes, n)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"username": targetUser,
		"nodes":    nodes,
	})
}

func stringifyJSONField(v any, fallback string) string {
	if v == nil {
		return fallback
	}
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if s == "" {
			return fallback
		}
		if json.Valid([]byte(s)) {
			return s
		}
		b, _ := json.Marshal(s)
		return string(b)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fallback
	}
	return string(b)
}

func parseJSONField(raw string, fallback any) any {
	if raw == "" {
		return fallback
	}
	var out any
	if err := json.Unmarshal([]byte(raw), &out); err == nil {
		return out
	}
	return raw
}
