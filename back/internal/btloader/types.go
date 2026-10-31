// Package btloader is the host-side half of the BitTorrent runtime split.
//
// 发现背景（Issue 后续）：torrent 栈原先被主程序静态链接，且 router 无条件
// 创建客户端 —— 每个进程启动都会开 BT 监听端口、跑 mainline DHT、联网拉公共
// tracker，而 README 承诺的是「off by default」。要真正兑现「按需加载」，
// 运行时必须搬出主进程：
//
//	主程序（本包）  ==  JSON 客户端 + 一个 LoadLibrary
//	peerdrive_bt.dll == p2p_bt/btservice 的 C ABI 包装（cmd/btbridge）
//
// 本包不含任何 torrent 依赖：跨边界只传 JSON。因此 back/go.mod 里不再需要
// github.com/anacrolix/torrent，OSV「直接依赖 × 有修复版本」这一层硬门禁
// 也就不会被 BT 栈的 CVE 反复点亮。
//
// 非 Windows 平台没有 DLL 形态（Go 的 c-shared 在 Linux 上是 .so，进程内再
// dlopen 一个 Go 运行时不可靠），因此本包在非 Windows 上只提供 ErrUnavailable：
// 服务照常启动，/bt/* 返回 503 并带明确原因。
package btloader

import "errors"

// ErrUnavailable is returned when the BT runtime cannot be loaded: not Windows,
// DLL missing, or exports missing.
var ErrUnavailable = errors.New("BitTorrent runtime is unavailable")

// TorrentFile mirrors p2p_bt.TorrentFile.
type TorrentFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// TorrentMeta mirrors p2p_bt/btservice.MetaDTO.
//
// PieceCount replaces the library's PiecesHex: the piece hashes never cross the
// boundary (see btservice.MetaDTO), only their count.
type TorrentMeta struct {
	Name         string        `json:"name"`
	PieceLength  int64         `json:"piece_length"`
	PieceCount   int           `json:"piece_count"`
	Files        []TorrentFile `json:"files"`
	TotalSize    int64         `json:"total_size"`
	InfoHash     string        `json:"infohash"`
	AnnounceList []string      `json:"announce_list,omitempty"`
	IsSingleFile bool          `json:"is_single_file"`
}

// DownloadStatus mirrors p2p_bt.DownloadStatus.
type DownloadStatus struct {
	InfoHash    string  `json:"infohash"`
	Name        string  `json:"name"`
	TotalSize   int64   `json:"total_size"`
	Downloaded  int64   `json:"downloaded"`
	PiecesTotal int     `json:"pieces_total"`
	PiecesDone  int     `json:"pieces_done"`
	Peers       int     `json:"peers"`
	Speed       float64 `json:"speed_bytes_per_sec"`
	Status      string  `json:"status"`
	Error       string  `json:"error,omitempty"`
	Seeding     bool    `json:"seeding"`
}

// CompletedFile mirrors p2p_bt.CompletedFile.
type CompletedFile struct {
	InfoHash string `json:"infohash"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256,omitempty"`
}

// GlobalStats mirrors p2p_bt.GlobalStats.
type GlobalStats struct {
	TotalUp        int64 `json:"total_up_bytes"`
	TotalDown      int64 `json:"total_down_bytes"`
	ActiveTorrents int   `json:"active_torrents"`
	PausedTorrents int   `json:"paused_torrents"`
	Completed      int   `json:"completed"`
	Errors         int   `json:"errors"`
	DHTNodes       int   `json:"dht_nodes"`
}

// DHTStatus is the reply of the dht_status op.
type DHTStatus struct {
	Available  bool   `json:"available"`
	ListenAddr string `json:"listen_addr"`
	NumNodes   int    `json:"num_nodes"`
	NodeID     string `json:"node_id"`
	Error      string `json:"error,omitempty"`
}

// Status is the reply of the ping op: what the runtime thinks it is.
type Status struct {
	Version       string `json:"version"`
	DataDir       string `json:"data_dir"`
	DHTEnabled    bool   `json:"dht_enabled"`
	DHTListenAddr string `json:"dht_listen_addr,omitempty"`
	DHTAvailable  bool   `json:"dht_available"`
	DHTError      string `json:"dht_error,omitempty"`
	StorageDir    string `json:"storage_dir,omitempty"`
}

// CreateAndSeedOptions mirrors the subset of p2p_bt.CreateTorrentOptions that
// the host can supply. The runtime builds the metainfo and copies the data into
// its own data dir, so the host never needs torrent's metainfo package.
type CreateAndSeedOptions struct {
	Path        string
	Name        string
	PieceLength int64
	Announce    []string
}
