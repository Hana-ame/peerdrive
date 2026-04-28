// HTTP Tracker announce support (BEP 3) for BitTorrent clients.
// Peerdrive uses this to discover peers via HTTP(S) trackers from .torrent files.
package p2p_bt

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"peerdrive/internal/log"
)

// TrackerResponse holds the parsed response from an HTTP tracker announce.
type TrackerResponse struct {
	Interval int      `json:"interval"`
	MinInterval int   `json:"min_interval,omitempty"`
	Peers    []string `json:"peers"` // "ip:port" strings
	Complete int      `json:"complete,omitempty"`
	Incomplete int    `json:"incomplete,omitempty"`
	Warning  string   `json:"warning,omitempty"`
	Failure  string   `json:"failure_reason,omitempty"`
}

// announcePeerID returns a 20-byte peer ID used for tracker announces.
var announcePeerID [20]byte

func init() {
	copy(announcePeerID[:], []byte("-PD0001-"))
	rand.Read(announcePeerID[8:])
}

// AnnounceHTTP performs an HTTP tracker announce and returns the parsed response.
// It follows the BitTorrent HTTP tracker protocol (BEP 3).
func AnnounceHTTP(announceURL string, ih [20]byte, port int, uploaded, downloaded, left int64) (*TrackerResponse, error) {
	defer log.LogDuration("BT.AnnounceHTTP")()
	log.LogInfo("[bt-wire] http tracker announce url=%s", announceURL)

	if port <= 0 {
		port = 6881
	}

	// Build query parameters per BEP 3.
	params := url.Values{}
	params.Set("info_hash", string(ih[:]))
	params.Set("peer_id", string(announcePeerID[:]))
	params.Set("port", strconv.Itoa(port))
	params.Set("uploaded", strconv.FormatInt(uploaded, 10))
	params.Set("downloaded", strconv.FormatInt(downloaded, 10))
	params.Set("left", strconv.FormatInt(left, 10))
	params.Set("compact", "1")
	params.Set("event", "started")

	fullURL := announceURL
	if stringsContains(announceURL, "?") {
		fullURL = announceURL + "&" + params.Encode()
	} else {
		fullURL = announceURL + "?" + params.Encode()
	}

	log.LogDebug("[bt-wire] tracker request: %s", fullURL)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(fullURL)
	if err != nil {
		log.LogWarn("[bt-wire] tracker request failed: %v", err)
		return nil, fmt.Errorf("tracker request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.LogWarn("[bt-wire] tracker read body failed: %v", err)
		return nil, fmt.Errorf("tracker read: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.LogWarn("[bt-wire] tracker HTTP %d: %s", resp.StatusCode, string(body))
		return nil, fmt.Errorf("tracker HTTP %d", resp.StatusCode)
	}

	return parseTrackerResponse(body)
}

// parseTrackerResponse decodes the bencoded tracker response.
func parseTrackerResponse(data []byte) (*TrackerResponse, error) {
	root, err := bdecodeBytes(data)
	if err != nil {
		return nil, fmt.Errorf("tracker decode: %w", err)
	}

	result := &TrackerResponse{}

	// Check for failure reason.
	if fr, ok := root["failure reason"]; ok && fr.isStr {
		result.Failure = fr.string()
		return result, fmt.Errorf("tracker failure: %s", fr.string())
	}

	// Warning (non-fatal).
	if w, ok := root["warning message"]; ok && w.isStr {
		result.Warning = w.string()
		log.LogWarn("[bt-wire] tracker warning: %s", w.string())
	}

	// Interval.
	if i, ok := root["interval"]; ok && i.isInt {
		result.Interval = int(i.int())
	}
	if mi, ok := root["min interval"]; ok && mi.isInt {
		result.MinInterval = int(mi.int())
	}

	// Seeders/leechers.
	if c, ok := root["complete"]; ok && c.isInt {
		result.Complete = int(c.int())
	}
	if ic, ok := root["incomplete"]; ok && ic.isInt {
		result.Incomplete = int(ic.int())
	}

	// Peers: compact format (6 bytes each) or list of dicts.
	if pv, ok := root["peers"]; ok {
		if pv.isStr {
			// Compact peer list.
			result.Peers = parseCompactPeers(pv.bytes())
		} else if pv.isList {
			// Non-compact peer list (dictionary format).
			for _, peerEntry := range pv.list {
				if !peerEntry.isDict {
					continue
				}
				pd := peerEntry.dict
				ip := ""
				port := 0
				if ipv, ok := pd["ip"]; ok && ipv.isStr {
					ip = ipv.string()
				}
				if portv, ok := pd["port"]; ok && portv.isInt {
					port = int(portv.int())
				}
				if ip != "" && port > 0 {
					result.Peers = append(result.Peers, net.JoinHostPort(ip, strconv.Itoa(port)))
				}
			}
		}
	}

	log.LogInfo("[bt-wire] tracker response: interval=%d complete=%d incomplete=%d peers=%d",
		result.Interval, result.Complete, result.Incomplete, len(result.Peers))
	return result, nil
}

// parseCompactPeers decodes a compact peer list (6 bytes per peer: 4 for IP, 2 for port).
func parseCompactPeers(data []byte) []string {
	if len(data)%6 != 0 {
		log.LogWarn("[bt-wire] compact peers length %d not multiple of 6", len(data))
	}
	var peers []string
	for i := 0; i+5 < len(data); i += 6 {
		ip := net.IP(data[i : i+4])
		port := binary.BigEndian.Uint16(data[i+4 : i+6])
		peers = append(peers, net.JoinHostPort(ip.String(), strconv.Itoa(int(port))))
	}
	return peers
}

// TrackerAnnounceList performs announces against all trackers in the list,
// returning the combined set of peers found. It is used by the BT client
// to discover peers from multiple trackers.
func TrackerAnnounceList(trackers []string, ih [20]byte, port int, uploaded, downloaded, left int64) ([]string, error) {
	defer log.LogDuration("BT.TrackerAnnounceList")()
	log.LogInfo("[bt-wire] tracker announce list: %d trackers", len(trackers))

	seen := make(map[string]struct{})
	var allPeers []string

	for _, tr := range trackers {
		// Skip non-HTTP trackers (e.g., UDP trackers).
		if !stringsContains(tr, "http://") && !stringsContains(tr, "https://") {
			log.LogDebug("[bt-wire] skipping non-HTTP tracker: %s", tr)
			continue
		}

		resp, err := AnnounceHTTP(tr, ih, port, uploaded, downloaded, left)
		if err != nil {
			log.LogWarn("[bt-wire] tracker %s announce failed: %v", tr, err)
			continue
		}

		for _, p := range resp.Peers {
			if _, ok := seen[p]; !ok {
				seen[p] = struct{}{}
				allPeers = append(allPeers, p)
			}
		}
	}

	log.LogInfo("[bt-wire] tracker announce list: found %d unique peers from %d trackers",
		len(allPeers), len(trackers))
	return allPeers, nil
}

// discoverPeersFromTrackers replaces the stub in client.go.
// It performs HTTP tracker announces to find peers.
func discoverPeersFromTrackers(infohash string, announceList []string) ([]string, error) {
	defer log.LogDuration("BT.discoverPeersFromTrackers")()
	log.LogDebug("[bt-wire] tracker discovery for %s (%d trackers)", infohash, len(announceList))

	if len(announceList) == 0 {
		return nil, nil
	}

	raw, err := hex.DecodeString(infohash)
	if err != nil {
		return nil, fmt.Errorf("decode infohash: %w", err)
	}
	var ih [20]byte
	copy(ih[:], raw)

	peers, err := TrackerAnnounceList(announceList, ih, 6881, 0, 0, 0)
	if err != nil {
		return nil, err
	}

	return peers, nil
}

// stringsContains is a small helper to avoid importing strings just for Contains.
func stringsContains(s, substr string) bool {
	return len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
