package model

import "time"

type UserRole string

const (
	RoleUser  UserRole = "user"
	RoleAdmin UserRole = "admin"
)

type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         UserRole  `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=3,max=32"`
	Password string `json:"password" binding:"required,min=6"`
	Role     string `json:"role"`        // optional, defaults to "user"
}

type ListResponse struct {
	Users []User `json:"users"`
	Total int    `json:"total"`
}

type RelayNode struct {
	PeerID        string    `json:"peer_id"`
	Addrs         []string  `json:"addrs"`
	StorageMB     int       `json:"storage_mb"`
	LoadPct       float64   `json:"load_pct"`
	Version       string    `json:"version"`
	RegisteredAt  time.Time `json:"registered_at"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
}

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type TokenResponse struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// Group represents a named group that users can belong to.
type Group struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// UserGroup is a join between users and groups.
type UserGroup struct {
	UserID    int64     `json:"user_id"`
	GroupID   int64     `json:"group_id"`
	Username  string    `json:"username"`
	GroupName string    `json:"group_name"`
	CreatedAt time.Time `json:"created_at"`
}

// Comment represents a user comment on a collection (identified by hash).
type Comment struct {
	ID        int64     `json:"id"`
	Hash      string    `json:"hash"`
	Username  string    `json:"username"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// Stats holds aggregate registration server statistics.
type Stats struct {
	TotalUsers    int `json:"total_users"`
	ActiveRelays  int `json:"active_relays"`
	TotalComments int `json:"total_comments"`
}

// ServicePolicy controls whether a user is allowed to use relay and/or P2P services.
type ServicePolicy struct {
	Username   string `json:"username"`
	AllowRelay bool   `json:"allow_relay"`
	AllowP2P   bool   `json:"allow_p2p"`
	Notes      string `json:"notes,omitempty"`
}

// UserStorage tracks per-user file storage usage on the registration server.
type UserStorage struct {
	Username   string `json:"username"`
	UsedBytes  int64  `json:"used_bytes"`
	LimitBytes int64  `json:"limit_bytes"`
}

// GroupDetail includes group metadata plus member list.
type GroupDetail struct {
	ID          int64       `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
	Members     []string    `json:"members"`
	MemberCount int         `json:"member_count"`
}

// RelayNodeDetail extends RelayNode with operator username.
type RelayNodeDetail struct {
	PeerID           string    `json:"peer_id"`
	Addrs            []string  `json:"addrs"`
	StorageMB        int       `json:"storage_mb"`
	LoadPct          float64   `json:"load_pct"`
	Version          string    `json:"version"`
	OperatorUsername string    `json:"operator_username,omitempty"`
	RegisteredAt     time.Time `json:"registered_at"`
	LastHeartbeat    time.Time `json:"last_heartbeat"`
}

// PeerNode represents a Peerdrive node registered by an authenticated user.
// Anonymous nodes are NOT registered here — registration is optional.
type PeerNode struct {
	PeerID      string   `json:"peer_id"`
	Username    string   `json:"username"`
	Addrs       []string `json:"addrs"`
	Version     string   `json:"version"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
}

// PeerNodeStats holds transfer statistics reported by a node.
type PeerNodeStats struct {
	PeerID        string `json:"peer_id"`
	UploadBytes   int64  `json:"upload_bytes"`   // delta since last report
	DownloadBytes int64  `json:"download_bytes"` // delta since last report
}

// UserNodeInfo is returned for node operator queries.
type UserNodeInfo struct {
	PeerID        string    `json:"peer_id"`
	Username      string    `json:"username"`
	Addrs         []string  `json:"addrs"`
	Version       string    `json:"version"`
	FirstSeen     time.Time `json:"first_seen"`
	LastSeen      time.Time `json:"last_seen"`
	TotalUpload   int64     `json:"total_upload_bytes"`
	TotalDownload int64     `json:"total_download_bytes"`
}

// StatsExt extends Stats with node count.
type StatsExt struct {
	TotalUsers    int `json:"total_users"`
	ActiveRelays  int `json:"active_relays"`
	ActiveNodes   int `json:"active_nodes"`
	TotalComments int `json:"total_comments"`
}
