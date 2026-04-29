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

func (s *RelayService) RegisterWithOperator(node model.RelayNode, operatorUsername string) error {
	return s.repo.UpsertWithOperator(node, operatorUsername)
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

func (s *RelayService) SetOperator(peerID, username string) error {
	return s.repo.SetOperator(peerID, username)
}

func (s *RelayService) GetByPeerID(peerID string) (*model.RelayNodeDetail, error) {
	return s.repo.GetByPeerID(peerID)
}

func (s *RelayService) ListByOperator(username string) ([]model.RelayNodeDetail, error) {
	return s.repo.ListByOperator(username)
}
