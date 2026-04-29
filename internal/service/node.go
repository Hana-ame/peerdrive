package service

import (
	"peerdrive-registration/internal/model"
	"peerdrive-registration/internal/repository"
)

type NodeService struct {
	repo *repository.NodeRepository
}

func NewNodeService(repo *repository.NodeRepository) *NodeService {
	return &NodeService{repo: repo}
}

func (s *NodeService) Register(node model.PeerNode) error {
	return s.repo.Upsert(node)
}

func (s *NodeService) Heartbeat(peerID string) error {
	return s.repo.Heartbeat(peerID)
}

func (s *NodeService) GetByPeerID(peerID string) (*model.UserNodeInfo, error) {
	return s.repo.GetByPeerID(peerID)
}

func (s *NodeService) ListByUsername(username string) ([]model.UserNodeInfo, error) {
	return s.repo.ListByUsername(username)
}

func (s *NodeService) AddTransferStats(peerID string, uploadDelta, downloadDelta int64) error {
	return s.repo.AddTransferStats(peerID, uploadDelta, downloadDelta)
}

func (s *NodeService) CountActive() (int, error) {
	return s.repo.CountActive()
}
