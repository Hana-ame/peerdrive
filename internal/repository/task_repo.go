// 任务仓库 — transfer_tasks 表的异步任务 CRUD 操作。
// 函数列表：
//   CreateTask(type, params)    — 创建新任务，状态初始为 "pending"
//   UpdateTaskStatus(id,status,result) — 更新任务状态和结果（自动更新 updated_at）
//   GetTask(id)                 — 按 ID 查询任务详情，未找到返回 (nil, nil)

package repository

import (
	"database/sql"
	"peerdrive/internal/model"
)

func CreateTask(taskType, params string) (int, error) {
	res, err := DB.Exec(`INSERT INTO transfer_tasks (type, status, params) VALUES (?, 'pending', ?)`, taskType, params)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return int(id), nil
}

func UpdateTaskStatus(id int, status, result string) error {
	_, err := DB.Exec(`UPDATE transfer_tasks SET status = ?, result = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, status, result, id)
	return err
}

func GetTask(id int) (*model.TransferTask, error) {
	var t model.TransferTask
	err := DB.QueryRow(`SELECT id, type, status, params, result, created_at, updated_at FROM transfer_tasks WHERE id = ?`, id).
		Scan(&t.ID, &t.Type, &t.Status, &t.Params, &t.Result, &t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}
