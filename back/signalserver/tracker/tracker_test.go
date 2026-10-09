// Tests for the BitTorrent HTTP tracker server package.
//
// Discovery background: tracker was built from scratch because anacrolix/torrent
// does not ship a ready-made HTTP tracker server (only the client side). Tests
// cover: wire format (compact/bencode), announce lifecycle (add/remove/stop),
// ban enforcement (info_hash/peer_id/IP/user), persistence, management API,
// and two-client end-to-end visibility.
package tracker

import (
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- Wire format tests ----

func TestCompactEncode(t *testing.T) {
	peers := []PeerAddr{
		{IP: net.IPv4(127, 0, 0, 1), Port: 6881},
		{IP: net.IPv4(192, 168, 1, 100), Port: 12345},
	}
	data := compactEncode(peers)
	if len(data) != 12 {
		t.Fatalf("compact encode: got %d bytes, want 12 (2 peers × 6)", len(data))
	}
	decoded := compactDecode(data)
	if len(decoded) != 2 {
		t.Fatalf("compact decode: got %d peers, want 2", len(decoded))
	}
	if !decoded[0].IP.Equal(net.IPv4(127, 0, 0, 1)) || decoded[0].Port != 6881 {
		t.Errorf("peer 0: got %s:%d, want 127.0.0.1:6881", decoded[0].IP, decoded[0].Port)
	}
	if !decoded[1].IP.Equal(net.IPv4(192, 168, 1, 100)) || decoded[1].Port != 12345 {
		t.Errorf("peer 1: got %s:%d, want 192.168.1.100:12345", decoded[1].IP, decoded[1].Port)
	}
}

func TestCompactEncode_SkipsIPv6(t *testing.T) {
	peers := []PeerAddr{
		{IP: net.ParseIP("::1"), Port: 6881}, // IPv6 — should be skipped
		{IP: net.IPv4(10, 0, 0, 1), Port: 1234},
	}
	data := compactEncode(peers)
	// Only the IPv4 peer should be encoded (6 bytes).
	if len(data) != 6 {
		t.Fatalf("compact encode with IPv6: got %d bytes, want 6 (IPv4 only)", len(data))
	}
}

func TestCompactDecode_Malformed(t *testing.T) {
	// 5 bytes — not a multiple of 6. Should return 0 peers gracefully.
	decoded := compactDecode([]byte{0x01, 0x02, 0x03, 0x04, 0x05})
	if len(decoded) != 0 {
		t.Errorf("malformed compact decode: got %d peers, want 0", len(decoded))
	}
}

func TestBencodeDict_SortedKeys(t *testing.T) {
	// Keys must be sorted lexicographically.
	entries := []bencodeEntry{
		{"zebra", bencodeInt(1)},
		{"apple", bencodeInt(2)},
		{"mango", bencodeInt(3)},
	}
	data := bencodeDict(entries)
	s := string(data)
	// Verify apple comes before mango which comes before zebra.
	appleIdx := strings.Index(s, "5:apple")
	mangoIdx := strings.Index(s, "5:mango")
	zebraIdx := strings.Index(s, "5:zebra")
	if appleIdx == -1 || mangoIdx == -1 || zebraIdx == -1 {
		t.Fatalf("missing keys in bencode dict: %q", s)
	}
	if !(appleIdx < mangoIdx && mangoIdx < zebraIdx) {
		t.Errorf("keys not sorted: apple=%d mango=%d zebra=%d", appleIdx, mangoIdx, zebraIdx)
	}
}

func TestBencodePeersDict_Compact(t *testing.T) {
	peers := []PeerAddr{{IP: net.IPv4(127, 0, 0, 1), Port: 6881}}
	data := bencodePeersDict(900, peers, true)
	s := string(data)
	// Should contain "interval" and "peers" keys (both must be present).
	if !strings.Contains(s, "8:interval") {
		t.Error("missing interval key in compact bencode")
	}
	if !strings.Contains(s, "5:peers") {
		t.Error("missing peers key in compact bencode")
	}
	// Compact mode: peers is a byte string (6 bytes for 1 peer).
	if !strings.Contains(s, "6:") {
		t.Error("missing 6: (6-byte compact peer data) in compact bencode")
	}
}

func TestBencodePeersDict_NonCompact(t *testing.T) {
	peers := []PeerAddr{{IP: net.IPv4(127, 0, 0, 1), Port: 6881}}
	data := bencodePeersDict(900, peers, false)
	s := string(data)
	// Non-compact: peers is a list with ip/port dicts.
	if !strings.Contains(s, "2:ip") {
		t.Error("missing ip key in non-compact bencode")
	}
	if !strings.Contains(s, "4:port") {
		t.Error("missing port key in non-compact bencode")
	}
}

func TestBencodeError(t *testing.T) {
	data := bencodeError("test reason")
	s := string(data)
	if !strings.Contains(s, "14:failure_reason") {
		t.Errorf("missing failure_reason key: %q", s)
	}
	if !strings.Contains(s, "11:test reason") {
		t.Errorf("missing reason text: %q", s)
	}
}

// ---- parseHashParam tests ----

func TestParseHashParam(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"empty", "", "", true},
		{"40char_hex", strings.Repeat("a", 40), strings.Repeat("a", 40), false},
		{"40char_hex_upper", strings.Repeat("A", 40), strings.Repeat("a", 40), false},
		{"raw_binary_20bytes", string(make([]byte, 20)), "0000000000000000000000000000000000000000", false},
		{"too_short", "abc", "", true},
		{"invalid_hex", strings.Repeat("g", 40), "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseHashParam(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseHashParam(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("parseHashParam(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---- Announce handler table-driven tests ----

func TestTrackerAnnounce(t *testing.T) {
	newTracker := func() *Tracker {
		return NewTracker(WithBanFile(""), WithPeerTTL(1*time.Hour))
	}

	// Helper to build an announce request URL.
	announceURL := func(infoHash, peerID string, port int, extra url.Values) string {
		q := url.Values{}
		q.Set("info_hash", infoHash)
		q.Set("peer_id", peerID)
		q.Set("port", itoa(port))
		for k, vs := range extra {
			for _, v := range vs {
				q.Add(k, v)
			}
		}
		return "/announce?" + q.Encode()
	}

	tests := []struct {
		name       string
		path       string
		method     string
		wantStatus int
		wantErrStr string // substring to check in body (for error cases)
	}{
		{
			name:       "valid_announce_compact",
			path:       announceURL(strings.Repeat("a", 40), strings.Repeat("b", 40), 6881, nil),
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
		},
		{
			name:       "valid_announce_noncompact",
			path:       announceURL(strings.Repeat("a", 40), strings.Repeat("b", 40), 6881, url.Values{"compact": {"0"}}),
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
		},
		{
			name:       "bad_info_hash_too_short",
			path:       announceURL("abc", strings.Repeat("b", 40), 6881, nil),
			method:     http.MethodGet,
			wantStatus: http.StatusBadRequest,
			wantErrStr: "invalid info_hash",
		},
		{
			name:       "missing_peer_id",
			path:       announceURL(strings.Repeat("a", 40), "", 6881, nil),
			method:     http.MethodGet,
			wantStatus: http.StatusBadRequest,
			wantErrStr: "invalid peer_id",
		},
		{
			name:       "missing_port",
			path:       "/announce?info_hash=" + strings.Repeat("a", 40) + "&peer_id=" + strings.Repeat("b", 40),
			method:     http.MethodGet,
			wantStatus: http.StatusBadRequest,
			wantErrStr: "port is required",
		},
		{
			name:       "invalid_port",
			path:       announceURL(strings.Repeat("a", 40), strings.Repeat("b", 40), 0, nil),
			method:     http.MethodGet,
			wantStatus: http.StatusBadRequest,
			wantErrStr: "invalid port",
		},
		{
			name:       "wrong_method",
			path:       "/announce",
			method:     http.MethodPost,
			wantStatus: http.StatusMethodNotAllowed,
			wantErrStr: "method not allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newTracker()
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.RemoteAddr = "127.0.0.1:12345"
			tr.HandleAnnounce(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d; body=%q", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if tt.wantErrStr != "" && !strings.Contains(rr.Body.String(), tt.wantErrStr) {
				t.Errorf("body %q does not contain %q", rr.Body.String(), tt.wantErrStr)
			}
		})
	}
}

// TestTrackerAnnounce_StoppedEvent verifies that stopped events remove the peer
// and return an empty peer list.
func TestTrackerAnnounce_StoppedEvent(t *testing.T) {
	tr := NewTracker(WithPeerTTL(1 * time.Hour))
	infoHash := strings.Repeat("c", 40)
	peerID := strings.Repeat("d", 40)

	// First announce: add the peer.
	q1 := url.Values{}
	q1.Set("info_hash", infoHash)
	q1.Set("peer_id", peerID)
	q1.Set("port", "6881")
	req1 := httptest.NewRequest(http.MethodGet, "/announce?"+q1.Encode(), nil)
	req1.RemoteAddr = "127.0.0.1:12345"
	rr1 := httptest.NewRecorder()
	tr.HandleAnnounce(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("first announce: status = %d", rr1.Code)
	}

	// Second announce (different peer) should see the first peer.
	q2 := url.Values{}
	q2.Set("info_hash", infoHash)
	q2.Set("peer_id", strings.Repeat("e", 40))
	q2.Set("port", "6882")
	req2 := httptest.NewRequest(http.MethodGet, "/announce?"+q2.Encode(), nil)
	req2.RemoteAddr = "127.0.0.1:12346"
	rr2 := httptest.NewRecorder()
	tr.HandleAnnounce(rr2, req2)
	// The response should contain the first peer's IP:port in compact format.
	// 127.0.0.1 = 7f000001, 6881 = 1ae9
	body2 := rr2.Body.Bytes()
	if len(body2) < 30 {
		t.Fatalf("second announce body too short: %d bytes", len(body2))
	}
	// Check that 127.0.0.1:6881 is in the compact data.
	compactIdx := strings.Index(rr2.Body.String(), "5:peers")
	if compactIdx == -1 {
		t.Fatalf("second announce: missing peers key in body %q", rr2.Body.String())
	}

	// Now send stopped event for the first peer.
	q3 := url.Values{}
	q3.Set("info_hash", infoHash)
	q3.Set("peer_id", peerID)
	q3.Set("port", "6881")
	q3.Set("event", "stopped")
	req3 := httptest.NewRequest(http.MethodGet, "/announce?"+q3.Encode(), nil)
	req3.RemoteAddr = "127.0.0.1:12345"
	rr3 := httptest.NewRecorder()
	tr.HandleAnnounce(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("stopped announce: status = %d", rr3.Code)
	}

	// After stopped, peer e should still be visible (only peer d was removed).
	q4 := url.Values{}
	q4.Set("info_hash", infoHash)
	q4.Set("peer_id", strings.Repeat("f", 40))
	q4.Set("port", "6883")
	req4 := httptest.NewRequest(http.MethodGet, "/announce?"+q4.Encode(), nil)
	req4.RemoteAddr = "127.0.0.1:12347"
	rr4 := httptest.NewRecorder()
	tr.HandleAnnounce(rr4, req4)
	// The compact response should contain peer e (127.0.0.1:6882).
	// 127.0.0.1 = 7f000001, 6882 = 1ae2
	if !strings.Contains(rr4.Body.String(), "6:\x7f\x00\x00\x01\x1a\xe2e") {
		t.Errorf("after stopped event: peer e should still be visible; body: %q", rr4.Body.String())
	}
}

// TestTrackerAnnounce_PeerAddRemove verifies the peer list grows and shrinks.
func TestTrackerAnnounce_PeerAddRemove(t *testing.T) {
	tr := NewTracker(WithPeerTTL(1 * time.Hour))
	infoHash := strings.Repeat("1", 40)

	// Announce 3 peers.
	for i := 0; i < 3; i++ {
		peerID := hex.EncodeToString([]byte{byte(i + 1)}) + strings.Repeat("0", 38)
		port := 6881 + i
		q := url.Values{}
		q.Set("info_hash", infoHash)
		q.Set("peer_id", peerID)
		q.Set("port", itoa(port))
		req := httptest.NewRequest(http.MethodGet, "/announce?"+q.Encode(), nil)
		req.RemoteAddr = "127.0.0.1:10000"
		rr := httptest.NewRecorder()
		tr.HandleAnnounce(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("announce %d: status = %d", i, rr.Code)
		}
	}

	// Check stats.
	stats := tr.Stats()
	if stats["torrents"] != 1 {
		t.Errorf("torrents = %v, want 1", stats["torrents"])
	}
	if stats["totalPeers"] != 3 {
		t.Errorf("totalPeers = %v, want 3", stats["totalPeers"])
	}

	// Stop one peer.
	stopQ := url.Values{}
	stopQ.Set("info_hash", infoHash)
	stopQ.Set("peer_id", hex.EncodeToString([]byte{0x01})+strings.Repeat("0", 38))
	stopQ.Set("port", "6881")
	stopQ.Set("event", "stopped")
	stopReq := httptest.NewRequest(http.MethodGet, "/announce?"+stopQ.Encode(), nil)
	stopReq.RemoteAddr = "127.0.0.1:10001"
	stopRR := httptest.NewRecorder()
	tr.HandleAnnounce(stopRR, stopReq)

	stats = tr.Stats()
	if stats["totalPeers"] != 2 {
		t.Errorf("after stopped: totalPeers = %v, want 2", stats["totalPeers"])
	}
}

// TestTrackerAnnounce_NumWant verifies numwant limiting.
func TestTrackerAnnounce_NumWant(t *testing.T) {
	tr := NewTracker(WithPeerTTL(1*time.Hour), WithMaxPeers(10))
	infoHash := strings.Repeat("2", 40)

	// Add 5 peers.
	for i := 0; i < 5; i++ {
		peerID := hex.EncodeToString([]byte{byte(i + 1)}) + strings.Repeat("0", 38)
		port := 7000 + i
		q := url.Values{}
		q.Set("info_hash", infoHash)
		q.Set("peer_id", peerID)
		q.Set("port", itoa(port))
		req := httptest.NewRequest(http.MethodGet, "/announce?"+q.Encode(), nil)
		req.RemoteAddr = "127.0.0.1:20000"
		rr := httptest.NewRecorder()
		tr.HandleAnnounce(rr, req)
	}

	// A new peer requesting numwant=2 should see at most 2 peers.
	q := url.Values{}
	q.Set("info_hash", infoHash)
	q.Set("peer_id", strings.Repeat("9", 40))
	q.Set("port", "7005")
	q.Set("numwant", "2")
	q.Set("compact", "0") // non-compact for easy parsing
	req := httptest.NewRequest(http.MethodGet, "/announce?"+q.Encode(), nil)
	req.RemoteAddr = "127.0.0.1:20001"
	rr := httptest.NewRecorder()
	tr.HandleAnnounce(rr, req)

	body := rr.Body.String()
	// Count "2:ip" occurrences — each peer dict has one.
	count := strings.Count(body, "2:ip")
	if count != 2 {
		t.Errorf("numwant=2: got %d peers in response, want 2; body=%q", count, body)
	}
}

// ---- End-to-end: two clients see each other ----

func TestTrackerAnnounce_E2E_TwoClients(t *testing.T) {
	tr := NewTracker(WithPeerTTL(1 * time.Hour))
	srv := httptest.NewServer(http.HandlerFunc(tr.HandleAnnounce))
	defer srv.Close()

	infoHash := strings.Repeat("a", 40)
	peerID1 := strings.Repeat("1", 40)
	peerID2 := strings.Repeat("2", 40)

	// Client 1 announces.
	_, err := http.Get(srv.URL + "/announce?info_hash=" + infoHash + "&peer_id=" + peerID1 + "&port=6881&compact=0")
	if err != nil {
		t.Fatal(err)
	}

	// Client 2 announces — should see client 1's peer.
	resp, err := http.Get(srv.URL + "/announce?info_hash=" + infoHash + "&peer_id=" + peerID2 + "&port=6882&compact=0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	bodyStr := string(body[:n])

	// The response should contain client 1's IP (127.0.0.1) and port (6881).
	if !strings.Contains(bodyStr, "2:ip") {
		t.Fatalf("response missing ip key: %q", bodyStr)
	}
	if !strings.Contains(bodyStr, "4:port") {
		t.Fatalf("response missing port key: %q", bodyStr)
	}
	// Check that port 6881 appears (it would be in the bencode as i6881e).
	if !strings.Contains(bodyStr, "i6881e") {
		t.Errorf("response missing port 6881: %q", bodyStr)
	}
}

// ---- Scrape handler ----

func TestTrackerScrape(t *testing.T) {
	tr := NewTracker(WithPeerTTL(1 * time.Hour))
	infoHash := strings.Repeat("a", 40)

	// Add a peer.
	q := url.Values{}
	q.Set("info_hash", infoHash)
	q.Set("peer_id", strings.Repeat("b", 40))
	q.Set("port", "6881")
	req := httptest.NewRequest(http.MethodGet, "/announce?"+q.Encode(), nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	tr.HandleAnnounce(rr, req)

	// Scrape for this info_hash.
	srReq := httptest.NewRequest(http.MethodGet, "/scrape?info_hash="+infoHash, nil)
	srRR := httptest.NewRecorder()
	tr.HandleScrape(srRR, srReq)
	if srRR.Code != http.StatusOK {
		t.Fatalf("scrape status = %d", srRR.Code)
	}
	body := srRR.Body.String()
	// Should contain "files" and "complete" and "incomplete".
	if !strings.Contains(body, "5:files") {
		t.Errorf("scrape missing files key: %q", body)
	}
	if !strings.Contains(body, "8:complete") {
		t.Errorf("scrape missing complete key: %q", body)
	}
	if !strings.Contains(body, "10:incomplete") {
		t.Errorf("scrape missing incomplete key: %q", body)
	}
}

func TestTrackerScrape_MissingInfoHash(t *testing.T) {
	tr := NewTracker()
	req := httptest.NewRequest(http.MethodGet, "/scrape", nil)
	rr := httptest.NewRecorder()
	tr.HandleScrape(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("scrape without info_hash: status = %d, want 400", rr.Code)
	}
}

// ---- Ban store tests ----

func TestBanStore_AddRemoveList(t *testing.T) {
	bs := NewBanStore("", nil)

	// Add bans.
	b1 := Ban{Type: BanInfoHash, Target: strings.Repeat("a", 40), Reason: "test"}
	b2 := Ban{Type: BanPeerID, Target: strings.Repeat("b", 40), Reason: "test"}
	b3 := Ban{Type: BanIP, Target: "1.2.3.4", Reason: "test"}
	b4 := Ban{Type: BanUser, Target: "hacker", Reason: "test"}

	if !bs.Add(b1) {
		t.Error("failed to add info_hash ban")
	}
	if !bs.Add(b2) {
		t.Error("failed to add peer_id ban")
	}
	if !bs.Add(b3) {
		t.Error("failed to add IP ban")
	}
	if !bs.Add(b4) {
		t.Error("failed to add user ban")
	}

	// Duplicate should fail.
	if bs.Add(b1) {
		t.Error("duplicate ban should fail")
	}

	// List.
	bans := bs.List()
	if len(bans) != 4 {
		t.Errorf("list: got %d bans, want 4", len(bans))
	}

	// Check IsBanned.
	if banned, _, _ := bs.IsBanned(strings.Repeat("a", 40), "", "", ""); !banned {
		t.Error("info_hash should be banned")
	}
	if banned, _, _ := bs.IsBanned("", strings.Repeat("b", 40), "", ""); !banned {
		t.Error("peer_id should be banned")
	}
	if banned, _, _ := bs.IsBanned("", "", "1.2.3.4", ""); !banned {
		t.Error("IP should be banned")
	}
	if !bs.IsUserBanned("hacker") {
		t.Error("user should be banned")
	}
	if banned, _, _ := bs.IsBanned("", "", "", "hacker"); !banned {
		t.Error("user should be banned via IsBanned")
	}

	// Remove.
	if !bs.Remove(BanInfoHash, strings.Repeat("a", 40)) {
		t.Error("failed to remove info_hash ban")
	}
	if banned, _, _ := bs.IsBanned(strings.Repeat("a", 40), "", "", ""); banned {
		t.Error("info_hash should not be banned after removal")
	}

	// Remove non-existent should fail.
	if bs.Remove(BanInfoHash, strings.Repeat("z", 40)) {
		t.Error("removing non-existent ban should fail")
	}

	bans = bs.List()
	if len(bans) != 3 {
		t.Errorf("after removal: got %d bans, want 3", len(bans))
	}
}

func TestBanStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "bans.json")

	// Create store with persistence.
	bs := NewBanStore(filePath, nil)
	b := Ban{Type: BanIP, Target: "5.6.7.8", Reason: "test", Created: time.Now()}
	bs.Add(b)

	// Save.
	if err := bs.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Verify file exists.
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Fatal("persistence file not created")
	}

	// Load into a new store.
	bs2 := NewBanStore(filePath, nil)
	if len(bs2.List()) != 1 {
		t.Fatalf("loaded bans: got %d, want 1", len(bs2.List()))
	}
	if bs2.List()[0].Target != "5.6.7.8" {
		t.Errorf("loaded ban target = %q, want %q", bs2.List()[0].Target, "5.6.7.8")
	}

	// Verify ban is enforced after reload.
	if banned, _, _ := bs2.IsBanned("", "", "5.6.7.8", ""); !banned {
		t.Error("ban should be enforced after reload")
	}
}

func TestBanStore_UserPeerBanned(t *testing.T) {
	bs := NewBanStore("", nil)
	bs.Add(Ban{Type: BanUser, Target: "hacker", Reason: "test"})

	// Create user→peer_id mapping.
	userPeers := map[string]map[string]bool{
		"hacker": {
			strings.Repeat("1", 40): true,
			strings.Repeat("2", 40): true,
		},
		"gooduser": {
			strings.Repeat("3", 40): true,
		},
	}

	if !bs.IsUserPeerBanned(strings.Repeat("1", 40), userPeers) {
		t.Error("peer of banned user should be detected as banned")
	}
	if !bs.IsUserPeerBanned(strings.Repeat("2", 40), userPeers) {
		t.Error("peer of banned user should be detected as banned")
	}
	if bs.IsUserPeerBanned(strings.Repeat("3", 40), userPeers) {
		t.Error("peer of non-banned user should not be banned")
	}
	if bs.IsUserPeerBanned(strings.Repeat("4", 40), userPeers) {
		t.Error("unknown peer should not be banned")
	}
}

// ---- Ban enforcement in announce ----

func TestTrackerAnnounce_BannedInfoHash(t *testing.T) {
	tr := NewTracker(WithPeerTTL(1 * time.Hour))
	infoHash := strings.Repeat("a", 40)

	// Ban the info_hash.
	tr.bans.Add(Ban{Type: BanInfoHash, Target: infoHash, Reason: "spam"})

	q := url.Values{}
	q.Set("info_hash", infoHash)
	q.Set("peer_id", strings.Repeat("b", 40))
	q.Set("port", "6881")
	req := httptest.NewRequest(http.MethodGet, "/announce?"+q.Encode(), nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	tr.HandleAnnounce(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("banned info_hash: status = %d, want 403", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "info_hash is banned") {
		t.Errorf("body should contain ban reason: %q", rr.Body.String())
	}
}

func TestTrackerAnnounce_BannedPeerID(t *testing.T) {
	tr := NewTracker(WithPeerTTL(1 * time.Hour))
	peerID := strings.Repeat("b", 40)

	tr.bans.Add(Ban{Type: BanPeerID, Target: peerID, Reason: "abuse"})

	q := url.Values{}
	q.Set("info_hash", strings.Repeat("a", 40))
	q.Set("peer_id", peerID)
	q.Set("port", "6881")
	req := httptest.NewRequest(http.MethodGet, "/announce?"+q.Encode(), nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	tr.HandleAnnounce(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("banned peer_id: status = %d, want 403", rr.Code)
	}
}

func TestTrackerAnnounce_BannedIP(t *testing.T) {
	tr := NewTracker(WithPeerTTL(1 * time.Hour))

	tr.bans.Add(Ban{Type: BanIP, Target: "127.0.0.1", Reason: "DDoS"})

	q := url.Values{}
	q.Set("info_hash", strings.Repeat("a", 40))
	q.Set("peer_id", strings.Repeat("b", 40))
	q.Set("port", "6881")
	req := httptest.NewRequest(http.MethodGet, "/announce?"+q.Encode(), nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	tr.HandleAnnounce(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("banned IP: status = %d, want 403", rr.Code)
	}
}

func TestTrackerAnnounce_BannedUser(t *testing.T) {
	tr := NewTracker(WithPeerTTL(1 * time.Hour))

	tr.bans.Add(Ban{Type: BanUser, Target: "hacker", Reason: "report"})

	q := url.Values{}
	q.Set("info_hash", strings.Repeat("a", 40))
	q.Set("peer_id", strings.Repeat("b", 40))
	q.Set("port", "6881")
	q.Set("user", "hacker")
	req := httptest.NewRequest(http.MethodGet, "/announce?"+q.Encode(), nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	tr.HandleAnnounce(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("banned user: status = %d, want 403", rr.Code)
	}
}

// TestTrackerAnnounce_BannedUserPeerBanned verifies that peers belonging to a
// banned user are rejected even when they don't carry the user parameter.
func TestTrackerAnnounce_BannedUserPeerBanned(t *testing.T) {
	tr := NewTracker(WithPeerTTL(1 * time.Hour))
	infoHash := strings.Repeat("a", 40)
	peerID := strings.Repeat("b", 40)

	// First, the user announces (creating the user→peer_id mapping).
	q1 := url.Values{}
	q1.Set("info_hash", infoHash)
	q1.Set("peer_id", peerID)
	q1.Set("port", "6881")
	q1.Set("user", "hacker")
	req1 := httptest.NewRequest(http.MethodGet, "/announce?"+q1.Encode(), nil)
	req1.RemoteAddr = "127.0.0.1:12345"
	rr1 := httptest.NewRecorder()
	tr.HandleAnnounce(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("first announce: status = %d", rr1.Code)
	}

	// Now ban the user.
	tr.bans.Add(Ban{Type: BanUser, Target: "hacker", Reason: "banned"})

	// The same peer announces again WITHOUT the user param — should be rejected
	// because its peer_id is mapped to a banned user.
	q2 := url.Values{}
	q2.Set("info_hash", infoHash)
	q2.Set("peer_id", peerID)
	q2.Set("port", "6881")
	req2 := httptest.NewRequest(http.MethodGet, "/announce?"+q2.Encode(), nil)
	req2.RemoteAddr = "127.0.0.1:12346"
	rr2 := httptest.NewRecorder()
	tr.HandleAnnounce(rr2, req2)

	if rr2.Code != http.StatusForbidden {
		t.Errorf("banned user peer: status = %d, want 403", rr2.Code)
	}
}

// TestTrackerAnnounce_BannedPeerFiltered verifies that banned peers are
// excluded from the peer list returned to other clients.
func TestTrackerAnnounce_BannedPeerFiltered(t *testing.T) {
	tr := NewTracker(WithPeerTTL(1 * time.Hour))
	infoHash := strings.Repeat("a", 40)

	peerID1 := strings.Repeat("1", 40)
	peerID2 := strings.Repeat("2", 40)

	// Add peer 1 (will be banned).
	q1 := url.Values{}
	q1.Set("info_hash", infoHash)
	q1.Set("peer_id", peerID1)
	q1.Set("port", "6881")
	req1 := httptest.NewRequest(http.MethodGet, "/announce?"+q1.Encode(), nil)
	req1.RemoteAddr = "127.0.0.1:12345"
	rr1 := httptest.NewRecorder()
	tr.HandleAnnounce(rr1, req1)

	// Add peer 2 (will NOT be banned).
	q2 := url.Values{}
	q2.Set("info_hash", infoHash)
	q2.Set("peer_id", peerID2)
	q2.Set("port", "6882")
	req2 := httptest.NewRequest(http.MethodGet, "/announce?"+q2.Encode(), nil)
	req2.RemoteAddr = "127.0.0.1:12346"
	rr2 := httptest.NewRecorder()
	tr.HandleAnnounce(rr2, req2)

	// Ban peer 1.
	tr.bans.Add(Ban{Type: BanPeerID, Target: peerID1, Reason: "test"})

	// Peer 2 asks for the peer list — should NOT see peer 1.
	q3 := url.Values{}
	q3.Set("info_hash", infoHash)
	q3.Set("peer_id", strings.Repeat("3", 40))
	q3.Set("port", "6883")
	q3.Set("compact", "0") // non-compact for easy checking
	req3 := httptest.NewRequest(http.MethodGet, "/announce?"+q3.Encode(), nil)
	req3.RemoteAddr = "127.0.0.1:12347"
	rr3 := httptest.NewRecorder()
	tr.HandleAnnounce(rr3, req3)

	body := rr3.Body.String()
	// Count peers in the response.
	count := strings.Count(body, "2:ip")
	if count != 1 {
		t.Errorf("banned peer filtered: got %d peers in response, want 1 (only non-banned); body=%q", count, body)
	}
}

// ---- Ban management endpoint ----

func TestBan_ManagementEndpoint_Unauthorized(t *testing.T) {
	tr := NewTracker(
		WithBanAuth(func(r *http.Request) bool {
			return r.Header.Get("Authorization") == "Bearer valid-token"
		}),
	)
	handler := tr.HandleBans()

	// Without auth.
	req := httptest.NewRequest(http.MethodGet, "/tracker/bans", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("unauthorized: status = %d, want 401", rr.Code)
	}
}

func TestBan_ManagementEndpoint_Authenticated(t *testing.T) {
	tr := NewTracker(
		WithBanAuth(func(r *http.Request) bool {
			return r.Header.Get("Authorization") == "Bearer valid-token"
		}),
	)
	handler := tr.HandleBans()

	// Add a ban.
	addBody := `{"type":"ip","target":"1.2.3.4","reason":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/tracker/bans", strings.NewReader(addBody))
	req.Header.Set("Authorization", "Bearer valid-token")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("add ban: status = %d, body=%q", rr.Code, rr.Body.String())
	}

	// List bans.
	req = httptest.NewRequest(http.MethodGet, "/tracker/bans", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rr = httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list bans: status = %d", rr.Code)
	}
	var resp struct {
		Bans []Ban `json:"bans"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Bans) != 1 {
		t.Errorf("list bans: got %d, want 1", len(resp.Bans))
	}

	// Remove ban.
	delURL := "/tracker/bans?type=ip&target=1.2.3.4"
	req = httptest.NewRequest(http.MethodDelete, delURL, nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rr = httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("remove ban: status = %d, body=%q", rr.Code, rr.Body.String())
	}

	// Verify removal.
	req = httptest.NewRequest(http.MethodGet, "/tracker/bans", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rr = httptest.NewRecorder()
	handler(rr, req)
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Bans) != 0 {
		t.Errorf("after removal: got %d bans, want 0", len(resp.Bans))
	}
}

func TestBan_ManagementEndpoint_BadBody(t *testing.T) {
	tr := NewTracker()
	handler := tr.HandleBans()

	// Missing target.
	req := httptest.NewRequest(http.MethodPost, "/tracker/bans", strings.NewReader(`{"type":"ip"}`))
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad body: status = %d, want 400", rr.Code)
	}

	// Invalid JSON.
	req = httptest.NewRequest(http.MethodPost, "/tracker/bans", strings.NewReader(`{invalid`))
	rr = httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON: status = %d, want 400", rr.Code)
	}

	// Missing target in DELETE.
	req = httptest.NewRequest(http.MethodDelete, "/tracker/bans?type=ip", nil)
	rr = httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("missing target in DELETE: status = %d, want 400", rr.Code)
	}

	// Wrong method.
	req = httptest.NewRequest(http.MethodPut, "/tracker/bans", nil)
	rr = httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("wrong method: status = %d, want 405", rr.Code)
	}
}

func TestBan_ManagementEndpoint_NotFound(t *testing.T) {
	tr := NewTracker()
	handler := tr.HandleBans()

	req := httptest.NewRequest(http.MethodDelete, "/tracker/bans?type=ip&target=9.9.9.9", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("not found: status = %d, want 404", rr.Code)
	}
}

// ---- Stats ----

func TestTrackerStats(t *testing.T) {
	tr := NewTracker()
	stats := tr.Stats()
	if stats["torrents"] != 0 {
		t.Errorf("torrents = %v, want 0", stats["torrents"])
	}
	if stats["totalPeers"] != 0 {
		t.Errorf("totalPeers = %v, want 0", stats["totalPeers"])
	}
	if stats["bans"] != 0 {
		t.Errorf("bans = %v, want 0", stats["bans"])
	}
}

// ---- VerifyUser callback ----

func TestTrackerAnnounce_VerifyUser(t *testing.T) {
	// Simulate a JWT verifier that maps tokens to usernames.
	verifier := func(token string) (string, bool) {
		if token == "valid-jwt" {
			return "alice", true
		}
		return "", false
	}

	tr := NewTracker(WithVerifyUser(verifier), WithPeerTTL(1*time.Hour))
	infoHash := strings.Repeat("a", 40)

	// Announce with a valid JWT — should associate the peer with "alice".
	q := url.Values{}
	q.Set("info_hash", infoHash)
	q.Set("peer_id", strings.Repeat("1", 40))
	q.Set("port", "6881")
	req := httptest.NewRequest(http.MethodGet, "/announce?"+q.Encode(), nil)
	req.Header.Set("Authorization", "Bearer valid-jwt")
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	tr.HandleAnnounce(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("announce with JWT: status = %d", rr.Code)
	}

	// Verify user→peer_id mapping was created.
	tr.mu.Lock()
	peers, ok := tr.userPeers["alice"]
	tr.mu.Unlock()
	if !ok {
		t.Fatal("user 'alice' not found in userPeers")
	}
	if !peers[strings.Repeat("1", 40)] {
		t.Error("peer_id not associated with user 'alice'")
	}

	// Ban user "alice" — the peer should now be rejected.
	tr.bans.Add(Ban{Type: BanUser, Target: "alice", Reason: "banned"})

	// Re-announce with the same JWT — should be rejected.
	q2 := url.Values{}
	q2.Set("info_hash", infoHash)
	q2.Set("peer_id", strings.Repeat("1", 40))
	q2.Set("port", "6881")
	req2 := httptest.NewRequest(http.MethodGet, "/announce?"+q2.Encode(), nil)
	req2.Header.Set("Authorization", "Bearer valid-jwt")
	req2.RemoteAddr = "127.0.0.1:12346"
	rr2 := httptest.NewRecorder()
	tr.HandleAnnounce(rr2, req2)
	if rr2.Code != http.StatusForbidden {
		t.Errorf("banned user re-announce: status = %d, want 403", rr2.Code)
	}
}

// ---- Helpers ----

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
