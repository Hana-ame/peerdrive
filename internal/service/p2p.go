// P2PService 封装 libp2p host、DHT、mDNS 发现和流式文件交换协议。
// 支持 announce、find providers、fetch file、sync files 及 WebSocket 文件请求。
package service

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/libp2p/go-libp2p/p2p/protocol/ping"
)

const (
	ProtocolExchange    = "/peerdrive/exchange/1.0.0"
	ProtocolAnnounce    = "/peerdrive/announce/1.0.0"
	ProtocolRequest     = "/peerdrive/request/1.0.0"
	DiscoveryServiceTag = "peerdrive-mdns"
	FileReadTimeout     = 30 * time.Second
)

type P2PService struct {
	Host       host.Host
	Ping       *ping.PingService
	DHT        *dht.IpfsDHT
	storageDir string
	cfg        *config.Config

	mu         sync.RWMutex
	discovered map[peer.ID]peer.AddrInfo

	wsHub       *wsHub
	requestCh   chan fileRequest
	responseCh  chan fileResponse

	ConnMgr    *ConnectionManager
	Transfer   *ChunkedTransfer

	tracker *PeerTracker
}

type fileRequest struct {
	Hash    string
	PeerID  peer.ID
	ReplyCh chan fileResponse
}

type fileResponse struct {
	Hash string
	Data []byte
	Err  error
}

// NewP2PService 创建 libp2p 服务，根据配置初始化 host、DHT、mDNS、中继和打洞等能力。
func NewP2PService(ctx context.Context, cfg *config.Config) (*P2PService, error) {
	defer log.LogDuration("P2PService.NewP2PService")()
	log.LogDebug("p2p: NewP2PService starting (enabled=%v)", cfg.P2PEnable)

	if !cfg.P2PEnable {
		log.LogInfo("p2p: P2P disabled, returning minimal service")
		return &P2PService{cfg: cfg}, nil
	}

	listenAddrs := []string{cfg.P2PListenAddr}
	if cfg.P2PListenAddrV6 != "" {
		listenAddrs = append(listenAddrs, cfg.P2PListenAddrV6)
	}
	opts := []libp2p.Option{
		libp2p.ListenAddrStrings(listenAddrs...),
		libp2p.EnableRelay(),
		libp2p.EnableNATService(),
	}

	if cfg.P2PHolePunch {
		opts = append(opts, libp2p.EnableHolePunching())
	}

	switch cfg.P2PRelayMode {
	case config.RelayServer:
		opts = append(opts, libp2p.EnableRelayService())
		opts = append(opts, libp2p.ForceReachabilityPublic())
	case config.RelayClient:
		if staticRelays := cfg.P2PStaticRelays; staticRelays != "" {
			addrs, err := parseStaticRelays(staticRelays)
			if err == nil && len(addrs) > 0 {
				opts = append(opts, libp2p.EnableAutoRelayWithStaticRelays(addrs))
				log.LogInfo("p2p: using %d static relay(s)", len(addrs))
			}
		}
	}

	if cfg.P2PNATPortMap {
		opts = append(opts, libp2p.NATPortMap())
	}

	if cfg.P2PAutoNAT {
		opts = append(opts, libp2p.EnableAutoNATv2())
	}

	h, err := libp2p.New(opts...)
	if err != nil {
		log.LogError("p2p: NewP2PService libp2p host creation failed: %v", err)
		return nil, fmt.Errorf("libp2p host: %w", err)
	}

	kdht, err := dht.New(ctx, h, dht.Mode(dht.ModeServer))
	if err != nil {
		h.Close()
		log.LogError("p2p: NewP2PService DHT init failed: %v", err)
		return nil, fmt.Errorf("dht init: %w", err)
	}

	if err := kdht.Bootstrap(ctx); err != nil {
		h.Close()
		log.LogError("p2p: NewP2PService DHT bootstrap failed: %v", err)
		return nil, fmt.Errorf("dht bootstrap: %w", err)
	}

	pingSvc := ping.NewPingService(h)

	svc := &P2PService{
		Host:       h,
		Ping:       pingSvc,
		DHT:        kdht,
		storageDir: cfg.StorageDir,
		cfg:        cfg,
		discovered: make(map[peer.ID]peer.AddrInfo),
		wsHub:      newWSHub(),
		requestCh:  make(chan fileRequest, 256),
		responseCh: make(chan fileResponse, 256),
	}

	h.SetStreamHandler(protocol.ID(ProtocolExchange), svc.handleExchange)
	h.SetStreamHandler(protocol.ID(ProtocolAnnounce), svc.handleAnnounce)
	h.SetStreamHandler(protocol.ID(ProtocolRequest), svc.handleRequest)

	if cfg.P2PMDNSEnable {
		if err := svc.setupMDNS(ctx); err != nil {
			log.LogWarn("p2p: mdns setup warning: %v", err)
		}
	}

	if cfg.P2PBootstrapPeer != "" {
		if err := svc.connectToBootstrap(ctx, cfg.P2PBootstrapPeer); err != nil {
			log.LogWarn("p2p: bootstrap connection warning: %v", err)
		}
	}

	go svc.processWSRequests()

	// Initialize connection manager and chunked transfer
	svc.ConnMgr = NewConnectionManager(svc)
	svc.Transfer = NewChunkedTransfer(svc)

	// Start auto-connect and heartbeat
	svc.ConnMgr.AutoConnectFromDiscovered()
	svc.ConnMgr.StartHeartbeat()

	log.LogInfo("p2p: NewP2PService completed, peerID=%s", h.ID().String())
	return svc, nil
}

func (p *P2PService) CfgP2PEnabled() bool {
	return p.cfg != nil && p.cfg.P2PEnable
}

// IsEnabled 返回 P2P 服务是否已启用且 host 已初始化。
func (p *P2PService) IsEnabled() bool {
	enabled := p.cfg != nil && p.cfg.P2PEnable && p.Host != nil
	log.LogDebug("p2p: IsEnabled=%v", enabled)
	return enabled
}

// GetNodeInfo 返回本节点的 PeerID 和监听地址列表。
func (p *P2PService) GetNodeInfo() (peer.ID, []string) {
	defer log.LogDuration("P2PService.GetNodeInfo")()
	if !p.IsEnabled() {
		log.LogDebug("p2p: GetNodeInfo returning empty (P2P disabled)")
		return "", []string{}
	}
	addrs := make([]string, 0)
	for _, addr := range p.Host.Addrs() {
		addrs = append(addrs, addr.String())
	}
	log.LogInfo("p2p: GetNodeInfo peerID=%s, addrs=%v", p.Host.ID().String(), addrs)
	return p.Host.ID(), addrs
}

// GetConnectedPeers 返回当前已连接的对端 ID 列表。
func (p *P2PService) GetConnectedPeers() []peer.ID {
	if !p.IsEnabled() {
		return nil
	}
	return p.Host.Network().Peers()
}

// GetDiscoveredPeers 返回通过 mDNS 等方式发现的所有对端信息。
func (p *P2PService) GetDiscoveredPeers() []peer.AddrInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]peer.AddrInfo, 0, len(p.discovered))
	for _, info := range p.discovered {
		result = append(result, info)
	}
	return result
}

// PingPeer 通过 libp2p ping 协议测量到指定对端的 RTT 延迟。
func (p *P2PService) PingPeer(ctx context.Context, peerID peer.ID) (time.Duration, error) {
	if !p.IsEnabled() {
		return 0, fmt.Errorf("p2p not enabled")
	}
	result := p.Ping.Ping(ctx, peerID)
	select {
	case res := <-result:
		return res.RTT, res.Error
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// Connect 连接到指定的对端地址信息。
func (p *P2PService) Connect(ctx context.Context, addrInfo peer.AddrInfo) error {
	defer log.LogDuration("P2PService.Connect")()
	log.LogDebug("p2p: Connect peer=%s", addrInfo.ID.String())

	if !p.IsEnabled() {
		err := fmt.Errorf("p2p not enabled")
		log.LogError("p2p: Connect failed: %v", err)
		return err
	}

	if err := p.Host.Connect(ctx, addrInfo); err != nil {
		log.LogError("p2p: Connect to %s failed: %v", addrInfo.ID.String(), err)
		return err
	}

	if p.tracker != nil {
		addrs := make([]string, len(addrInfo.Addrs))
		for i, a := range addrInfo.Addrs {
			addrs[i] = a.String()
		}
		p.tracker.RecordConnection(addrInfo.ID.String(), addrs, "")
	}

	log.LogInfo("p2p: Connect to %s successful", addrInfo.ID.String())
	return nil
}

// ConnectByAddr 通过 multiaddr 字符串连接到指定对端。
func (p *P2PService) ConnectByAddr(ctx context.Context, addrStr string) error {
	if !p.IsEnabled() {
		return fmt.Errorf("p2p not enabled")
	}
	maddr, err := multiaddrFromString(addrStr)
	if err != nil {
		return fmt.Errorf("invalid multiaddr: %w", err)
	}
	info, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return fmt.Errorf("parse peer addr: %w", err)
	}
	return p.Host.Connect(ctx, *info)
}

// AnnounceHash 在 libp2p DHT 上 announce 指定文件哈希。
func (p *P2PService) AnnounceHash(hash string) error {
	defer log.LogDuration("P2PService.AnnounceHash")()
	log.LogDebug("p2p: AnnounceHash hash=%s", hash)

	if !p.IsEnabled() || p.DHT == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.DHT.Provide(ctx, cidFromSha256(hash), true); err != nil {
		log.LogError("p2p: AnnounceHash failed: %v", err)
		return err
	}
	log.LogInfo("p2p: AnnounceHash %s successful", hash)
	return nil
}

// FindProviders 通过 DHT 查找指定哈希的文件提供者。
func (p *P2PService) FindProviders(hash string) ([]peer.AddrInfo, error) {
	defer log.LogDuration("P2PService.FindProviders")()
	log.LogDebug("p2p: FindProviders hash=%s", hash)

	if !p.IsEnabled() || p.DHT == nil {
		err := fmt.Errorf("dht not available")
		log.LogError("p2p: FindProviders failed: %v", err)
		return nil, err
	}
	p.mu.RLock()
	for _, info := range p.discovered {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := p.Host.Connect(ctx, info); err == nil {
			cancel()
			break
		}
		cancel()
	}
	p.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cid := cidFromSha256(hash)
	providers, err := p.DHT.FindProviders(ctx, cid)
	if err != nil {
		log.LogError("p2p: FindProviders DHT search failed: %v", err)
		return nil, fmt.Errorf("dht find providers: %w", err)
	}

	result := make([]peer.AddrInfo, 0)
	for _, pi := range providers {
		if pi.ID == p.Host.ID() {
			continue
		}
		result = append(result, pi)
	}

	log.LogInfo("p2p: FindProviders for %s found %d providers", hash, len(result))
	return result, nil
}

// FetchFile 从指定对端（或 DHT 发现的对端）获取文件，校验 SHA256 哈希。
func (p *P2PService) FetchFile(ctx context.Context, hash string, peers []peer.AddrInfo) ([]byte, error) {
	defer log.LogDuration("P2PService.FetchFile")()
	log.LogDebug("p2p: FetchFile hash=%s, peers=%d", hash, len(peers))

	if !p.IsEnabled() {
		err := fmt.Errorf("p2p not enabled")
		log.LogError("p2p: FetchFile failed: %v", err)
		return nil, err
	}

	if len(peers) == 0 {
		var err error
		peers, err = p.FindProviders(hash)
		if err != nil {
			log.LogWarn("p2p: DHT search failed for %s: %v", hash, err)
		}
	}

	if len(peers) == 0 {
		err := fmt.Errorf("no peers available for %s", hash)
		log.LogError("p2p: %v", err)
		return nil, err
	}

	for _, pi := range peers {
		if pi.ID == p.Host.ID() {
			continue
		}
		if p.Host.Network().Connectedness(pi.ID) != network.Connected {
			ctxConn, cancel := context.WithTimeout(ctx, 5*time.Second)
			if err := p.Host.Connect(ctxConn, pi); err != nil {
				cancel()
				continue
			}
			cancel()
		}

		data, err := p.requestData(ctx, pi.ID, hash)
		if err != nil {
			log.LogWarn("p2p: fetch from %s failed: %v", pi.ID, err)
			continue
		}

		h := sha256.Sum256(data)
		if hex.EncodeToString(h[:]) != hash {
			log.LogWarn("p2p: hash mismatch from %s, discarding", pi.ID)
			continue
		}

		log.LogInfo("p2p: fetched %s from %s (%d bytes)", hash, pi.ID.String(), len(data))
		return data, nil
	}

	err := fmt.Errorf("file not found on any peer")
	log.LogError("p2p: FetchFile %s: %v", hash, err)
	return nil, err
}

// FetchCollection 从对端获取集合文件并解析为 AnonCollection。
func (p *P2PService) FetchCollection(ctx context.Context, hash string, peers []peer.AddrInfo) (*model.AnonCollection, error) {
	data, err := p.FetchFile(ctx, hash, peers)
	if err != nil {
		return nil, err
	}
	var coll model.AnonCollection
	if err := json.Unmarshal(data, &coll); err != nil {
		return nil, fmt.Errorf("invalid collection json: %w", err)
	}
	return &coll, nil
}

// SyncFiles 从指定对端同步多个文件到本地目标目录，同时缓存到存储目录。
func (p *P2PService) SyncFiles(ctx context.Context, peerID peer.ID, hashes []string, targetDir string) ([]string, error) {
	defer log.LogDuration("P2PService.SyncFiles")()
	log.LogDebug("p2p: SyncFiles peer=%s, hashes=%d, target=%s", peerID.String(), len(hashes), targetDir)

	if !p.IsEnabled() {
		err := fmt.Errorf("p2p not enabled")
		log.LogError("p2p: SyncFiles failed: %v", err)
		return nil, err
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		log.LogError("p2p: SyncFiles create target dir failed: %v", err)
		return nil, fmt.Errorf("create target dir: %w", err)
	}

	synced := make([]string, 0)
	for _, hash := range hashes {
		data, err := p.requestData(ctx, peerID, hash)
		if err != nil {
			log.LogError("p2p: SyncFiles fetch %s failed: %v", hash, err)
			return nil, fmt.Errorf("fetch %s: %w", hash, err)
		}

		h := sha256.Sum256(data)
		if hex.EncodeToString(h[:]) != hash {
			log.LogError("p2p: SyncFiles hash mismatch for %s", hash)
			return nil, fmt.Errorf("hash mismatch for %s", hash)
		}

		destPath := filepath.Join(targetDir, hash)
		if err := os.WriteFile(destPath, data, 0644); err != nil {
			log.LogError("p2p: SyncFiles write %s failed: %v", destPath, err)
			return nil, fmt.Errorf("write %s: %w", destPath, err)
		}

		relPath := filepath.Join(hash[:2], hash)
		fullStorage := filepath.Join(p.storageDir, relPath)
		os.MkdirAll(filepath.Dir(fullStorage), 0755)
		os.WriteFile(fullStorage, data, 0644)

		repository.InsertFileMeta(&model.FileMeta{
			Hash:     hash,
			Size:     int64(len(data)),
			Filename: hash,
			Type:     repository.FileTypeBlob,
		})
		repository.InsertFileProvider(hash, "local", relPath)

		synced = append(synced, hash)
	}

	log.LogInfo("p2p: SyncFiles synced %d files from %s", len(synced), peerID.String())
	return synced, nil
}

func (p *P2PService) requestData(ctx context.Context, peerID peer.ID, hash string) ([]byte, error) {
	stream, err := p.Host.NewStream(ctx, peerID, protocol.ID(ProtocolExchange))
	if err != nil {
		return nil, fmt.Errorf("open stream: %w", err)
	}
	defer stream.Close()

	stream.SetReadDeadline(time.Now().Add(FileReadTimeout))
	if _, err := fmt.Fprintf(stream, "%s\n", hash); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	reader := bufio.NewReader(stream)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read status: %w", err)
	}

	var status int
	if _, scanErr := fmt.Sscanf(statusLine, "OK %d\n", &status); scanErr != nil || status <= 0 {
		return nil, fmt.Errorf("peer returned error: %s", statusLine)
	}

	data := make([]byte, status)
	if _, err := io.ReadFull(reader, data); err != nil {
		return nil, fmt.Errorf("read data: %w", err)
	}

	if p.tracker != nil {
		p.tracker.RecordBytesRecv(peerID.String(), int64(len(data)))
	}

	return data, nil
}

func (p *P2PService) handleExchange(stream network.Stream) {
	log.LogDebug("p2p: handleExchange from %s", stream.Conn().RemotePeer().String())
	defer stream.Close()

	reader := bufio.NewReader(stream)
	hashLine, err := reader.ReadString('\n')
	if err != nil {
		log.LogWarn("p2p: handleExchange bad request from %s: %v", stream.Conn().RemotePeer().String(), err)
		fmt.Fprintf(stream, "ERR bad request\n")
		return
	}
	hash := trimNewline(hashLine)

	// handle SIZE command for chunked transfer
	if strings.HasPrefix(hash, "SIZE ") {
		hash = strings.TrimPrefix(hash, "SIZE ")
		if len(hash) != 64 {
			fmt.Fprintf(stream, "ERR invalid hash length %d\n", len(hash))
			return
		}
		filePath := filepath.Join(p.storageDir, hash[:2], hash)
		info, err := os.Stat(filePath)
		if err != nil {
			log.LogDebug("p2p: handleExchange SIZE not found for %s", hash)
			fmt.Fprintf(stream, "ERR not found\n")
			return
		}
		fmt.Fprintf(stream, "OK %d\n", info.Size())
		return
	}

	if len(hash) != 64 {
		fmt.Fprintf(stream, "ERR invalid hash length %d\n", len(hash))
		return
	}

	filePath := filepath.Join(p.storageDir, hash[:2], hash)
	data, err := os.ReadFile(filePath)
	if err != nil {
		meta, _ := repository.GetFileMeta(hash)
		if meta != nil {
			providers, _ := repository.GetFileProviders(hash)
			for _, prov := range providers {
				if prov.ProviderType == "local" {
					absPath := prov.Path
					data, err = os.ReadFile(absPath)
					if err == nil {
						break
					}
				}
			}
		}
	}
	if err != nil || data == nil {
		log.LogDebug("p2p: handleExchange file not found for %s", hash)
		fmt.Fprintf(stream, "ERR not found\n")
		return
	}

	log.LogInfo("p2p: handleExchange serving %s to %s (%d bytes)", hash, stream.Conn().RemotePeer().String(), len(data))
	fmt.Fprintf(stream, "OK %d\n", len(data))
	stream.Write(data)

	if p.tracker != nil {
		p.tracker.RecordBytesSent(stream.Conn().RemotePeer().String(), int64(len(data)))
	}
}

func (p *P2PService) handleAnnounce(stream network.Stream) {
	log.LogDebug("p2p: handleAnnounce from %s", stream.Conn().RemotePeer().String())
	defer stream.Close()

	reader := bufio.NewReader(stream)
	hashLine, err := reader.ReadString('\n')
	if err != nil {
		log.LogWarn("p2p: handleAnnounce read error from %s: %v", stream.Conn().RemotePeer().String(), err)
		return
	}
	hash := trimNewline(hashLine)

	log.LogInfo("p2p: peer %s announced hash %s", stream.Conn().RemotePeer().String(), hash)

	if p.DHT != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			p.DHT.Provide(ctx, cidFromSha256(hash), true)
		}()
	}

	fmt.Fprintf(stream, "OK\n")
}

func (p *P2PService) handleRequest(stream network.Stream) {
	log.LogDebug("p2p: handleRequest from %s", stream.Conn().RemotePeer().String())
	defer stream.Close()

	reader := bufio.NewReader(stream)
	hashLine, err := reader.ReadString('\n')
	if err != nil {
		log.LogWarn("p2p: handleRequest read error from %s: %v", stream.Conn().RemotePeer().String(), err)
		return
	}
	hash := trimNewline(hashLine)

	log.LogInfo("p2p: peer %s requested hash %s via stream", stream.Conn().RemotePeer().String(), hash)

	p.requestCh <- fileRequest{
		Hash:   hash,
		PeerID: stream.Conn().RemotePeer(),
	}
}

func (p *P2PService) processWSRequests() {
	for req := range p.requestCh {
		filePath := filepath.Join(p.storageDir, req.Hash[:2], req.Hash)
		data, err := os.ReadFile(filePath)
		if err != nil {
			meta, _ := repository.GetFileMeta(req.Hash)
			if meta != nil {
				providers, _ := repository.GetFileProviders(req.Hash)
				for _, prov := range providers {
					if prov.ProviderType == "local" {
						data, err = os.ReadFile(prov.Path)
						if err == nil {
							break
						}
					}
				}
			}
		}

		if err != nil || data == nil {
			log.LogWarn("p2p: requested file not found: %s", req.Hash)
			continue
		}

		resp := fileResponse{Hash: req.Hash, Data: data}
		if req.ReplyCh != nil {
			select {
			case req.ReplyCh <- resp:
			default:
			}
		}
	}
}

// BroadcastRequest 向多个对端广播文件请求，返回所有响应结果。
func (p *P2PService) BroadcastRequest(hash string, peerIDs []peer.ID) ([]fileResponse, error) {
	defer log.LogDuration("P2PService.BroadcastRequest")()
	log.LogDebug("p2p: BroadcastRequest hash=%s, targets=%d", hash, len(peerIDs))

	if !p.IsEnabled() {
		err := fmt.Errorf("p2p not enabled")
		log.LogError("p2p: BroadcastRequest failed: %v", err)
		return nil, err
	}

	targets := peerIDs
	if len(targets) == 0 {
		targets = p.GetConnectedPeers()
		log.LogDebug("p2p: BroadcastRequest using %d connected peers", len(targets))
	}

	results := make([]fileResponse, 0)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, pid := range targets {
		wg.Add(1)
		go func(pid peer.ID) {
			defer wg.Done()
			data, err := p.requestData(context.Background(), pid, hash)
			mu.Lock()
			results = append(results, fileResponse{Hash: hash, Data: data, Err: err})
			mu.Unlock()
		}(pid)
	}
	wg.Wait()

	if len(results) == 0 {
		err := fmt.Errorf("no peers available")
		log.LogError("p2p: BroadcastRequest: %v", err)
		return nil, err
	}

	log.LogInfo("p2p: BroadcastRequest %s got %d responses", hash, len(results))
	return results, nil
}

func (p *P2PService) setupMDNS(ctx context.Context) error {
	discoverySvc := mdns.NewMdnsService(p.Host, DiscoveryServiceTag, p)
	return discoverySvc.Start()
}

func (p *P2PService) HandlePeerFound(pi peer.AddrInfo) {
	p.mu.Lock()
	p.discovered[pi.ID] = pi
	p.mu.Unlock()
	log.LogInfo("p2p: discovered peer: %s", pi.ID.String())

	if p.tracker != nil {
		addrs := make([]string, len(pi.Addrs))
		for i, a := range pi.Addrs {
			addrs[i] = a.String()
		}
		p.tracker.RecordConnection(pi.ID.String(), addrs, "")
	}
}

// RelayMode 返回当前中继模式（off/server/client）。
func (p *P2PService) RelayMode() string {
	if p.cfg == nil {
		return string(config.RelayOff)
	}
	return string(p.cfg.P2PRelayMode)
}

// HolePunchEnabled 返回是否启用了 NAT 打洞功能。
func (p *P2PService) HolePunchEnabled() bool {
	return p.cfg != nil && p.cfg.P2PHolePunch
}

func (p *P2PService) connectToBootstrap(ctx context.Context, addr string) error {
	info, err := parsePeerAddr(addr)
	if err != nil {
		return err
	}
	return p.Host.Connect(ctx, *info)
}

// SetPeerTracker 注入 PeerTracker 并注册网络连接/断开的通知回调。
func (p *P2PService) SetPeerTracker(t *PeerTracker) {
	p.tracker = t
	if p.Host != nil {
		p.Host.Network().Notify(&network.NotifyBundle{
			ConnectedF: func(n network.Network, c network.Conn) {
				if p.tracker != nil {
					addrs := []string{c.RemoteMultiaddr().String()}
					p.tracker.RecordConnection(c.RemotePeer().String(), addrs, "")
					direction := "inbound"
					if c.Stat().Direction == network.DirOutbound {
						direction = "outbound"
					}
					p.tracker.SetDirection(c.RemotePeer().String(), direction)
				}
			},
			DisconnectedF: func(n network.Network, c network.Conn) {
				if p.tracker != nil {
					p.tracker.RecordDisconnect(c.RemotePeer().String())
				}
			},
		})
	}
}

// Close 关闭 P2P 服务，停止连接管理器、请求通道和 libp2p host。
func (p *P2PService) Close() error {
	log.LogDebug("p2p: Close shutting down")
	if p.ConnMgr != nil {
		p.ConnMgr.StopHeartbeat()
	}
	if p.requestCh != nil {
		close(p.requestCh)
	}
	if p.responseCh != nil {
		close(p.responseCh)
	}
	if p.Host != nil {
		return p.Host.Close()
	}
	return nil
}

func trimNewline(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\n' {
		return s[:len(s)-1]
	}
	return s
}
