package source

// bt_control.go: BTControl implementation -- wraps p2p_bt.BTClient.
// Discovery background: Source control plane design (doc/source-control.md), BT active download of torrent/magnet.

import (
	pp "github.com/Hana-ame/go-peerdrive-bt"
	"peerdrive/internal/log"
)

// btController is the wrapper implementation of BTControl.
type btController struct {
	inner *pp.BTClient
}

// NewBTControl creates a BT control plane instance (nil is acceptable; all methods return ErrControlUnsupported).
func NewBTControl(client *pp.BTClient) BTControl {
	return &btController{inner: client}
}

func (b *btController) DownloadTorrent(data []byte) (*TorrentMeta, error) {
	if b.inner == nil {
		return nil, ErrControlUnsupported
	}
	meta, err := b.inner.AddTorrentBytes(data)
	if err != nil {
		return nil, err
	}
	log.LogInfo("source/bt: download torrent name=%s infohash=%s", meta.Name, meta.InfoHashHex)
	return &TorrentMeta{
		InfoHash:  meta.InfoHashHex,
		Name:      meta.Name,
		TotalSize: meta.TotalSize,
		Files:     len(meta.Files),
	}, nil
}

func (b *btController) DownloadMagnet(uri string) (*TorrentMeta, error) {
	if b.inner == nil {
		return nil, ErrControlUnsupported
	}
	meta, err := b.inner.AddMagnetURI(uri)
	if err != nil {
		return nil, err
	}
	log.LogInfo("source/bt: download magnet uri=%s infohash=%s", uri, meta.InfoHashHex)
	return &TorrentMeta{
		InfoHash:  meta.InfoHashHex,
		Name:      meta.Name,
		TotalSize: meta.TotalSize,
		Files:     len(meta.Files),
	}, nil
}

func (b *btController) ListDownloads() []DownloadStatus {
	if b.inner == nil {
		return nil
	}
	list := b.inner.ListDownloads()
	out := make([]DownloadStatus, len(list))
	for i, s := range list {
		out[i] = downloadStatusFromP2p(s)
	}
	return out
}

func (b *btController) GetDownload(infohash string) *DownloadStatus {
	if b.inner == nil {
		return nil
	}
	s := b.inner.GetDownload(infohash)
	if s == nil {
		return nil
	}
	ds := downloadStatusFromP2p(*s)
	return &ds
}

func (b *btController) PauseDownload(infohash string) error {
	if b.inner == nil {
		return ErrControlUnsupported
	}
	return b.inner.PauseDownload(infohash)
}

func (b *btController) ResumeDownload(infohash string) error {
	if b.inner == nil {
		return ErrControlUnsupported
	}
	return b.inner.ResumeDownload(infohash)
}

func (b *btController) RemoveDownload(infohash string) error {
	if b.inner == nil {
		return ErrControlUnsupported
	}
	return b.inner.RemoveDownload(infohash)
}

// --- Helpers ---

func downloadStatusFromP2p(s pp.DownloadStatus) DownloadStatus {
	return DownloadStatus{
		InfoHash:      s.InfoHash,
		Name:          s.Name,
		Status:        s.Status,
		BytesDone:     s.Downloaded,
		BytesTotal:    s.TotalSize,
		Peers:         s.Peers,
		Seeders:       0, // p2p_bt.DownloadStatus does not have a Seeders field
		Progress:      float64(s.PiecesDone) / float64(max(s.PiecesTotal, 1)),
		DownloadSpeed: s.Speed,
		ErrorMessage:  s.Error,
	}
}
