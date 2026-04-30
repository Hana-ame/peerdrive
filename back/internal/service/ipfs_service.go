// IPFSService 封装 boxo Bitswap 客户端/服务端，复用 P2PService 的 libp2p host 和 DHT。
// Blockstore 直接映射 SHA-256 内容寻址存储，不复制文件。
// Pin = 文件在 content-addressed storage 中存在。
package service

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"peerdrive/internal/log"
	"peerdrive/pkg/hashutil"

	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	mh "github.com/multiformats/go-multihash"

	"github.com/ipfs/boxo/bitswap"
	bsnet "github.com/ipfs/boxo/bitswap/network/bsnet"
	"github.com/ipfs/boxo/blockstore"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

// IPFSService 管理 IPFS Bitswap 客户端、服务端、DHT 提供/查询。
type IPFSService struct {
	host       host.Host
	dht        *dht.IpfsDHT
	storageDir string

	blockstore blockstore.Blockstore
	bswap      *bitswap.Bitswap

	enabled bool
	mu      sync.RWMutex
}

// NewIPFSService 创建 IPFSService。需要已初始化的 P2PService 提供 libp2p host 和 DHT。
func NewIPFSService(ctx context.Context, p2p *P2PService, storageDir string) (*IPFSService, error) {
	s := &IPFSService{
		storageDir: storageDir,
	}

	bs := newPeerdriveBlockstore(storageDir)
	s.blockstore = bs

	if p2p == nil || p2p.Host == nil {
		log.LogInfo("ipfs-svc: P2P disabled, blockstore-only mode")
		return s, nil
	}

	s.host = p2p.Host
	s.dht = p2p.DHT

	// Bitswap 网络层 + 客户端/服务端（复用 libp2p host + DHT）
	bsNetwork := bsnet.NewFromIpfsHost(s.host)
	s.bswap = bitswap.New(ctx, bsNetwork, s.dht, bs)
	bsNetwork.Start(s.bswap)

	s.enabled = true
	log.LogInfo("ipfs-svc: Bitswap+DHT ready, storage=%s", storageDir)
	return s, nil
}

// Enabled 返回 Bitswap 是否完整可用。
func (s *IPFSService) Enabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled
}

// ─── Blockstore 访问 ─────────────────────────────────────────────

// HasCID 检查 CID 对应的文件是否在本地存储中存在。
func (s *IPFSService) HasCID(cidStr string) bool {
	c, err := cid.Decode(cidStr)
	if err != nil {
		return false
	}
	has, _ := s.blockstore.Has(context.Background(), c)
	return has
}

// GetBlock 读取 CID 对应的原始数据。
func (s *IPFSService) GetBlock(cidStr string) ([]byte, error) {
	c, err := cid.Decode(cidStr)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	blk, err := s.blockstore.Get(ctx, c)
	if err != nil {
		return nil, err
	}
	return blk.RawData(), nil
}

// BlockCount 返回本地存储中可提供的文件数。
func (s *IPFSService) BlockCount() int {
	return countFiles(s.storageDir)
}

// BlockstorePath 返回存储根目录。
func (s *IPFSService) BlockstorePath() string {
	return s.storageDir
}

// ─── DHT 操作 ────────────────────────────────────────────────────

// Provide 通过 IPFS DHT 宣布为某 SHA-256 文件提供 CID。
func (s *IPFSService) Provide(ctx context.Context, sha256hex string) error {
	if !s.enabled || s.dht == nil {
		return nil
	}
	cidStr := hashutil.SHA256ToCID(sha256hex)
	if cidStr == "" {
		return nil
	}
	c, err := cid.Decode(cidStr)
	if err != nil {
		return err
	}
	return s.dht.Provide(ctx, c, true)
}

// ProvideAll 遍历本地所有文件并在 DHT 上宣布提供。
func (s *IPFSService) ProvideAll(ctx context.Context) {
	if !s.enabled || s.dht == nil {
		return
	}
	cidCh, err := s.blockstore.AllKeysChan(ctx)
	if err != nil {
		return
	}
	for c := range cidCh {
		if err := s.dht.Provide(ctx, c, true); err != nil {
			log.LogWarn("ipfs-svc: provide %s failed: %v", c, err)
		}
	}
}

// FindProviders 通过 IPFS DHT 查找 CID 的提供者。
func (s *IPFSService) FindProviders(ctx context.Context, cidStr string, count int) <-chan peer.AddrInfo {
	c, err := cid.Decode(cidStr)
	if err != nil || s.dht == nil {
		ch := make(chan peer.AddrInfo)
		close(ch)
		return ch
	}
	return s.dht.FindProvidersAsync(ctx, c, count)
}

// ─── Bitswap 获取 ────────────────────────────────────────────────

// FetchByCID 先查本地 blockstore，再通过 Bitswap 从网络拉取。
func (s *IPFSService) FetchByCID(ctx context.Context, cidStr string) ([]byte, error) {
	c, err := cid.Decode(cidStr)
	if err != nil {
		return nil, fmt.Errorf("ipfs: invalid CID %q: %w", cidStr, err)
	}

	// 本地已有
	if blk, err := s.blockstore.Get(ctx, c); err == nil {
		return blk.RawData(), nil
	}

	// Bitswap 网络获取
	if s.enabled && s.bswap != nil {
		blk, err := s.bswap.GetBlock(ctx, c)
		if err == nil {
			return blk.RawData(), nil
		}
	}

	return nil, fmt.Errorf("ipfs: CID %s not found locally or via Bitswap", cidStr)
}

// AddToBlockstore 将 SHA-256 存储中的已有文件注册为 IPFS 块。
func (s *IPFSService) AddToBlockstore(sha256hex string) error {
	if len(sha256hex) != 64 {
		return fmt.Errorf("invalid sha256: %s", sha256hex)
	}

	cidStr := hashutil.SHA256ToCID(sha256hex)
	if cidStr == "" {
		return fmt.Errorf("sha256 to cid failed: %s", sha256hex)
	}
	c, err := cid.Decode(cidStr)
	if err != nil {
		return err
	}

	srcPath := filepath.Join(s.storageDir, sha256hex[:2], sha256hex)
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}

	blk, err := blocks.NewBlockWithCid(data, c)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.blockstore.Put(ctx, blk)
}

// Close 释放 Bitswap 资源。
func (s *IPFSService) Close() error {
	if s.bswap != nil {
		s.bswap.Close()
	}
	return nil
}

// ====================================================================
// peerdriveBlockstore — blockstore.Blockstore 基于 SHA-256 文件系统
// ====================================================================

type peerdriveBlockstore struct {
	storageDir string
}

func newPeerdriveBlockstore(storageDir string) *peerdriveBlockstore {
	return &peerdriveBlockstore{storageDir: storageDir}
}

func (bs *peerdriveBlockstore) cidToPath(c cid.Cid) string {
	mhash := c.Hash()
	dec, err := mh.Decode(mhash)
	if err != nil || dec.Code != mh.SHA2_256 || dec.Length != 32 || len(dec.Digest) != 32 {
		return ""
	}
	sha256hex := hex.EncodeToString(dec.Digest)
	return filepath.Join(bs.storageDir, sha256hex[:2], sha256hex)
}

func (bs *peerdriveBlockstore) Has(ctx context.Context, c cid.Cid) (bool, error) {
	p := bs.cidToPath(c)
	if p == "" {
		return false, nil
	}
	_, err := os.Stat(p)
	return err == nil, nil
}

func (bs *peerdriveBlockstore) Get(ctx context.Context, c cid.Cid) (blocks.Block, error) {
	p := bs.cidToPath(c)
	if p == "" {
		return nil, fmt.Errorf("unsupported CID hash type: %v", c)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("block not found: %s", c)
	}
	return blocks.NewBlockWithCid(data, c)
}

func (bs *peerdriveBlockstore) GetSize(ctx context.Context, c cid.Cid) (int, error) {
	p := bs.cidToPath(c)
	if p == "" {
		return -1, fmt.Errorf("unsupported CID: %s", c)
	}
	info, err := os.Stat(p)
	if err != nil {
		return -1, fmt.Errorf("block not found: %s", c)
	}
	return int(info.Size()), nil
}

func (bs *peerdriveBlockstore) Put(ctx context.Context, blk blocks.Block) error {
	p := bs.cidToPath(blk.Cid())
	if p == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	return os.WriteFile(p, blk.RawData(), 0644)
}

func (bs *peerdriveBlockstore) PutMany(ctx context.Context, blks []blocks.Block) error {
	for _, blk := range blks {
		if err := bs.Put(ctx, blk); err != nil {
			return err
		}
	}
	return nil
}

func (bs *peerdriveBlockstore) DeleteBlock(ctx context.Context, c cid.Cid) error {
	p := bs.cidToPath(c)
	if p == "" {
		return nil
	}
	return os.Remove(p)
}

func (bs *peerdriveBlockstore) AllKeysChan(ctx context.Context) (<-chan cid.Cid, error) {
	ch := make(chan cid.Cid)
	go func() {
		defer close(ch)
		entries, err := os.ReadDir(bs.storageDir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if !entry.IsDir() || len(entry.Name()) != 2 {
				continue
			}
			subDir := filepath.Join(bs.storageDir, entry.Name())
			files, err := os.ReadDir(subDir)
			if err != nil {
				continue
			}
			for _, f := range files {
				if f.IsDir() {
					continue
				}
				cidStr := hashutil.SHA256ToCID(f.Name())
				if cidStr == "" {
					continue
				}
				c, err := cid.Decode(cidStr)
				if err != nil {
					continue
				}
				select {
				case <-ctx.Done():
					return
				case ch <- c:
				}
			}
		}
	}()
	return ch, nil
}

func (bs *peerdriveBlockstore) HashOnRead(enabled bool) {}

func countFiles(storageDir string) int {
	count := 0
	entries, err := os.ReadDir(storageDir)
	if err != nil {
		return 0
	}
	for _, entry := range entries {
		if !entry.IsDir() || len(entry.Name()) != 2 {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(storageDir, entry.Name()))
		count += len(files)
	}
	return count
}
