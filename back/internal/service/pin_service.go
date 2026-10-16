// PinService is the IPFS pin use-case layer (M2 convergence: the p2p controller previously called repository pin functions directly).
// Note: the p2p.go controller as a whole is legacy (pending M1 migration), but pin endpoints are independent functionality,
// and after convergence the controller no longer touches the repository.
package service

import (
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
)

type PinService struct{}

func NewPinService() *PinService { return &PinService{} }

// Get looks up a pin by CID (returns nil, nil if not found).
func (s *PinService) Get(cid string) (*model.IPFSPin, error) {
	return repository.GetPin(cid)
}

// Insert adds or updates a pin.
func (s *PinService) Insert(cid, hash, filename string, size int64) error {
	return repository.InsertPin(cid, hash, filename, size)
}

// Remove deletes a pin.
func (s *PinService) Remove(cid string) error {
	return repository.RemovePin(cid)
}

// List lists all pins (newest first, up to 1000).
func (s *PinService) List() ([]model.IPFSPin, error) {
	return repository.ListPins()
}

// InsertMeta registers file metadata + local provider (called after pin cache is written to disk).
func (s *PinService) InsertMeta(hash, filename string, size int64, relPath string) error {
	if err := repository.InsertFileMeta(&model.FileMeta{
		Hash:     hash,
		Size:     size,
		Filename: filename,
		Type:     model.FileTypeBlob,
	}); err != nil {
		return err
	}
	return repository.InsertFileProvider(hash, "local", relPath)
}
