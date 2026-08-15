// Package repository — ipfs_pins table CRUD.
// Tracks CIDs that have been pinned (permanently cached) from IPFS gateways.

package repository

import (
	"database/sql"

	"peerdrive/internal/model"
)

// IPFSPin 已上移 model 包（M2 收层：领域类型归 domain）。此处用别名保持引用不变。

// InsertPin inserts a new pin record (or replaces an existing one).
func InsertPin(cid, hash, filename string, size int64) error {
	_, err := DB.Exec(
		`INSERT INTO ipfs_pins (cid, hash, size, filename, pinned_at)
		 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(cid) DO UPDATE SET
		   hash=excluded.hash,
		   size=excluded.size,
		   filename=excluded.filename,
		   pinned_at=CURRENT_TIMESTAMP`,
		cid, hash, size, filename,
	)
	return err
}

// ListPins returns all pinned CIDs, most recently pinned first.
// M11：无 LIMIT → 大量 pin 时全表物化；pin 数无业务上限，加 LIMIT 兜底。
func ListPins() ([]model.IPFSPin, error) {
	rows, err := DB.Query(
		`SELECT cid, hash, size, filename, pinned_at
		 FROM ipfs_pins ORDER BY pinned_at DESC LIMIT 1000`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pins []model.IPFSPin
	for rows.Next() {
		var p model.IPFSPin
		if err := rows.Scan(&p.CID, &p.Hash, &p.Size, &p.Filename, &p.PinnedAt); err != nil {
			return nil, err
		}
		pins = append(pins, p)
	}
	return pins, nil
}

// GetPin retrieves a single pin by CID.
func GetPin(cid string) (*model.IPFSPin, error) {
	var p model.IPFSPin
	err := DB.QueryRow(
		`SELECT cid, hash, size, filename, pinned_at
		 FROM ipfs_pins WHERE cid = ?`, cid,
	).Scan(&p.CID, &p.Hash, &p.Size, &p.Filename, &p.PinnedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// RemovePin deletes a pin record by CID.
func RemovePin(cid string) error {
	_, err := DB.Exec(`DELETE FROM ipfs_pins WHERE cid = ?`, cid)
	return err
}

// PinExists returns true if the given CID is already pinned.
func PinExists(cid string) (bool, error) {
	var count int
	err := DB.QueryRow(`SELECT COUNT(*) FROM ipfs_pins WHERE cid = ?`, cid).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
