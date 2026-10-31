// Package btservice exposes the BitTorrent runtime (torrent client + mainline
// DHT + DHT bridge) behind a single JSON-in / JSON-out entry point.
//
// 发现背景：BT 栈默认是关的（PEERDRIVE_BT_ENABLE 默认 false），但 router 却无条件
// 调 p2p_bt.NewBTClient —— 于是每个进程启动都会开监听端口、跑 DHT、联网拉公共
// tracker（tracker_list.go 的 buildPublicTrackers）。「按需加载」要求运行时能被
// 整体搬出主进程：主程序只留一层 JSON 客户端（internal/btloader），真正的 BT
// 运行时装进 peerdrive_bt.dll（Windows，见 cmd/btbridge）。
//
// 这个包就是「被搬走的运行时」本体。它刻意保持纯 Go、不碰 cgo，这样：
//   - Linux/CI 上可以直接 go test 覆盖全部 op（不需要 Windows 或 mingw）；
//   - 跨 ABI 的只有字符串，C 侧不出现任何 Go 对象，编组错误只可能出在 JSON 层。
//
// 所有 op 的参数与返回值都可 JSON 编码（二进制走 base64），因此同一份实现能被
// 多种宿主驱动：DLL（c-shared）、同进程直调（测试）。
package btservice

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Hana-ame/go-peerdrive-bt"
)

// maxPendingCompletions bounds the completion queue. The host drains it on a
// ticker; if the host never drains (a DLL loaded by something that does not
// poll), the queue must not grow without bound for the process lifetime.
const maxPendingCompletions = 256

// bridgeFetchTimeout bounds one bridge_fetch. The ABI call is synchronous, so a
// hung DHT peer would otherwise pin an HTTP handler goroutine forever: the host
// cannot cancel across the boundary, the runtime must bound itself.
const bridgeFetchTimeout = 90 * time.Second

// Config configures the BT runtime. It arrives as JSON from the host, so every
// field is tagged and optional except DataDir.
type Config struct {
	// DataDir is the torrent data directory (PEERDRIVE_DOWNLOAD_DIR).
	DataDir string `json:"data_dir"`

	// ListenAddr is the BT peer listen address (":0" = random port).
	// Empty keeps the library default.
	ListenAddr string `json:"listen_addr,omitempty"`

	// StorageDir is the peerdrive content-addressed storage root. Required only
	// for the DHT bridge (bridge_* ops), which writes fetched files there.
	StorageDir string `json:"storage_dir,omitempty"`

	// DHTEnabled starts the mainline DHT server (BEP 5/44/51 + bridge).
	// Off by default: NewBTDHT bootstraps against public routers and sleeps 1s.
	DHTEnabled bool `json:"dht_enabled"`

	// DHTListenAddr is the UDP listen address for the DHT server (":6881").
	DHTListenAddr string `json:"dht_listen_addr,omitempty"`
}

// Completion is one download-complete event, queued for the host to drain.
type Completion struct {
	InfoHash string          `json:"infohash"`
	Files    []CompletedFile `json:"files"`
}

// MetaDTO is the ABI-friendly projection of p2p_bt.TorrentMeta.
//
// p2p_bt.TorrentMeta carries PiecesHex (one 40-char hex string per piece) with
// `json:"pieces,omitempty"`. Crossing the ABI, that field is pure overhead: a
// 100 GiB torrent at 256 KiB pieces is ~400k entries ≈ 16 MB of JSON, encoded
// and decoded on both sides of a synchronous call, purely so the HTTP handler
// can report a count. The DTO keeps the count and drops the payload.
type MetaDTO struct {
	Name         string               `json:"name"`
	PieceLength  int64                `json:"piece_length"`
	PieceCount   int                  `json:"piece_count"`
	Files        []p2p_bt.TorrentFile `json:"files"`
	TotalSize    int64                `json:"total_size"`
	InfoHashHex  string               `json:"infohash"`
	AnnounceList []string             `json:"announce_list,omitempty"`
	IsSingleFile bool                 `json:"is_single_file"`
}

func metaDTO(m *p2p_bt.TorrentMeta) *MetaDTO {
	if m == nil {
		return nil
	}
	return &MetaDTO{
		Name:         m.Name,
		PieceLength:  m.PieceLength,
		PieceCount:   len(m.PiecesHex),
		Files:        m.Files,
		TotalSize:    m.TotalSize,
		InfoHashHex:  m.InfoHashHex,
		AnnounceList: m.AnnounceList,
		IsSingleFile: m.IsSingleFile,
	}
}

// CompletedFile mirrors p2p_bt.CompletedFile across the ABI boundary.
type CompletedFile struct {
	InfoHash string `json:"infohash"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256,omitempty"`
}

// Status is the runtime's self-description, returned by the ping op. The host
// uses it to report "BT enabled but degraded" (e.g. DHT failed to bind).
type Status struct {
	Version       string `json:"version"`
	DataDir       string `json:"data_dir"`
	DHTEnabled    bool   `json:"dht_enabled"`
	DHTListenAddr string `json:"dht_listen_addr,omitempty"`
	DHTAvailable  bool   `json:"dht_available"`
	DHTError      string `json:"dht_error,omitempty"`
	StorageDir    string `json:"storage_dir,omitempty"`
}

// Service owns the BT runtime for one process.
type Service struct {
	cfg    Config
	client *p2p_bt.BTClient
	dht    *p2p_bt.BTDHTService
	bridge *p2p_bt.BTBridge

	// dhtErr records why the DHT is absent, so ops can answer with the reason
	// instead of a bare "unavailable".
	dhtErr string

	mu          sync.Mutex
	completions []Completion
	closed      bool
}

// New starts the BT runtime. It blocks while the torrent client is constructed
// (which fetches the public tracker list over the network) and, when
// DHTEnabled, while the DHT server bootstraps.
//
// A DHT failure is deliberately non-fatal: torrent downloads work without it,
// and the reason is surfaced in Status.DHTError so the host can log it once
// instead of failing the whole BT subsystem.
func New(cfg Config) (*Service, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("btservice: data_dir is required")
	}

	client := p2p_bt.NewBTClientWithListenAddr(cfg.DataDir, cfg.ListenAddr)
	if client == nil {
		return nil, errors.New("btservice: torrent client creation failed")
	}

	s := &Service{cfg: cfg, client: client}

	if cfg.DHTEnabled {
		dht, err := p2p_bt.NewBTDHT(cfg.DHTListenAddr)
		if err != nil {
			s.dhtErr = err.Error()
			p2p_bt.LogWarn("btservice: DHT unavailable, continuing without it: %v", err)
		} else {
			s.dht = dht
			p2p_bt.SetGlobalDHT(dht)
			if cfg.StorageDir != "" {
				s.bridge = p2p_bt.NewBTBridge(dht, cfg.StorageDir)
			}
		}
	}

	client.SetOnComplete(func(infohash string, files []p2p_bt.CompletedFile) {
		out := make([]CompletedFile, 0, len(files))
		for _, f := range files {
			out = append(out, CompletedFile{InfoHash: f.InfoHash, Path: f.Path, Size: f.Size, SHA256: f.SHA256})
		}
		s.mu.Lock()
		if !s.closed && len(s.completions) < maxPendingCompletions {
			s.completions = append(s.completions, Completion{InfoHash: infohash, Files: out})
		}
		s.mu.Unlock()
	})

	return s, nil
}

// Close tears the runtime down. Safe to call more than once.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	if s.dht != nil {
		_ = s.dht.Close()
	}
	if s.client != nil {
		s.client.Close()
	}
	return nil
}

// ---- Op dispatch ----

// Call executes one op. args is the raw JSON request (nil/empty = no arguments).
// The returned value must be JSON-marshalable; nil encodes as null.
//
// Errors are returned as-is; the ABI shim wraps them into the reply envelope so
// that a single decode path on the host covers both success and failure.
func (s *Service) Call(op string, args json.RawMessage) (any, error) {
	if s == nil {
		return nil, errors.New("bt service is not initialized")
	}

	switch op {
	// --- lifecycle / introspection ---
	case "ping":
		return s.status(), nil
	case "download_dir":
		return map[string]string{"dir": s.client.GetDownloadDir()}, nil
	case "drain_completions":
		return s.drainCompletions(), nil

	// --- torrent downloads ---
	case "add_torrent":
		var a struct {
			DataB64 string `json:"data_b64"`
		}
		if err := decodeArgs(args, &a); err != nil {
			return nil, err
		}
		raw, err := b64dec(a.DataB64)
		if err != nil {
			return nil, fmt.Errorf("add_torrent: %w", err)
		}
		meta, err := s.client.AddTorrentBytes(raw)
		if err != nil {
			return nil, err
		}
		return metaDTO(meta), nil

	case "add_magnet":
		var a struct {
			URI string `json:"uri"`
		}
		if err := decodeArgs(args, &a); err != nil {
			return nil, err
		}
		if a.URI == "" {
			return nil, errors.New("add_magnet: uri is required")
		}
		meta, err := s.client.AddMagnetURI(a.URI)
		if err != nil {
			return nil, err
		}
		return metaDTO(meta), nil

	case "get_download":
		infohash, err := infohashArg(args)
		if err != nil {
			return nil, err
		}
		return s.client.GetDownload(infohash), nil

	case "list_downloads":
		return s.client.ListDownloads(), nil

	case "pause":
		return s.voidOnInfohash(args, s.client.PauseDownload)
	case "resume":
		return s.voidOnInfohash(args, s.client.ResumeDownload)
	case "remove":
		return s.voidOnInfohash(args, s.client.RemoveDownload)
	case "start_seed":
		return s.voidOnInfohash(args, s.client.StartSeed)
	case "stop_seed":
		return s.voidOnInfohash(args, s.client.StopSeed)
	case "set_auto_seed":
		return s.voidOnInfohash(args, func(ih string) error {
			s.client.SetAutoSeed(ih)
			return nil
		})

	case "list_seeders":
		return s.client.ListSeeders(), nil

	case "global_stats":
		return s.client.GetGlobalStats(), nil

	case "torrent_bytes":
		infohash, err := infohashArg(args)
		if err != nil {
			return nil, err
		}
		raw, err := s.client.GetTorrentBytes(infohash)
		if err != nil {
			return nil, err
		}
		return map[string]string{"data_b64": base64.StdEncoding.EncodeToString(raw)}, nil

	case "magnet_uri":
		infohash, err := infohashArg(args)
		if err != nil {
			return nil, err
		}
		return map[string]string{"uri": s.client.GetMagnetURI(infohash)}, nil

	case "create_and_seed":
		var a struct {
			Path        string   `json:"path"`
			Name        string   `json:"name"`
			PieceLength int64    `json:"piece_length"`
			Announce    []string `json:"announce"`
		}
		if err := decodeArgs(args, &a); err != nil {
			return nil, err
		}
		if a.Path == "" {
			return nil, errors.New("create_and_seed: path is required")
		}
		meta, err := s.client.CreateAndSeed(p2p_bt.CreateTorrentOptions{
			Path:         a.Path,
			Name:         a.Name,
			PieceLength:  a.PieceLength,
			AnnounceList: a.Announce,
		})
		if err != nil {
			return nil, err
		}
		return metaDTO(meta), nil

	// --- mainline DHT (BEP 5) ---
	case "dht_status":
		return s.dhtStatus(), nil

	case "dht_announce":
		if err := s.requireDHT(); err != nil {
			return nil, err
		}
		var a hashArg
		if err := decodeArgs(args, &a); err != nil {
			return nil, err
		}
		return nil, s.dht.Announce(a.Hash)

	case "dht_find_providers":
		if err := s.requireDHT(); err != nil {
			return nil, err
		}
		var a hashArg
		if err := decodeArgs(args, &a); err != nil {
			return nil, err
		}
		return s.dht.FindProviders(a.Hash)

	// --- BEP 44 (immutable data) ---
	case "bep44_put":
		if err := s.requireDHT(); err != nil {
			return nil, err
		}
		var a struct {
			DataB64 string `json:"data_b64"`
		}
		if err := decodeArgs(args, &a); err != nil {
			return nil, err
		}
		raw, err := b64dec(a.DataB64)
		if err != nil {
			return nil, fmt.Errorf("bep44_put: %w", err)
		}
		target, err := s.dht.PutImmutable(raw)
		if err != nil {
			return nil, err
		}
		return map[string]string{"target": hex.EncodeToString(target[:])}, nil

	case "bep44_get":
		if err := s.requireDHT(); err != nil {
			return nil, err
		}
		var a struct {
			Target string `json:"target"`
		}
		if err := decodeArgs(args, &a); err != nil {
			return nil, err
		}
		target, err := decodeTarget(a.Target)
		if err != nil {
			return nil, err
		}
		raw, err := s.dht.GetImmutable(target)
		if err != nil {
			return nil, err
		}
		return map[string]string{"data_b64": base64.StdEncoding.EncodeToString(raw)}, nil

	// --- BEP 51 (infohash sampling) ---
	case "bep51_sample":
		if err := s.requireDHT(); err != nil {
			return nil, err
		}
		var a struct {
			Max int `json:"max"`
		}
		if err := decodeArgs(args, &a); err != nil {
			return nil, err
		}
		samples, err := s.dht.DiscoverInfohashes(a.Max)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(samples))
		for _, sample := range samples {
			out = append(out, hex.EncodeToString(sample[:]))
		}
		return out, nil

	// --- DHT HTTP bridge (peerdrive file sharing over DHT providers) ---
	case "bridge_share":
		if err := s.requireBridge(); err != nil {
			return nil, err
		}
		var a hashArg
		if err := decodeArgs(args, &a); err != nil {
			return nil, err
		}
		return nil, s.bridge.ShareFile(a.Hash)

	case "bridge_list_shared":
		if err := s.requireBridge(); err != nil {
			return nil, err
		}
		return s.bridge.ListShared()

	case "bridge_path":
		if err := s.requireBridge(); err != nil {
			return nil, err
		}
		var a hashArg
		if err := decodeArgs(args, &a); err != nil {
			return nil, err
		}
		return map[string]string{"path": s.bridge.FilePath(a.Hash)}, nil

	// bridge_fetch deliberately does NOT return the bytes: a bridge fetch can be
	// up to 512 MiB, and base64 across the ABI would triple the peak RSS of both
	// sides. The runtime verifies the SHA-256 and writes into storage itself
	// (EnsureFileWritten), then hands back the path; the host reads the file.
	case "bridge_fetch":
		if err := s.requireBridge(); err != nil {
			return nil, err
		}
		var a hashArg
		if err := decodeArgs(args, &a); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), bridgeFetchTimeout)
		defer cancel()
		raw, err := s.bridge.FetchFile(ctx, a.Hash)
		if err != nil {
			return nil, err
		}
		if err := s.bridge.EnsureFileWritten(a.Hash, raw); err != nil {
			return nil, err
		}
		path := s.bridge.FilePath(a.Hash)
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("bridge_fetch: written file missing: %w", err)
		}
		return map[string]any{"path": path, "size": len(raw)}, nil

	default:
		return nil, fmt.Errorf("unknown bt op %q", op)
	}
}

// ---- helpers ----

type hashArg struct {
	Hash string `json:"hash"`
}

func decodeArgs(raw json.RawMessage, dst any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

func infohashArg(raw json.RawMessage) (string, error) {
	var a struct {
		InfoHash string `json:"infohash"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return "", err
	}
	if a.InfoHash == "" {
		return "", errors.New("infohash is required")
	}
	return a.InfoHash, nil
}

func (s *Service) voidOnInfohash(raw json.RawMessage, fn func(string) error) (any, error) {
	infohash, err := infohashArg(raw)
	if err != nil {
		return nil, err
	}
	if err := fn(infohash); err != nil {
		return nil, err
	}
	return map[string]string{"infohash": infohash}, nil
}

func (s *Service) requireDHT() error {
	if s.dht == nil {
		if s.dhtErr != "" {
			return fmt.Errorf("BT DHT unavailable: %s", s.dhtErr)
		}
		return errors.New("BT DHT is not enabled (set PEERDRIVE_BT_DHT_ENABLE=1)")
	}
	return nil
}

func (s *Service) requireBridge() error {
	if err := s.requireDHT(); err != nil {
		return err
	}
	if s.bridge == nil {
		return errors.New("BT DHT bridge unavailable: storage_dir is not configured")
	}
	return nil
}

func (s *Service) status() Status {
	return Status{
		Version:       "1",
		DataDir:       s.cfg.DataDir,
		DHTEnabled:    s.cfg.DHTEnabled,
		DHTListenAddr: s.cfg.DHTListenAddr,
		DHTAvailable:  s.dht != nil,
		DHTError:      s.dhtErr,
		StorageDir:    s.cfg.StorageDir,
	}
}

func (s *Service) dhtStatus() map[string]any {
	out := map[string]any{
		"available":   s.dht != nil,
		"listen_addr": "",
		"num_nodes":   0,
		"node_id":     "",
	}
	if s.dhtErr != "" {
		out["error"] = s.dhtErr
	}
	if s.dht != nil {
		if s.dht.Server != nil {
			out["listen_addr"] = s.dht.Server.Addr().String()
			out["num_nodes"] = s.dht.NumNodes()
		}
		out["node_id"] = hex.EncodeToString(s.dht.NodeID[:])
	}
	return out
}

func (s *Service) drainCompletions() []Completion {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.completions) == 0 {
		return []Completion{}
	}
	out := s.completions
	s.completions = nil
	return out
}

func b64dec(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid base64 payload: %w", err)
	}
	return raw, nil
}

// decodeTarget parses a 20-byte BEP 44 target given as 40 hex characters.
func decodeTarget(s string) ([20]byte, error) {
	var out [20]byte
	if len(s) != 40 {
		return out, fmt.Errorf("invalid target length %d (want 40 hex chars)", len(s))
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return out, fmt.Errorf("invalid target hex: %w", err)
	}
	copy(out[:], raw)
	return out, nil
}
