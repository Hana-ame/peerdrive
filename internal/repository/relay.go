package repository

import (
	"database/sql"
	"encoding/json"

	"peerdrive-registration/internal/model"
)

type RelayRepository struct {
	db *sql.DB
}

func NewRelayRepository(db *sql.DB) *RelayRepository {
	return &RelayRepository{db: db}
}

func (r *RelayRepository) InitSchema() error {
	_, err := r.db.Exec(`
		CREATE TABLE IF NOT EXISTS relay_nodes (
			peer_id TEXT PRIMARY KEY,
			addrs TEXT NOT NULL,
			storage_mb INTEGER DEFAULT 0,
			load_pct REAL DEFAULT 0,
			version TEXT DEFAULT '',
			operator_username TEXT DEFAULT '',
			registered_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_heartbeat DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

func (r *RelayRepository) Upsert(node model.RelayNode) error {
	addrsJSON, err := json.Marshal(node.Addrs)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(`
		INSERT INTO relay_nodes (peer_id, addrs, storage_mb, version)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(peer_id) DO UPDATE SET
			addrs = excluded.addrs,
			storage_mb = excluded.storage_mb,
			version = excluded.version,
			last_heartbeat = CURRENT_TIMESTAMP
	`, node.PeerID, string(addrsJSON), node.StorageMB, node.Version)
	return err
}

func (r *RelayRepository) ListActive() ([]model.RelayNode, error) {
	rows, err := r.db.Query(`
		SELECT peer_id, addrs, storage_mb, load_pct, version, registered_at, last_heartbeat
		FROM relay_nodes
		WHERE last_heartbeat >= datetime('now', '-5 minutes')
		ORDER BY last_heartbeat DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var relays []model.RelayNode
	for rows.Next() {
		var node model.RelayNode
		var addrsJSON string
		if err := rows.Scan(&node.PeerID, &addrsJSON, &node.StorageMB, &node.LoadPct, &node.Version, &node.RegisteredAt, &node.LastHeartbeat); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(addrsJSON), &node.Addrs)
		relays = append(relays, node)
	}
	return relays, nil
}

func (r *RelayRepository) UpdateHeartbeat(peerID string, loadPct float64) error {
	_, err := r.db.Exec(`
		UPDATE relay_nodes
		SET load_pct = ?, last_heartbeat = CURRENT_TIMESTAMP
		WHERE peer_id = ?
	`, loadPct, peerID)
	return err
}

// CountActive returns the number of relays with heartbeat in the last 5 minutes.
func (r *RelayRepository) CountActive() (int, error) {
	var count int
	err := r.db.QueryRow(`
		SELECT COUNT(*) FROM relay_nodes
		WHERE last_heartbeat >= datetime('now', '-5 minutes')
	`).Scan(&count)
	return count, err
}

// UpsertWithOperator upserts a relay node and links it to an operator account.
func (r *RelayRepository) UpsertWithOperator(node model.RelayNode, operatorUsername string) error {
	addrsJSON, err := json.Marshal(node.Addrs)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(`
		INSERT INTO relay_nodes (peer_id, addrs, storage_mb, version, operator_username)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(peer_id) DO UPDATE SET
			addrs = excluded.addrs,
			storage_mb = excluded.storage_mb,
			version = excluded.version,
			operator_username = excluded.operator_username,
			last_heartbeat = CURRENT_TIMESTAMP
	`, node.PeerID, string(addrsJSON), node.StorageMB, node.Version, operatorUsername)
	return err
}

// SetOperator links an existing relay node to a user account.
func (r *RelayRepository) SetOperator(peerID, username string) error {
	_, err := r.db.Exec(`
		UPDATE relay_nodes SET operator_username = ? WHERE peer_id = ?
	`, username, peerID)
	return err
}

// GetByPeerID returns a single relay node by peer ID with operator info.
func (r *RelayRepository) GetByPeerID(peerID string) (*model.RelayNodeDetail, error) {
	node := &model.RelayNodeDetail{}
	var addrsJSON string
	err := r.db.QueryRow(`
		SELECT peer_id, addrs, storage_mb, load_pct, version,
			COALESCE(operator_username, ''), registered_at, last_heartbeat
		FROM relay_nodes WHERE peer_id = ?
	`, peerID).Scan(&node.PeerID, &addrsJSON, &node.StorageMB, &node.LoadPct,
		&node.Version, &node.OperatorUsername, &node.RegisteredAt, &node.LastHeartbeat)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(addrsJSON), &node.Addrs)
	return node, nil
}

// ListByOperator returns all relay nodes operated by a given user.
func (r *RelayRepository) ListByOperator(username string) ([]model.RelayNodeDetail, error) {
	rows, err := r.db.Query(`
		SELECT peer_id, addrs, storage_mb, load_pct, version,
			COALESCE(operator_username, ''), registered_at, last_heartbeat
		FROM relay_nodes
		WHERE operator_username = ?
		ORDER BY last_heartbeat DESC
	`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []model.RelayNodeDetail
	for rows.Next() {
		var node model.RelayNodeDetail
		var addrsJSON string
		if err := rows.Scan(&node.PeerID, &addrsJSON, &node.StorageMB, &node.LoadPct,
			&node.Version, &node.OperatorUsername, &node.RegisteredAt, &node.LastHeartbeat); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(addrsJSON), &node.Addrs)
		nodes = append(nodes, node)
	}
	return nodes, nil
}
