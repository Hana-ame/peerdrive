// Package p2p_bt implements BitTorrent protocol support for Peerdrive,
// including torrent file parsing, magnet link resolution, and piece exchange.
package p2p_bt

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"

	"peerdrive/internal/log"
)

// TorrentFile represents a single file entry in a torrent.
type TorrentFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// TorrentMeta holds the parsed metadata from a .torrent file.
type TorrentMeta struct {
	Name         string        `json:"name"`
	PieceLength  int64         `json:"piece_length"`
	Pieces       [][]byte      `json:"-"` // parsed SHA1 hashes (20 bytes each)
	PiecesHex    []string      `json:"pieces,omitempty"`
	Files        []TorrentFile `json:"files"`
	TotalSize    int64         `json:"total_size"`
	InfoHash     []byte        `json:"-"` // 20-byte infohash
	InfoHashHex  string        `json:"infohash"`
	AnnounceList []string      `json:"announce_list,omitempty"`
	IsSingleFile bool          `json:"is_single_file"`
}

// --- Bencode decoder ---

type bval struct {
	intVal int64
	strVal string
	list   []bval
	dict   map[string]bval
	isInt  bool
	isStr  bool
	isList bool
	isDict bool
}

func bdecode(data []byte) (bval, int, error) {
	if len(data) == 0 {
		return bval{}, 0, fmt.Errorf("bencode: empty input")
	}
	switch {
	case data[0] == 'i':
		end := bytes.IndexByte(data, 'e')
		if end < 0 {
			return bval{}, 0, fmt.Errorf("bencode: unterminated integer")
		}
		n, err := strconv.ParseInt(string(data[1:end]), 10, 64)
		if err != nil {
			return bval{}, 0, fmt.Errorf("bencode: bad integer: %w", err)
		}
		return bval{intVal: n, isInt: true}, end + 1, nil

	case data[0] >= '0' && data[0] <= '9':
		colon := bytes.IndexByte(data, ':')
		if colon < 0 {
			return bval{}, 0, fmt.Errorf("bencode: unterminated string length")
		}
		n, err := strconv.Atoi(string(data[:colon]))
		if err != nil || n < 0 {
			return bval{}, 0, fmt.Errorf("bencode: bad string length: %w", err)
		}
		if colon+1+n > len(data) {
			return bval{}, 0, fmt.Errorf("bencode: string length %d exceeds data", n)
		}
		return bval{strVal: string(data[colon+1 : colon+1+n]), isStr: true}, colon + 1 + n, nil

	case data[0] == 'l':
		pos := 1
		var lst []bval
		for pos < len(data) && data[pos] != 'e' {
			v, n, err := bdecode(data[pos:])
			if err != nil {
				return bval{}, 0, err
			}
			lst = append(lst, v)
			pos += n
		}
		if pos >= len(data) {
			return bval{}, 0, fmt.Errorf("bencode: unterminated list")
		}
		return bval{list: lst, isList: true}, pos + 1, nil

	case data[0] == 'd':
		pos := 1
		dict := make(map[string]bval)
		for pos < len(data) && data[pos] != 'e' {
			k, n, err := bdecode(data[pos:])
			if err != nil {
				return bval{}, 0, err
			}
			if !k.isStr {
				return bval{}, 0, fmt.Errorf("bencode: dict key not a string")
			}
			pos += n
			v, n, err := bdecode(data[pos:])
			if err != nil {
				return bval{}, 0, err
			}
			dict[k.strVal] = v
			pos += n
		}
		if pos >= len(data) {
			return bval{}, 0, fmt.Errorf("bencode: unterminated dict")
		}
		return bval{dict: dict, isDict: true}, pos + 1, nil

	default:
		return bval{}, 0, fmt.Errorf("bencode: unexpected byte 0x%02x", data[0])
	}
}

func bdecodeDict(data []byte) (map[string]bval, error) {
	v, n, err := bdecode(data)
	if err != nil {
		return nil, err
	}
	if !v.isDict {
		return nil, fmt.Errorf("bencode: expected dict")
	}
	if n != len(data) {
		return nil, fmt.Errorf("bencode: trailing data (%d bytes)", len(data)-n)
	}
	return v.dict, nil
}

func bdecodeBytes(data []byte) (map[string]bval, error) {
	return bdecodeDict(data)
}

// bval helpers
func (v bval) string() string     { return v.strVal }
func (v bval) int() int64         { return v.intVal }
func (v bval) bytes() []byte      { return []byte(v.strVal) }
func (v bval) listLen() int       { return len(v.list) }
func (v bval) listIdx(i int) bval { return v.list[i] }

// --- Torrent parsing ---

// ParseTorrent parses a bencoded .torrent file buffer into TorrentMeta.
func ParseTorrent(data []byte) (*TorrentMeta, error) {
	defer log.LogDuration("BT.ParseTorrent")()
	log.LogDebug("bt-torrent: ParseTorrent data=%d bytes", len(data))

	root, err := bdecodeBytes(data)
	if err != nil {
		log.LogError("bt-torrent: bencode decode failed: %v", err)
		return nil, fmt.Errorf("bencode decode: %w", err)
	}

	infoVal, ok := root["info"]
	if !ok || !infoVal.isDict {
		return nil, fmt.Errorf("torrent: missing or invalid info dict")
	}

	// Encode the raw info dict for infohash calculation.
	infoDictRaw := infoVal.dict
	infoBencoded := bencodeDict(infoDictRaw)

	// Compute infohash = SHA1 of bencoded info dict.
	ih := sha1.Sum(infoBencoded)

	meta := &TorrentMeta{
		InfoHash:    ih[:],
		InfoHashHex: hex.EncodeToString(ih[:]),
	}

	// Announce
	if a, ok := root["announce"]; ok && a.isStr {
		meta.AnnounceList = append(meta.AnnounceList, a.string())
	}

	// Announce-list (list of lists of strings)
	if al, ok := root["announce-list"]; ok && al.isList {
		for _, tier := range al.list {
			if tier.isList {
				for _, url := range tier.list {
					if url.isStr {
						meta.AnnounceList = append(meta.AnnounceList, url.string())
					}
				}
			}
		}
	}

	// Info fields
	if name, ok := infoDictRaw["name"]; ok && name.isStr {
		meta.Name = name.string()
	}

	if pl, ok := infoDictRaw["piece length"]; ok && pl.isInt {
		meta.PieceLength = pl.int()
	}

	// Pieces: concatenated 20-byte SHA1 hashes
	if pieces, ok := infoDictRaw["pieces"]; ok && pieces.isStr {
		raw := pieces.bytes()
		if len(raw)%20 != 0 {
			return nil, fmt.Errorf("torrent: pieces length %d not multiple of 20", len(raw))
		}
		numPieces := len(raw) / 20
		meta.Pieces = make([][]byte, numPieces)
		meta.PiecesHex = make([]string, numPieces)
		for i := 0; i < numPieces; i++ {
			h := raw[i*20 : (i+1)*20]
			meta.Pieces[i] = h
			meta.PiecesHex[i] = hex.EncodeToString(h)
		}
	}

	// Determine single-file vs multi-file
	if length, ok := infoDictRaw["length"]; ok && length.isInt {
		// Single-file
		meta.IsSingleFile = true
		meta.Files = []TorrentFile{{Path: meta.Name, Size: length.int()}}
		meta.TotalSize = length.int()
	} else if files, ok := infoDictRaw["files"]; ok && files.isList {
		// Multi-file
		for _, fv := range files.list {
			if !fv.isDict {
				continue
			}
			fd := fv.dict
			tf := TorrentFile{Size: fd["length"].int()}
			if pathV, ok := fd["path"]; ok && pathV.isList {
				var parts []string
				for _, p := range pathV.list {
					if p.isStr {
						parts = append(parts, p.string())
					}
				}
				tf.Path = meta.Name
				for _, p := range parts {
					tf.Path = tf.Path + "/" + p
				}
				// If no path parts, use name
				if len(parts) == 0 {
					tf.Path = meta.Name + "/unknown"
				}
			} else {
				tf.Path = meta.Name + "/unknown"
			}
			meta.Files = append(meta.Files, tf)
			meta.TotalSize += tf.Size
		}
	}

	log.LogInfo("bt-torrent: parsed %q infohash=%s files=%d size=%d pieces=%d",
		meta.Name, meta.InfoHashHex, len(meta.Files), meta.TotalSize, len(meta.Pieces))
	return meta, nil
}

// ParseTorrentFile reads and parses a .torrent file from disk.
func ParseTorrentFile(path string) (*TorrentMeta, error) {
	defer log.LogDuration("BT.ParseTorrentFile")()
	log.LogDebug("bt-torrent: ParseTorrentFile path=%s", path)

	data, err := os.ReadFile(path)
	if err != nil {
		log.LogError("bt-torrent: read %s failed: %v", path, err)
		return nil, fmt.Errorf("read torrent file: %w", err)
	}
	return ParseTorrent(data)
}

// NumberOfPieces returns the number of pieces based on total size and piece length.
func (m *TorrentMeta) NumberOfPieces() int {
	if m.PieceLength <= 0 {
		return 0
	}
	n := m.TotalSize / m.PieceLength
	if m.TotalSize%m.PieceLength != 0 {
		n++
	}
	return int(n)
}

// --- Bencode encoder for info dict (minimal, only handles strings/ints/dicts) ---

func bencodeString(s string) []byte {
	return []byte(strconv.Itoa(len(s)) + ":" + s)
}

func bencodeInt(n int64) []byte {
	return []byte("i" + strconv.FormatInt(n, 10) + "e")
}

func bencodeDict(d map[string]bval) []byte {
	var buf bytes.Buffer
	buf.WriteByte('d')
	// Keys must be sorted lexicographically in bencode.
	keys := sortedKeys(d)
	for _, k := range keys {
		buf.Write(bencodeString(k))
		v := d[k]
		switch {
		case v.isInt:
			buf.Write(bencodeInt(v.intVal))
		case v.isStr:
			buf.Write(bencodeString(v.strVal))
		case v.isList:
			buf.WriteByte('l')
			for _, item := range v.list {
				switch {
				case item.isInt:
					buf.Write(bencodeInt(item.intVal))
				case item.isStr:
					buf.Write(bencodeString(item.strVal))
				case item.isList:
					buf.WriteByte('l')
					for _, sub := range item.list {
						switch {
						case sub.isInt:
							buf.Write(bencodeInt(sub.intVal))
						case sub.isStr:
							buf.Write(bencodeString(sub.strVal))
						}
					}
					buf.WriteByte('e')
				case item.isDict:
					buf.Write(bencodeDict(item.dict))
				}
			}
			buf.WriteByte('e')
		case v.isDict:
			buf.Write(bencodeDict(v.dict))
		}
	}
	buf.WriteByte('e')
	return buf.Bytes()
}

func sortedKeys(d map[string]bval) []string {
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	// Simple insertion sort for small dicts (typical in torrent files).
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
