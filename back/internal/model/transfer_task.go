// Package model 定义 Peerdrive 系统的异步任务数据结构。
// TransferTask（transfer_tasks 表）：跟踪异步操作（pull、merge 等），
//   支持状态轮询（pending/completed/failed），params 和 result 为 JSON 字符串。

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
