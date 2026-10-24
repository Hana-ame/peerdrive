// Package p2p_bt — shared types for BitTorrent integration, extracted to avoid
// circular dependencies and kept stable for external consumers.
package p2p_bt

// TorrentFile represents a single file entry in a torrent.
type TorrentFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// TorrentMeta holds parsed metadata from a .torrent file or magnet URI.
// Used in HTTP API responses; internally the library's metainfo types are used.
type TorrentMeta struct {
	Name         string        `json:"name"`
	PieceLength  int64         `json:"piece_length"`
	Pieces       [][]byte      `json:"-"`
	PiecesHex    []string      `json:"pieces,omitempty"`
	Files        []TorrentFile `json:"files"`
	TotalSize    int64         `json:"total_size"`
	InfoHash     []byte        `json:"-"`
	InfoHashHex  string        `json:"infohash"`
	AnnounceList []string      `json:"announce_list,omitempty"`
	IsSingleFile bool          `json:"is_single_file"`
}

// MagnetInfo holds parsed data from a BitTorrent magnet URI.
type MagnetInfo struct {
	InfoHash    string   `json:"infohash"`
	InfoHashRaw []byte   `json:"-"`
	DisplayName string   `json:"display_name,omitempty"`
	Trackers    []string `json:"trackers,omitempty"`
}

// DownloadStatus represents the current state of a torrent download.
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

// CompletedFile holds information about a completed torrent file.
type CompletedFile struct {
	InfoHash string `json:"infohash"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256,omitempty"`
}

// OnTorrentComplete is a callback invoked when a torrent finishes downloading.
type OnTorrentComplete func(infohash string, files []CompletedFile)

// GlobalStats holds global BitTorrent client statistics.
type GlobalStats struct {
	TotalUp        int64 `json:"total_up_bytes"`
	TotalDown      int64 `json:"total_down_bytes"`
	ActiveTorrents int   `json:"active_torrents"`
	PausedTorrents int   `json:"paused_torrents"`
	Completed      int   `json:"completed"`
	Errors         int   `json:"errors"`
	DHTNodes       int   `json:"dht_nodes"`
}
