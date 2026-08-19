package source

// bt_control.go：BTControl 实现——包装 p2p_bt.BTClient。
// 发现背景：Source 控制面设计（doc/source-control.md），BT 主动下载 torrent/magnet。

import (
	pp "github.com/Hana-ame/go-peerdrive-bt"
	"peerdrive/internal/log"
)

// btController 是 BTControl 的封装实现。
type btController struct {
	inner *pp.BTClient
}

// NewBTControl 创建 BT 控制面实例（nil 可用，所有方法返回 ErrControlUnsupported）。
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

// ---- 辅助 ----

func downloadStatusFromP2p(s pp.DownloadStatus) DownloadStatus {
	return DownloadStatus{
		InfoHash:      s.InfoHash,
		Name:          s.Name,
		Status:        s.Status,
		BytesDone:     s.Downloaded,
		BytesTotal:    s.TotalSize,
		Peers:         s.Peers,
		Seeders:       0, // p2p_bt.DownloadStatus 没有 Seeders 字段
		Progress:      float64(s.PiecesDone) / float64(max(s.PiecesTotal, 1)),
		DownloadSpeed: s.Speed,
		ErrorMessage:  s.Error,
	}
}
