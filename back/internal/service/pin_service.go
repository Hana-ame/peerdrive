// PinService IPFS pin 用例层（M2 收层：p2p 控制器此前直调 repository pin 函数）。
// 注意：所属控制器 p2p.go 整体属 legacy（待 M1 迁移），但 pin 端点是独立功能，
// 收编后 controller 不再触碰 repository。
package service

import (
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
)

type PinService struct{}

func NewPinService() *PinService { return &PinService{} }

// Get 按 CID 查 pin（未找到返回 nil,nil）。
func (s *PinService) Get(cid string) (*model.IPFSPin, error) {
	return repository.GetPin(cid)
}

// Insert 新增/更新 pin。
func (s *PinService) Insert(cid, hash, filename string, size int64) error {
	return repository.InsertPin(cid, hash, filename, size)
}

// Remove 删除 pin。
func (s *PinService) Remove(cid string) error {
	return repository.RemovePin(cid)
}

// List 列出全部 pin（最新优先，最多 1000）。
func (s *PinService) List() ([]model.IPFSPin, error) {
	return repository.ListPins()
}

// InsertMeta 登记文件元数据 + 本地 provider（pin 缓存落盘后调用）。
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