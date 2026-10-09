// Package tracker implements a minimal BitTorrent HTTP tracker server
// (BEP 12 announce + BEP 31 scrape) with ban management and persistence.
//
// This package is part of the go-peerserver module so it can be mounted on
// both the standalone peersignal binary and the regserver (via the main
// peerdrive module's replace directive). It has zero external dependencies
// beyond the standard library — bencode encoding and compact peer packing
// are implemented locally rather than importing anacrolix/torrent (which
// would be a ~200MB transitive dependency for a ~50-line feature).
//
// Wire format notes:
//   - Bencode dicts require keys sorted lexicographically; we sort before encode.
//   - info_hash / peer_id arrive as 20-byte binary (URL-encoded) or 40-char hex;
//     both are accepted. See parseHashParam for the dual-accept logic.
//   - Compact peer format: 4-byte IPv4 + 2-byte big-endian port per peer (6 bytes total).
//     IPv6 peers are dropped in compact mode (BEP 12 §Compact).
package tracker

import (
	"encoding/binary"
	"fmt"
	"net"
	"sort"
)

// PeerAddr is a resolved IP:port pair for a tracker peer.
type PeerAddr struct {
	IP   net.IP
	Port uint16
}

// bencodeString encodes a string in bencode format: <length>:<data>.
func bencodeString(s string) []byte {
	out := make([]byte, 0, len(s)+len(fmt.Sprintf("%d", len(s)))+1)
	out = append(out, fmt.Sprintf("%d:", len(s))...)
	out = append(out, s...)
	return out
}

// bencodeBytes encodes a byte string in bencode format: <length>:<data>.
func bencodeBytes(b []byte) []byte {
	out := make([]byte, 0, len(b)+len(fmt.Sprintf("%d", len(b)))+1)
	out = append(out, fmt.Sprintf("%d:", len(b))...)
	out = append(out, b...)
	return out
}

// bencodeInt encodes an integer in bencode format: i<int>e.
func bencodeInt(i int64) []byte {
	return []byte(fmt.Sprintf("i%de", i))
}

// bencodeEntry is a key-value pair for bencode dictionary encoding.
type bencodeEntry struct {
	key   string
	value []byte
}

// bencodeDict encodes a dictionary in bencode format: d<sorted-kv-pairs>e.
// Keys must be unique; they are sorted lexicographically before encoding.
func bencodeDict(entries []bencodeEntry) []byte {
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	out := []byte{'d'}
	for _, e := range entries {
		out = append(out, bencodeString(e.key)...)
		out = append(out, e.value...)
	}
	out = append(out, 'e')
	return out
}

// bencodeList encodes a list in bencode format: l<items>e.
func bencodeList(items ...[]byte) []byte {
	out := []byte{'l'}
	for _, item := range items {
		out = append(out, item...)
	}
	out = append(out, 'e')
	return out
}

// compactEncode packs peers into the BT compact format: 6 bytes per IPv4 peer
// (4-byte IP + 2-byte big-endian port). IPv6 peers are silently dropped — the
// compact format has no room for them. This is standard BT behaviour; clients
// that need IPv6 should request non-compact (compact=0) or use the DHT.
func compactEncode(peers []PeerAddr) []byte {
	buf := make([]byte, 0, len(peers)*6)
	for _, p := range peers {
		ip4 := p.IP.To4()
		if ip4 == nil {
			continue // IPv6 not representable in compact format
		}
		buf = append(buf, ip4[0], ip4[1], ip4[2], ip4[3])
		buf = append(buf, byte(p.Port>>8), byte(p.Port))
	}
	return buf
}

// compactDecode unpacks the BT compact format into PeerAddr slice.
// Malformed input (length not a multiple of 6) is tolerated by stopping at
// the last complete entry — trackers should never crash on a bad response.
func compactDecode(data []byte) []PeerAddr {
	peers := make([]PeerAddr, 0, len(data)/6)
	for i := 0; i+5 < len(data); i += 6 {
		ip := net.IPv4(data[i], data[i+1], data[i+2], data[i+3])
		port := binary.BigEndian.Uint16(data[i+4 : i+6])
		peers = append(peers, PeerAddr{IP: ip, Port: port})
	}
	return peers
}

// bencodePeersDict builds the full bencoded announce response dict:
// d8:intervali<seconds>e5:peers<compact|list>e
//
// When compact is true, peers are packed as a single byte string.
// When false, peers are a list of d4:ip<ip>e4:port<port>e dicts.
func bencodePeersDict(interval int, peers []PeerAddr, compact bool) []byte {
	var peersVal []byte
	if compact {
		peersVal = bencodeBytes(compactEncode(peers))
	} else {
		items := make([][]byte, 0, len(peers))
		for _, p := range peers {
			ip := p.IP.String()
			dict := bencodeDict([]bencodeEntry{
				{"ip", bencodeString(ip)},
				{"port", bencodeInt(int64(p.Port))},
			})
			items = append(items, dict)
		}
		peersVal = bencodeList(items...)
	}
	return bencodeDict([]bencodeEntry{
		{"interval", bencodeInt(int64(interval))},
		{"peers", peersVal},
	})
}

// bencodeError builds a bencoded error response: d8:failure_reason<len>:<reason>e
// Some clients ignore the status code and only check the bencode body, so
// error responses should be well-formed bencode rather than plain text.
func bencodeError(reason string) []byte {
	return bencodeDict([]bencodeEntry{
		{"failure_reason", bencodeString(reason)},
	})
}
