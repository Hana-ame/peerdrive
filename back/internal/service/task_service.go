// TaskService 异步任务用例层（M2 收层：task/fork 控制器此前直调 repository）。
// 注意：任务系统目前只是占位（fork 的 pull 是 no-op），保留结构待真正异步化。
package service

import (
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
)

type TaskService struct{}

func NewTaskService() *TaskService { return &TaskService{} }

func (s *TaskService) Create(taskType, params string) (int, error) {
	return repository.CreateTask(taskType, params)
}

func (s *TaskService) UpdateStatus(id int, status, result string) error {
	return repository.UpdateTaskStatus(id, status, result)
}

func (s *TaskService) Get(id int) (*model.TransferTask, error) {
	return repository.GetTask(id)
}