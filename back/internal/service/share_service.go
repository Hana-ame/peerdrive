// ShareService 分享链接用例层（M2 收层：share 控制器此前直调 repository）。
package service

import (
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
)

type ShareService struct{}

func NewShareService() *ShareService { return &ShareService{} }

// Create 创建 30 天有效期的分享链接。
func (s *ShareService) Create(hash, shareType, filename string) (*model.ShareLink, error) {
	return repository.CreateShare(hash, shareType, filename)
}

// GetByToken 按 token 查询未过期分享。
func (s *ShareService) GetByToken(token string) (*model.ShareLink, error) {
	return repository.GetShareByToken(token)
}

// List 列出全部未过期分享（最多 100 条）。
func (s *ShareService) List() ([]model.ShareLink, error) {
	return repository.ListShares()
}