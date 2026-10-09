// Package repository — ipfs_pins table CRUD.
// Tracks CIDs that have been pinned (permanently cached) from IPFS gateways.

package repository

import (
	"database/sql"

	"peerdrive/internal/model"
)

// IPFSPin has been moved to the model package (M2 layering: domain types belong to domain).
// Alias retained here to keep references unchanged.

// InsertPin inserts a new pin record (or replaces an existing one).
func InsertPin(cid, hash, filename string, size int64) error {
	_, err := db.Exec(
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
// M11: No LIMIT → full-table materialization with many pins; pin count has no business cap, add LIMIT as backstop.
func ListPins() ([]model.IPFSPin, error) {
	rows, err := db.Query(
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
	err := db.QueryRow(
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
	_, err := db.Exec(`DELETE FROM ipfs_pins WHERE cid = ?`, cid)
	return err
}

// PinExists returns true if the given CID is already pinned.
func PinExists(cid string) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM ipfs_pins WHERE cid = ?`, cid).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
