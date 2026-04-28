package service

import (
	"peerdrive-registration/internal/model"
	"peerdrive-registration/internal/repository"
)

type RelayService struct {
	repo *repository.RelayRepository
}

func NewRelayService(repo *repository.RelayRepository) *RelayService {
	return &RelayService{repo: repo}
}

func (s *RelayService) Register(node model.RelayNode) error {
	return s.repo.Upsert(node)
}

func (s *RelayService) ListActive() ([]model.RelayNode, error) {
	return s.repo.ListActive()
}

func (s *RelayService) Heartbeat(peerID string, loadPct float64) error {
	return s.repo.UpdateHeartbeat(peerID, loadPct)
}

func (s *RelayService) CountActive() (int, error) {
	return s.repo.CountActive()
}
