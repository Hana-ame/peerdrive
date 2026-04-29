package repository

import (
	"database/sql"
	"encoding/json"
	"time"

	"peerdrive-registration/internal/model"
)

type NodeRepository struct {
	db *sql.DB
}

func NewNodeRepository(db *sql.DB) *NodeRepository {
	return &NodeRepository{db: db}
}

func (r *NodeRepository) InitSchema() error {
	_, err := r.db.Exec(`
		CREATE TABLE IF NOT EXISTS peer_nodes (
			peer_id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			addrs TEXT NOT NULL DEFAULT '',
			version TEXT DEFAULT '',
			total_upload_bytes INTEGER NOT NULL DEFAULT 0,
			total_download_bytes INTEGER NOT NULL DEFAULT 0,
			first_seen DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_seen DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (username) REFERENCES users(username)
		)
	`)
	return err
}

// Upsert registers or updates a node bound to a user.
func (r *NodeRepository) Upsert(node model.PeerNode) error {
	addrsJSON, _ := json.Marshal(node.Addrs)
	_, err := r.db.Exec(`
		INSERT INTO peer_nodes (peer_id, username, addrs, version, first_seen, last_seen)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(peer_id) DO UPDATE SET
			username = excluded.username,
			addrs = excluded.addrs,
			version = excluded.version,
			last_seen = CURRENT_TIMESTAMP
	`, node.PeerID, node.Username, string(addrsJSON), node.Version)
	return err
}

// Heartbeat updates last_seen for a node (no username change).
func (r *NodeRepository) Heartbeat(peerID string) error {
	_, err := r.db.Exec(`
		UPDATE peer_nodes SET last_seen = CURRENT_TIMESTAMP WHERE peer_id = ?
	`, peerID)
	return err
}

// GetByPeerID returns a node by peer ID, or nil if not registered.
func (r *NodeRepository) GetByPeerID(peerID string) (*model.UserNodeInfo, error) {
	info := &model.UserNodeInfo{}
	var addrsJSON string
	err := r.db.QueryRow(`
		SELECT peer_id, username, addrs, version, first_seen, last_seen,
			total_upload_bytes, total_download_bytes
		FROM peer_nodes WHERE peer_id = ?
	`, peerID).Scan(&info.PeerID, &info.Username, &addrsJSON, &info.Version,
		&info.FirstSeen, &info.LastSeen, &info.TotalUpload, &info.TotalDownload)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(addrsJSON), &info.Addrs)
	return info, nil
}

// ListByUsername returns all nodes registered by a user.
func (r *NodeRepository) ListByUsername(username string) ([]model.UserNodeInfo, error) {
	rows, err := r.db.Query(`
		SELECT peer_id, username, addrs, version, first_seen, last_seen,
			total_upload_bytes, total_download_bytes
		FROM peer_nodes WHERE username = ?
		ORDER BY last_seen DESC
	`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []model.UserNodeInfo
	for rows.Next() {
		var info model.UserNodeInfo
		var addrsJSON string
		if err := rows.Scan(&info.PeerID, &info.Username, &addrsJSON, &info.Version,
			&info.FirstSeen, &info.LastSeen, &info.TotalUpload, &info.TotalDownload); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(addrsJSON), &info.Addrs)
		nodes = append(nodes, info)
	}
	return nodes, nil
}

// AddTransferStats adds upload/download delta to a node's totals.
func (r *NodeRepository) AddTransferStats(peerID string, uploadDelta, downloadDelta int64) error {
	_, err := r.db.Exec(`
		UPDATE peer_nodes SET
			total_upload_bytes = total_upload_bytes + ?,
			total_download_bytes = total_download_bytes + ?,
			last_seen = CURRENT_TIMESTAMP
		WHERE peer_id = ?
	`, uploadDelta, downloadDelta, peerID)
	return err
}

// CountActive returns the count of nodes seen in the last 5 minutes.
func (r *NodeRepository) CountActive() (int, error) {
	var count int
	err := r.db.QueryRow(`
		SELECT COUNT(*) FROM peer_nodes
		WHERE last_seen >= datetime('now', '-5 minutes')
	`).Scan(&count)
	return count, err
}

// CountAll returns total registered nodes.
func (r *NodeRepository) CountAll() (int, error) {
	var count int
	err := r.db.QueryRow("SELECT COUNT(*) FROM peer_nodes").Scan(&count)
	return count, err
}

// ensure time is used
var _ = time.Now
