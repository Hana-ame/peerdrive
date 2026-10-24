package p2p_bt

import (
	"encoding/hex"
	"io"
	"os"
	"strings"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// ---- File access ----

func (c *BTClient) ListDownloadFiles(infohash string) ([]CompletedFile, bool) {
	c.mu.RLock()
	ds, ok := c.downloads[infohash]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return ds.files, ds.status == "completed"
}

func (c *BTClient) ReadFileReader(filePath string) (io.ReadCloser, error) {
	return os.Open(filePath)
}

func (c *BTClient) GetDownloadDir() string {
	return c.dataDir
}

// ---- Helper functions ----

// torrentMetaFromLibrary extracts TorrentMeta from library types (used for HTTP API responses).
func torrentMetaFromLibrary(mi *metainfo.MetaInfo, t *torrent.Torrent) *TorrentMeta {
	info := t.Info()
	if info == nil {
		meta := &TorrentMeta{
			InfoHashHex: mi.HashInfoBytes().HexString(),
		}
		// Try to parse Info to get the name
		if parsed, err := mi.UnmarshalInfo(); err == nil {
			meta.Name = parsed.BestName()
		}
		return meta
	}

	meta := &TorrentMeta{
		Name:        info.Name,
		PieceLength: info.PieceLength,
		TotalSize:   info.TotalLength(),
		InfoHashHex: mi.HashInfoBytes().HexString(),
		InfoHash:    mi.HashInfoBytes().Bytes(),
	}

	if len(info.Files) == 0 {
		meta.IsSingleFile = true
		meta.Files = []TorrentFile{{Path: info.Name, Size: info.Length}}
	} else {
		for _, f := range info.Files {
			path := strings.Join(f.Path, "/")
			meta.Files = append(meta.Files, TorrentFile{Path: path, Size: f.Length})
		}
	}

	// Extract Announce URLs
	for _, tier := range mi.UpvertedAnnounceList() {
		meta.AnnounceList = append(meta.AnnounceList, tier...)
	}

	// Extract piece hashes (one SHA1 per 20 bytes)
	numPieces := len(info.Pieces) / 20
	meta.Pieces = make([][]byte, numPieces)
	meta.PiecesHex = make([]string, numPieces)
	for i := 0; i < numPieces; i++ {
		hash := info.Pieces[i*20 : i*20+20]
		meta.Pieces[i] = hash
		meta.PiecesHex[i] = hex.EncodeToString(hash)
	}

	return meta
}

// metaFromTorrent extracts TorrentMeta from a completed Torrent object.
func metaFromTorrent(t *torrent.Torrent) *TorrentMeta {
	info := t.Info()
	if info == nil {
		return nil
	}

	meta := &TorrentMeta{
		Name:        info.Name,
		PieceLength: info.PieceLength,
		TotalSize:   info.TotalLength(),
		InfoHashHex: t.InfoHash().HexString(),
		InfoHash:    t.InfoHash().Bytes(),
	}

	if len(info.Files) == 0 {
		meta.IsSingleFile = true
		meta.Files = []TorrentFile{{Path: info.Name, Size: info.Length}}
	} else {
		for _, f := range info.Files {
			path := strings.Join(f.Path, "/")
			meta.Files = append(meta.Files, TorrentFile{Path: path, Size: f.Length})
		}
	}

	numPieces := len(info.Pieces) / 20
	meta.Pieces = make([][]byte, numPieces)
	meta.PiecesHex = make([]string, numPieces)
	for i := 0; i < numPieces; i++ {
		hash := info.Pieces[i*20 : i*20+20]
		meta.Pieces[i] = hash
		meta.PiecesHex[i] = hex.EncodeToString(hash)
	}

	return meta
}
