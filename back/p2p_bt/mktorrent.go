// mktorrent.go — torrent creation API.
//
// 发现背景：用户需要「制作种子」功能——从本地文件/目录生成 .torrent。
// 实现依托 anacrolix/torrent v1.61.0 的 metainfo.Info.BuildFromFilePath
// （walk 目录 → 设置 Name/Files/Length → ChoosePieceLength 自动选块长 →
// GeneratePieces SHA1 逐块哈希）与 metainfo.MetaInfo.Write（bencode 编码）。
// 库内无独立 Create 函数，bt_test.go 的 createTestTorrentFile helper 已验证此路径。
package p2p_bt

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// CreateTorrentOptions configures torrent creation from a local file or directory.
type CreateTorrentOptions struct {
	// Path is the root file or directory to hash.
	Path string

	// PieceLength is the piece size in bytes. Zero means auto-select via
	// metainfo.ChoosePieceLength (power of 2, ≥16KB, targets ~1024-2048 pieces).
	PieceLength int64

	// AnnounceList is a tier-1 announce list (one entry per tracker URL).
	// When empty and Announce is set, it is used as a single-tier list.
	AnnounceList []string

	// Announce is a single tracker URL. Used as fallback when AnnounceList is empty.
	Announce string

	// Name overrides the info.name field. Empty means filepath.Base(Path).
	Name string

	// CreatedBy sets the "created by" string. Empty means "peerdrive".
	CreatedBy string

	// Comment is an optional free-text comment field.
	Comment string

	// Private marks the torrent as BEP27 private (no DHT/tracker cross-contamination).
	Private bool
}

// CreateTorrentResult holds the result of a successful torrent creation.
type CreateTorrentResult struct {
	// Torrent is the raw .torrent file bytes, ready for AddTorrentBytes or file writing.
	Torrent []byte

	// Meta is the parsed torrent metadata (infohash, name, files, pieces).
	Meta *TorrentMeta

	// InfoHash is the 40-character hex-encoded infohash.
	InfoHash string
}

// CreateTorrent builds a .torrent file from a local file or directory.
// The resulting torrent is NOT added to the client; use AddTorrentBytes to start
// downloading, or CreateAndSeed to add and begin seeding in one step.
func (c *BTClient) CreateTorrent(opts CreateTorrentOptions) (*CreateTorrentResult, error) {
	mi, info, err := buildMetaInfo(opts)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		return nil, fmt.Errorf("encode torrent: %w", err)
	}

	meta := metaFromInfo(info, mi.HashInfoBytes().HexString(), mi.UpvertedAnnounceList())
	return &CreateTorrentResult{
		Torrent:  buf.Bytes(),
		Meta:     meta,
		InfoHash: meta.InfoHashHex,
	}, nil
}

// CreateTorrentToFile builds a .torrent from local files and writes it to path.
// The .torrent file at path contains announce URLs from opts, is immediately
// shareable, and can be loaded with AddTorrentBytes or by any BT client.
func (c *BTClient) CreateTorrentToFile(opts CreateTorrentOptions, path string) (*TorrentMeta, error) {
	result, err := c.CreateTorrent(opts)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create torrent dir: %w", err)
	}
	if err := os.WriteFile(path, result.Torrent, 0644); err != nil {
		return nil, fmt.Errorf("write torrent file: %w", err)
	}
	return result.Meta, nil
}

// CreateAndSeed creates a torrent from local files, copies the data into the BT
// data directory, adds the torrent to the client, and enables auto-seeding.
//
// Because the data is already present on disk, the library will verify it on add
// and mark the torrent complete immediately, then begin serving to peers (seed).
// This is the "availability" path: the created torrent is immediately findable
// via ListDownloads and is actively uploading to peers.
func (c *BTClient) CreateAndSeed(opts CreateTorrentOptions) (*TorrentMeta, error) {
	result, err := c.CreateTorrent(opts)
	if err != nil {
		return nil, err
	}

	torrentName := result.Meta.Name
	if torrentName == "" {
		torrentName = result.InfoHash
	}

	// Copy source data into the BT data directory so the library can verify it.
	dataRoot := filepath.Join(c.dataDir, torrentName)
	if err := copyTorrentData(opts.Path, dataRoot); err != nil {
		return nil, fmt.Errorf("copy data for seeding: %w", err)
	}

	// Ensure auto-seed is set before adding (AddTorrentBytes → watchDownload → finalizeDownload).
	c.mu.Lock()
	c.autoSeed[result.InfoHash] = true
	c.mu.Unlock()

	// Add the torrent. The library will verify the pre-placed data and complete immediately.
	meta, err := c.AddTorrentBytes(result.Torrent)
	if err != nil {
		return nil, err
	}
	return meta, nil
}

// ---- Internal helpers ----

// buildMetaInfo constructs a metainfo.MetaInfo from a local path with options.
// It uses metainfo.Info.BuildFromFilePath to walk the directory, set file layout,
// auto-select piece length, and generate SHA1 piece hashes.
func buildMetaInfo(opts CreateTorrentOptions) (*metainfo.MetaInfo, *metainfo.Info, error) {
	root := opts.Path
	if root == "" {
		return nil, nil, fmt.Errorf("path is required")
	}

	if _, err := os.Stat(root); err != nil {
		return nil, nil, fmt.Errorf("stat %s: %w", root, err)
	}

	info := &metainfo.Info{
		PieceLength: opts.PieceLength,
	}

	if opts.Private {
		info.Private = &opts.Private
	}

	// BuildFromFilePath walks the root path, sets Name/Files/Length,
	// auto-selects PieceLength if zero, and generates SHA1 piece hashes.
	if err := info.BuildFromFilePath(root); err != nil {
		return nil, nil, fmt.Errorf("build info from %s: %w", root, err)
	}

	// Override name if specified.
	if opts.Name != "" {
		info.Name = opts.Name
	}

	// Validate: at least one file or non-zero length.
	if info.TotalLength() == 0 {
		return nil, nil, fmt.Errorf("empty torrent: no files found under %s", root)
	}

	// Marshal info to bencode for the MetaInfo.InfoBytes field.
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		return nil, nil, fmt.Errorf("bencode info: %w", err)
	}

	// Build the MetaInfo container.
	mi := &metainfo.MetaInfo{
		InfoBytes: infoBytes,
	}

	// Set announce: AnnounceList takes precedence; Announce is fallback.
	if len(opts.AnnounceList) > 0 {
		mi.AnnounceList = metainfo.AnnounceList{opts.AnnounceList}
	} else if opts.Announce != "" {
		mi.Announce = opts.Announce
	}

	// CreatedBy.
	if opts.CreatedBy != "" {
		mi.CreatedBy = opts.CreatedBy
	} else {
		mi.SetDefaults()
	}

	// Comment.
	mi.Comment = opts.Comment

	return mi, info, nil
}

// copyTorrentData copies a file or directory tree from src into dataRoot.
// If src is a file, it is placed at dataRoot (the torrent name IS the file for
// single-file torrents). If src is a directory, its contents are copied into
// dataRoot (the torrent name is the directory name for multi-file torrents).
func copyTorrentData(src, dataRoot string) error {
	st, err := os.Stat(src)
	if err != nil {
		return err
	}

	if st.IsDir() {
		// Multi-file: copy directory contents into dataRoot.
		return filepath.Walk(src, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if fi.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}
			dst := filepath.Join(dataRoot, rel)
			return copyFile(path, dst, fi.Mode())
		})
	}

	// Single-file: the file IS the torrent name.
	// dataRoot is already the target path (name of the file).
	return copyFile(src, dataRoot, st.Mode())
}

// copyFile copies a single file, creating parent directories as needed.
func copyFile(src, dst string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm.Perm())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

// metaFromInfo converts a metainfo.Info, infohash hex, and announce list
// into a TorrentMeta.
func metaFromInfo(info *metainfo.Info, infohashHex string, announceList metainfo.AnnounceList) *TorrentMeta {
	meta := &TorrentMeta{
		Name:        info.BestName(),
		PieceLength: info.PieceLength,
		TotalSize:   info.TotalLength(),
		InfoHashHex: infohashHex,
	}

	// Extract announce URLs.
	for _, tier := range announceList {
		for _, url := range tier {
			meta.AnnounceList = append(meta.AnnounceList, url)
		}
	}

	if len(info.Files) == 0 {
		meta.IsSingleFile = true
		meta.Files = []TorrentFile{{Path: info.BestName(), Size: info.Length}}
	} else {
		meta.Files = make([]TorrentFile, len(info.Files))
		for i, f := range info.Files {
			meta.Files[i] = TorrentFile{
				Path: strings.Join(f.BestPath(), "/"),
				Size: f.Length,
			}
		}
	}

	// Extract piece hashes.
	numPieces := info.NumPieces()
	meta.Pieces = make([][]byte, numPieces)
	meta.PiecesHex = make([]string, numPieces)
	for i := 0; i < numPieces; i++ {
		hash := info.Pieces[i*20 : i*20+20]
		meta.Pieces[i] = append([]byte(nil), hash...)
		meta.PiecesHex[i] = hashStr(hash)
	}

	return meta
}

// hashStr hex-encodes a SHA1 hash. Kept local to avoid importing encoding/hex
// in the hot path (client.go already imports it; metaFromInfo is in mktorrent.go).
func hashStr(b []byte) string {
	const hexChars = "0123456789abcdef"
	dst := make([]byte, len(b)*2)
	for i, c := range b {
		dst[i*2] = hexChars[c>>4]
		dst[i*2+1] = hexChars[c&0x0f]
	}
	return string(dst)
}
