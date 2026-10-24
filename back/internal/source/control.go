package source

// control.go: Source control plane (optional capability).
//
// The read plane (Source interface) only answers "give me the byte stream for a hash";
// the control plane answers "how to add/write files to this source." Control planes are
// split by capability; not every source must implement one.
// Discovery background: 2026-08-19 control plane design (doc/source-control.md) -- users need
// local to add local files/write directly, BT to download torrents, IPFS to serve/pin.

import (
	"errors"
	"io"
)

// ErrControlUnsupported indicates the current source does not implement the corresponding control capability.
var ErrControlUnsupported = errors.New("source does not support this control operation")

// LocalControl is the control capability for local file sources.
type LocalControl interface {
	// AddLocalFile adds an existing local file to the source: computes hash, registers index.
	AddLocalFile(path string) (*FileMeta, error)

	// WriteFile writes a file directly: reads from a reader, computes hash and registers after completion.
	WriteFile(name string, r io.Reader) (*FileMeta, error)
}

// LocalControlOf returns the LocalControl implementation of a source; ok=false if not supported.
func LocalControlOf(s Source) (LocalControl, bool) {
	lc, ok := s.(LocalControl)
	return lc, ok
}

// BTControl is the control capability for torrent downloads.
type BTControl interface {
	// DownloadTorrent starts a download from .torrent file bytes, returning metadata.
	DownloadTorrent(data []byte) (*TorrentMeta, error)

	// DownloadMagnet starts a download from a magnet URI, returning metadata.
	DownloadMagnet(uri string) (*TorrentMeta, error)

	// ListDownloads lists all download task statuses.
	ListDownloads() []DownloadStatus

	// GetDownload queries the download status of a specified infohash.
	GetDownload(infohash string) *DownloadStatus

	// PauseDownload pauses a download.
	PauseDownload(infohash string) error

	// ResumeDownload resumes a download.
	ResumeDownload(infohash string) error

	// RemoveDownload removes a download task (including downloaded data).
	RemoveDownload(infohash string) error
}

// TorrentMeta is torrent metadata.
type TorrentMeta struct {
	InfoHash  string `json:"infohash"`
	Name      string `json:"name"`
	TotalSize int64  `json:"total_size"`
	Files     int    `json:"files"`
}

// DownloadStatus is the download task status.
type DownloadStatus struct {
	InfoHash      string  `json:"infohash"`
	Name          string  `json:"name"`
	Status        string  `json:"status"` // downloading / paused / completed / error / seeding
	BytesDone     int64   `json:"bytes_done"`
	BytesTotal    int64   `json:"bytes_total"`
	Peers         int     `json:"peers"`
	Seeders       int     `json:"seeders"`
	Progress      float64
	DownloadSpeed float64
	ErrorMessage  string  `json:"error_message,omitempty"`
}

// IPFSControl is the control capability for IPFS.
type IPFSControl interface {
	// PinCID downloads a CID and caches it to local storage, returning meta.
	PinCID(cid string) (*PinInfo, error)

	// UnpinCID removes a pinned CID.
	UnpinCID(cid string) error

	// ListPins lists all pinned CIDs.
	ListPins() ([]PinInfo, error)

	// GatewayStatus returns the health status of each gateway.
	GatewayStatus() ([]GatewayStatus, error)
}

// PinInfo is pin entry information.
type PinInfo struct {
	CID      string `json:"cid"`
	Hash     string `json:"hash,omitempty"`
	Filename string `json:"filename,omitempty"`
	Size     int64  `json:"size"`
	PinnedAt string `json:"pinned_at,omitempty"`
}

// GatewayStatus is the gateway status.
type GatewayStatus struct {
	URL     string `json:"url"`
	Online  bool   `json:"online"`
	Latency string `json:"latency,omitempty"`
}
