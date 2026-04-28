package service

import (
	"peerdrive-registration/internal/model"
	"peerdrive-registration/internal/repository"
)

type CommentService struct {
	repo *repository.CommentRepository
}

func NewCommentService(repo *repository.CommentRepository) *CommentService {
	return &CommentService{repo: repo}
}

func (s *CommentService) Post(hash, username, content string) (*model.Comment, error) {
	return s.repo.Create(hash, username, content)
}

func (s *CommentService) ListByHash(hash string) ([]model.Comment, error) {
	return s.repo.ListByHash(hash)
}

func (s *CommentService) CountAll() (int, error) {
	return s.repo.CountAll()
}
