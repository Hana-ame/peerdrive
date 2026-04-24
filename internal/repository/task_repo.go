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
