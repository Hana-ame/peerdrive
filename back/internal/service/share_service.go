// ShareService is the share link use-case layer (M2 convergence: the share controller previously called repository directly).
package service

import (
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
)

type ShareService struct{}

func NewShareService() *ShareService { return &ShareService{} }

// Create creates a share link valid for 30 days.
func (s *ShareService) Create(hash, shareType, filename string) (*model.ShareLink, error) {
	return repository.CreateShare(hash, shareType, filename)
}

// GetByToken queries an unexpired share by token.
func (s *ShareService) GetByToken(token string) (*model.ShareLink, error) {
	return repository.GetShareByToken(token)
}

// List lists all unexpired shares (up to 100 entries).
func (s *ShareService) List() ([]model.ShareLink, error) {
	return repository.ListShares()
}
