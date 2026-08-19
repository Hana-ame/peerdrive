package source

// ipfs_control.go：IPFSControl 实现——包装 IPFSProvider + repository pin。
// 发现背景：Source 控制面设计（doc/source-control.md），IPFS pin/网关管理。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/provider"
	"peerdrive/internal/repository"
)

// ipfsController 是 IPFSControl 的封装实现。
type ipfsController struct {
	prov  *provider.IPFSProvider
	store string // 存储目录（pin 缓存）
}

// NewIPFSControl 创建 IPFS 控制面实例（nil 后所有方法返回 ErrControlUnsupported）。
func NewIPFSControl(prov *provider.IPFSProvider, storageDir string) IPFSControl {
	return &ipfsController{prov: prov, store: storageDir}
}

func (c *ipfsController) PinCID(cid string) (*PinInfo, error) {
	if c.prov == nil {
		return nil, ErrControlUnsupported
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	data, err := c.prov.FetchByCID(ctx, cid)
	if err != nil {
		return nil, fmt.Errorf("ipfs: fetch %s: %w", cid, err)
	}
	h := sha256.Sum256(data)
	hash := hex.EncodeToString(h[:])
	// 写缓存
	relPath := filepath.Join(hash[:2], hash)
	absPath := filepath.Join(c.store, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(absPath, data, 0o644); err != nil {
		return nil, err
	}
	// 登记 pin
	if err := repository.InsertPin(cid, hash, cid, int64(len(data))); err != nil {
		log.LogWarn("source/ipfs: insert pin %s: %v", cid, err)
	}
	// 登记 file_meta + provider
	if err := repository.InsertFileMeta(&model.FileMeta{
		Hash:     hash,
		Size:     int64(len(data)),
		Filename: cid,
		Type:     model.FileTypeBlob,
	}); err != nil && !strings.Contains(err.Error(), "UNIQUE") {
		log.LogWarn("source/ipfs: insert meta %s: %v", hash, err)
	}
	if err := repository.InsertFileProvider(hash, "local", relPath); err != nil {
		log.LogWarn("source/ipfs: insert provider %s: %v", hash, err)
	}
	log.LogInfo("source/ipfs: pin cid=%s hash=%s size=%d", cid, hash, len(data))
	return &PinInfo{CID: cid, Hash: hash, Size: int64(len(data)), Filename: cid}, nil
}

func (c *ipfsController) UnpinCID(cid string) error {
	if c.prov == nil {
		return ErrControlUnsupported
	}
	pin, err := repository.GetPin(cid)
	if err != nil {
		return err
	}
	if pin == nil {
		return fmt.Errorf("pin %s not found", cid)
	}
	if err := repository.RemovePin(cid); err != nil {
		return err
	}
	if pin.Hash != "" {
		path := filepath.Join(c.store, pin.Hash[:2], pin.Hash)
		os.Remove(path)
	}
	log.LogInfo("source/ipfs: unpin cid=%s", cid)
	return nil
}

func (c *ipfsController) ListPins() ([]PinInfo, error) {
	if c.prov == nil {
		return nil, ErrControlUnsupported
	}
	pins, err := repository.ListPins()
	if err != nil {
		return nil, err
	}
	out := make([]PinInfo, len(pins))
	for i, p := range pins {
		out[i] = PinInfo{
			CID:      p.CID,
			Hash:     p.Hash,
			Filename: p.Filename,
			Size:     p.Size,
			PinnedAt: p.PinnedAt,
		}
	}
	return out, nil
}

func (c *ipfsController) GatewayStatus() ([]GatewayStatus, error) {
	if c.prov == nil || len(c.prov.Gateways) == 0 {
		return nil, ErrControlUnsupported
	}
	var out []GatewayStatus
	for _, gw := range c.prov.Gateways {
		start := time.Now()
		url := strings.TrimRight(gw, "/") + "/ipfs/QmUNLLsPACCz1vLxQVkXqqLX5R1X345qqfHbsf67hvA3Nn"
		req, err := http.NewRequest("HEAD", url, nil)
		if err != nil {
			out = append(out, GatewayStatus{URL: gw, Online: false})
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req = req.WithContext(ctx)
		resp, err := http.DefaultClient.Do(req)
		cancel()
		latency := time.Since(start)
		if err == nil {
			_ = resp.Body.Close()
		}
		out = append(out, GatewayStatus{
			URL:     gw,
			Online:  err == nil,
			Latency: latency.Truncate(time.Millisecond).String(),
		})
	}
	return out, nil
}
