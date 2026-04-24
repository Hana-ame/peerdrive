// Package model defines data structures for the Peerdrive system.
// TransferTask: tracks async operations (pull, merge, etc.) with status polling.

package model

type TransferTask struct {
	ID        int    `db:"id"`
	Type      string `db:"type"`
	Status    string `db:"status"`
	Params    string `db:"params"`
	Result    string `db:"result"`
	CreatedAt string `db:"created_at"`
	UpdatedAt string `db:"updated_at"`
}
