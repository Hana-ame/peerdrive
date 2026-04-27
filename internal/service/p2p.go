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

func NewP2PService(ctx context.Context, cfg *config.Config) (*P2PService, error) {
	if !cfg.P2PEnable {
		return &P2PService{cfg: cfg}, nil
	}

	opts := []libp2p.Option{
		libp2p.ListenAddrStrings(cfg.P2PListenAddr),
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
				logf("using %d static relay(s)", len(addrs))
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
		return nil, fmt.Errorf("libp2p host: %w", err)
	}

	kdht, err := dht.New(ctx, h, dht.Mode(dht.ModeServer))
	if err != nil {
		h.Close()
		return nil, fmt.Errorf("dht init: %w", err)
	}

	if err := kdht.Bootstrap(ctx); err != nil {
		h.Close()
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
			logf("mdns setup warning: %v", err)
		}
	}

	if cfg.P2PBootstrapPeer != "" {
		if err := svc.connectToBootstrap(ctx, cfg.P2PBootstrapPeer); err != nil {
			logf("bootstrap connection warning: %v", err)
		}
	}

	go svc.processWSRequests()

	// Initialize connection manager and chunked transfer
	svc.ConnMgr = NewConnectionManager(svc)
	svc.Transfer = NewChunkedTransfer(svc)

	// Start auto-connect and heartbeat
	svc.ConnMgr.AutoConnectFromDiscovered()
	svc.ConnMgr.StartHeartbeat()

	return svc, nil
}

func logf(format string, args ...interface{}) {
	full := fmt.Sprintf("[p2p] "+format, args...)
	os.Stderr.WriteString(full + "\n")
}

func (p *P2PService) IsEnabled() bool {
	return p.cfg != nil && p.cfg.P2PEnable && p.Host != nil
}

func (p *P2PService) GetNodeInfo() (peer.ID, []string) {
	if !p.IsEnabled() {
		return "", []string{}
	}
	addrs := make([]string, 0)
	for _, addr := range p.Host.Addrs() {
		addrs = append(addrs, addr.String())
	}
	return p.Host.ID(), addrs
}

func (p *P2PService) GetConnectedPeers() []peer.ID {
	if !p.IsEnabled() {
		return nil
	}
	return p.Host.Network().Peers()
}

func (p *P2PService) GetDiscoveredPeers() []peer.AddrInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]peer.AddrInfo, 0, len(p.discovered))
	for _, info := range p.discovered {
		result = append(result, info)
	}
	return result
}

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

func (p *P2PService) Connect(ctx context.Context, addrInfo peer.AddrInfo) error {
	if !p.IsEnabled() {
		return fmt.Errorf("p2p not enabled")
	}
	return p.Host.Connect(ctx, addrInfo)
}

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

func (p *P2PService) AnnounceHash(hash string) error {
	if !p.IsEnabled() || p.DHT == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return p.DHT.Provide(ctx, cidFromSha256(hash), true)
}

func (p *P2PService) FindProviders(hash string) ([]peer.AddrInfo, error) {
	if !p.IsEnabled() || p.DHT == nil {
		return nil, fmt.Errorf("dht not available")
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
		return nil, fmt.Errorf("dht find providers: %w", err)
	}

	result := make([]peer.AddrInfo, 0)
	for _, pi := range providers {
		if pi.ID == p.Host.ID() {
			continue
		}
		result = append(result, pi)
	}
	return result, nil
}

func (p *P2PService) FetchFile(ctx context.Context, hash string, peers []peer.AddrInfo) ([]byte, error) {
	if !p.IsEnabled() {
		return nil, fmt.Errorf("p2p not enabled")
	}

	if len(peers) == 0 {
		var err error
		peers, err = p.FindProviders(hash)
		if err != nil {
			logf("DHT search failed for %s: %v", hash, err)
		}
	}

	if len(peers) == 0 {
		return nil, fmt.Errorf("no peers available for %s", hash)
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
			logf("fetch from %s failed: %v", pi.ID, err)
			continue
		}

		h := sha256.Sum256(data)
		if hex.EncodeToString(h[:]) != hash {
			logf("hash mismatch from %s, discarding", pi.ID)
			continue
		}

		return data, nil
	}

	return nil, fmt.Errorf("file not found on any peer")
}

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

func (p *P2PService) SyncFiles(ctx context.Context, peerID peer.ID, hashes []string, targetDir string) ([]string, error) {
	if !p.IsEnabled() {
		return nil, fmt.Errorf("p2p not enabled")
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, fmt.Errorf("create target dir: %w", err)
	}

	synced := make([]string, 0)
	for _, hash := range hashes {
		data, err := p.requestData(ctx, peerID, hash)
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", hash, err)
		}

		h := sha256.Sum256(data)
		if hex.EncodeToString(h[:]) != hash {
			return nil, fmt.Errorf("hash mismatch for %s", hash)
		}

		destPath := filepath.Join(targetDir, hash)
		if err := os.WriteFile(destPath, data, 0644); err != nil {
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

	return data, nil
}

func (p *P2PService) handleExchange(stream network.Stream) {
	defer stream.Close()

	reader := bufio.NewReader(stream)
	hashLine, err := reader.ReadString('\n')
	if err != nil {
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
		fmt.Fprintf(stream, "ERR not found\n")
		return
	}

	fmt.Fprintf(stream, "OK %d\n", len(data))
	stream.Write(data)
}

func (p *P2PService) handleAnnounce(stream network.Stream) {
	defer stream.Close()

	reader := bufio.NewReader(stream)
	hashLine, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	hash := trimNewline(hashLine)

	logf("peer %s announced hash %s", stream.Conn().RemotePeer(), hash)

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
	defer stream.Close()

	reader := bufio.NewReader(stream)
	hashLine, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	hash := trimNewline(hashLine)

	logf("peer %s requested hash %s via stream", stream.Conn().RemotePeer(), hash)

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
			logf("requested file not found: %s", req.Hash)
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

func (p *P2PService) BroadcastRequest(hash string, peerIDs []peer.ID) ([]fileResponse, error) {
	if !p.IsEnabled() {
		return nil, fmt.Errorf("p2p not enabled")
	}

	targets := peerIDs
	if len(targets) == 0 {
		targets = p.GetConnectedPeers()
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
		return nil, fmt.Errorf("no peers available")
	}

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
	logf("discovered peer: %s", pi.ID.String())
}

func (p *P2PService) RelayMode() string {
	if p.cfg == nil {
		return string(config.RelayOff)
	}
	return string(p.cfg.P2PRelayMode)
}

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

func (p *P2PService) Close() error {
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
