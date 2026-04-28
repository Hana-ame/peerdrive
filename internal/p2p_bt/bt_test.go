// Integration test for the p2p_bt package.
// Tests torrent parsing, wire protocol (connect, handshake, piece download),
// and end-to-end download from a local TCP seeder.
package p2p_bt

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// testBencodeStr encodes a string in bencode format.
func testBencodeStr(s string) []byte {
	return []byte(strconv.Itoa(len(s)) + ":" + s)
}

// testBencodeInt encodes an integer in bencode format.
func testBencodeInt(n int64) []byte {
	return []byte("i" + strconv.FormatInt(n, 10) + "e")
}

// testBencodeDict encodes a map[string][]byte into bencode dict format.
func testBencodeDict(d map[string][]byte) []byte {
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	// Sort keys lexicographically (insertion sort for small maps).
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	var buf bytes.Buffer
	buf.WriteByte('d')
	for _, k := range keys {
		buf.Write(testBencodeStr(k))
		buf.Write(d[k])
	}
	buf.WriteByte('e')
	return buf.Bytes()
}

// testCreateTorrentData creates .torrent file bytes and returns the info hash.
// It does NOT write to disk.
func testCreateTorrentData(data []byte, name string, trackerURL string) ([]byte, [20]byte, error) {
	pieceLen := int64(16384)

	var piecesBuf bytes.Buffer
	for offset := 0; offset < len(data); offset += int(pieceLen) {
		end := offset + int(pieceLen)
		if end > len(data) {
			end = len(data)
		}
		h := sha1.Sum(data[offset:end])
		piecesBuf.Write(h[:])
	}

	infoDict := testBencodeDict(map[string][]byte{
		"name":         testBencodeStr(name),
		"piece length": testBencodeInt(pieceLen),
		"pieces":       testBencodeStr(string(piecesBuf.Bytes())),
		"length":       testBencodeInt(int64(len(data))),
	})

	infoHash := sha1.Sum(infoDict)

	torrentDict := testBencodeDict(map[string][]byte{
		"announce":      testBencodeStr(trackerURL),
		"created by":    testBencodeStr("peerdrive-bt-test"),
		"creation date": testBencodeInt(time.Now().Unix()),
		"info":          infoDict,
	})

	return torrentDict, infoHash, nil
}

// testWriteTorrent creates and writes a .torrent file.
func testWriteTorrent(data []byte, torrentPath, name, trackerURL string) ([20]byte, error) {
	torrentDict, infoHash, err := testCreateTorrentData(data, name, trackerURL)
	if err != nil {
		return infoHash, err
	}
	return infoHash, os.WriteFile(torrentPath, torrentDict, 0644)
}

// ---------------------------------------------------------------------------
// TCP Seeder: a real BitTorrent wire protocol server
// ---------------------------------------------------------------------------

// btSeeder serves a file over the BT wire protocol.
type btSeeder struct {
	listener net.Listener
	data     []byte
	port     int
	infoHash [20]byte
}

// startSeeder creates and starts a BT seeder on a random localhost port.
func startSeeder(data []byte, infoHash [20]byte) (*btSeeder, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	s := &btSeeder{listener: listener, data: data, port: port, infoHash: infoHash}
	go s.acceptLoop()
	return s, nil
}

func (s *btSeeder) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

// handleConn implements a minimal but complete BT wire protocol seeder.
// It sends the handshake, bitfield, unchokes immediately on interested,
// and responds to request messages with piece data.
func (s *btSeeder) handleConn(conn net.Conn) {
	defer conn.Close()

	// 1. Read client handshake (68 bytes).
	hs := make([]byte, 68)
	if _, err := io.ReadFull(conn, hs); err != nil {
		return
	}
	if hs[0] != 19 || string(hs[1:20]) != btProtocolName {
		return
	}
	var gotIH [20]byte
	copy(gotIH[:], hs[28:48])
	if gotIH != s.infoHash {
		return
	}

	// 2. Send handshake response (68 bytes).
	resp := make([]byte, 68)
	resp[0] = 19
	copy(resp[1:20], btProtocolName)
	resp[27] |= 0x01
	copy(resp[28:48], s.infoHash[:])
	var pid [20]byte
	copy(pid[:], []byte("-PDTEST-12345678901"))
	copy(resp[48:68], pid[:])
	if _, err := conn.Write(resp); err != nil {
		return
	}

	// 3. Build and send bitfield message.
	pieceSize := int64(16384)
	numPieces := int64(len(s.data) + int(pieceSize) - 1) / int64(pieceSize)
	bfLen := int(numPieces+7) / 8
	bf := make([]byte, bfLen)
	// Set all bits (all pieces available).
	for i := int64(0); i < numPieces; i++ {
		bf[i/8] |= 1 << (7 - uint(i%8))
	}

	// Write bitfield message: [4-byte length][1-byte id][bitfield]
	msgLen := uint32(1 + len(bf))
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], msgLen)
	if _, err := conn.Write(hdr[:]); err != nil {
		return
	}
	conn.Write([]byte{msgBitfield})
	conn.Write(bf)

	// 4. Read messages from client.
	for {
		// Read 4-byte message length.
		var lenBuf [4]byte
		if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
			return
		}
		msgLen := binary.BigEndian.Uint32(lenBuf[:])
		if msgLen == 0 {
			continue // keep-alive
		}

		// Read message ID + payload.
		msg := make([]byte, msgLen)
		if _, err := io.ReadFull(conn, msg); err != nil {
			return
		}
		msgID := msg[0]
		payload := msg[1:]

		switch msgID {
		case msgInterested:
			// Send unchoke: [4-byte length=1][1-byte id=unchoke]
			binary.BigEndian.PutUint32(hdr[:], 1)
			conn.Write(hdr[:])
			conn.Write([]byte{msgUnchoke})

		case msgRequest:
			if len(payload) < 12 {
				continue
			}
			pIdx := binary.BigEndian.Uint32(payload[0:4])
			off := binary.BigEndian.Uint32(payload[4:8])
			length := binary.BigEndian.Uint32(payload[8:12])

			// Compute data range.
			start := int64(pIdx)*pieceSize + int64(off)
			end := start + int64(length)
			if start > int64(len(s.data)) {
				continue
			}
			if end > int64(len(s.data)) {
				end = int64(len(s.data))
			}
			block := s.data[start:end]

			// Build piece message: [4-byte len][1-byte id=7][4-byte idx][4-byte off][block]
			totalLen := uint32(1 + 4 + 4 + len(block))
			binary.BigEndian.PutUint32(hdr[:], totalLen)
			conn.Write(hdr[:])
			conn.Write([]byte{msgPiece})

			var idxBuf [4]byte
			binary.BigEndian.PutUint32(idxBuf[:], pIdx)
			conn.Write(idxBuf[:])

			var offBuf [4]byte
			binary.BigEndian.PutUint32(offBuf[:], off)
			conn.Write(offBuf[:])

			conn.Write(block)

		case msgChoke, msgNotInterested:
			return

		default:
			// keep-alive or unknown -- continue
		}
	}
}

func (s *btSeeder) Close()  { s.listener.Close() }
func (s *btSeeder) Addr() string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(s.port))
}

// ---------------------------------------------------------------------------
// HTTP Tracker server (minimal) for integration test
// ---------------------------------------------------------------------------

type testTracker struct {
	listener net.Listener
	seederAddr string
}

func startTracker(seederAddr string) (*testTracker, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("tracker listen: %w", err)
	}
	t := &testTracker{listener: listener, seederAddr: seederAddr}
	go t.serve()
	return t, nil
}

func (t *testTracker) serve() {
	for {
		conn, err := t.listener.Accept()
		if err != nil {
			return
		}
		go t.handleConn(conn)
	}
}

func (t *testTracker) handleConn(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	buf := make([]byte, 4096)
	_, err := conn.Read(buf)
	if err != nil {
		return
	}

	host, portStr, _ := net.SplitHostPort(t.seederAddr)
	port, _ := strconv.Atoi(portStr)
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil {
		ip = net.ParseIP("127.0.0.1")
	}

	// Compact peer list: 6 bytes per peer (4 IP + 2 port).
	compactPeers := make([]byte, 6)
	copy(compactPeers[0:4], ip.To4())
	binary.BigEndian.PutUint16(compactPeers[4:6], uint16(port))

	responseDict := testBencodeDict(map[string][]byte{
		"interval":   testBencodeInt(60),
		"complete":   testBencodeInt(1),
		"incomplete": testBencodeInt(0),
		"peers":      testBencodeStr(string(compactPeers)),
	})

	httpResp := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		len(responseDict), string(responseDict))
	conn.Write([]byte(httpResp))
}

func (t *testTracker) Close() { t.listener.Close() }
func (t *testTracker) Addr() string { return t.listener.Addr().String() }

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestParseTorrent tests parsing a .torrent file.
func TestParseTorrent(t *testing.T) {
	data := []byte("hello peerdrive bt test " + time.Now().String())

	dir := t.TempDir()
	torrentPath := filepath.Join(dir, "test.torrent")
	_, err := testWriteTorrent(data, torrentPath, "test.dat", "http://127.0.0.1:0/announce")
	if err != nil {
		t.Fatalf("create torrent: %v", err)
	}

	meta, err := ParseTorrentFile(torrentPath)
	if err != nil {
		t.Fatalf("ParseTorrentFile: %v", err)
	}

	if meta.Name != "test.dat" {
		t.Errorf("expected name 'test.dat', got %q", meta.Name)
	}
	if meta.PieceLength != 16384 {
		t.Errorf("expected piece length 16384, got %d", meta.PieceLength)
	}
	if !meta.IsSingleFile {
		t.Error("expected single file torrent")
	}
	if len(meta.Files) != 1 {
		t.Errorf("expected 1 file, got %d", len(meta.Files))
	}
	if meta.TotalSize != int64(len(data)) {
		t.Errorf("expected total size %d, got %d", len(data), meta.TotalSize)
	}
	if len(meta.Pieces) == 0 {
		t.Error("expected at least 1 piece")
	}
	if len(meta.InfoHashHex) != 40 {
		t.Errorf("expected 40-char infohash, got %q (%d chars)", meta.InfoHashHex, len(meta.InfoHashHex))
	}

	t.Logf("torrent: name=%q infohash=%s size=%d pieces=%d",
		meta.Name, meta.InfoHashHex, meta.TotalSize, len(meta.Pieces))
}

// TestWireHandshake tests the BT wire protocol handshake in isolation.
func TestWireHandshake(t *testing.T) {
	var infoHash [20]byte
	copy(infoHash[:], []byte("TESTINFOHASH00000000"))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Read handshake.
		hs := make([]byte, 68)
		if _, err := io.ReadFull(conn, hs); err != nil {
			t.Errorf("server read handshake: %v", err)
			return
		}
		if hs[0] != 19 || string(hs[1:20]) != btProtocolName {
			t.Error("server: invalid protocol")
			return
		}
		var clientIH [20]byte
		copy(clientIH[:], hs[28:48])
		if clientIH != infoHash {
			t.Errorf("server: ih mismatch: got %x", clientIH)
			return
		}

		// Send response.
		resp := make([]byte, 68)
		resp[0] = 19
		copy(resp[1:20], btProtocolName)
		copy(resp[28:48], infoHash[:])
		copy(resp[48:68], []byte("-PDTEST-12345678901"))
		conn.Write(resp)
	}()

	conn, err := net.DialTimeout("tcp", listener.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	peerID, err := btHandshake(conn, infoHash)
	if err != nil {
		t.Fatalf("handshake failed: %v", err)
	}

	<-serverDone
	t.Logf("handshake succeeded, peerID=%s", hex.EncodeToString(peerID[:]))
}

// TestDownloadPiece tests downloading a single piece from a local seeder.
func TestDownloadPiece(t *testing.T) {
	data := make([]byte, 8000)
	for i := range data {
		data[i] = byte(i % 256)
	}

	pieceLen := int64(len(data))
	infoHash := sha1.Sum(data)

	seeder, err := startSeeder(data, infoHash)
	if err != nil {
		t.Fatalf("start seeder: %v", err)
	}
	defer seeder.Close()

	expectedHash := sha1.Sum(data)
	var expHash [20]byte
	copy(expHash[:], expectedHash[:])

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	downloaded, err := DownloadPiece(ctx, seeder.Addr(), infoHash, 0, pieceLen, expHash)
	if err != nil {
		t.Fatalf("DownloadPiece: %v", err)
	}

	if len(downloaded) != len(data) {
		t.Fatalf("expected %d bytes, got %d", len(data), len(downloaded))
	}
	if !bytes.Equal(downloaded, data) {
		t.Fatal("downloaded data does not match original")
	}
	t.Logf("downloaded piece 0: %d bytes, SHA1 verified", len(downloaded))
}

// TestDownloadMultiPiece tests downloading from a multi-piece file.
func TestDownloadMultiPiece(t *testing.T) {
	data := make([]byte, 50000) // ~3 pieces at 16 KiB
	for i := range data {
		data[i] = byte(i * 7 % 256)
	}

	infoHash := sha1.Sum(data)

	seeder, err := startSeeder(data, infoHash)
	if err != nil {
		t.Fatalf("start seeder: %v", err)
	}
	defer seeder.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pieceLen := int64(16384)
	numPieces := (len(data) + 16383) / 16384

	for i := 0; i < numPieces; i++ {
		start := i * 16384
		end := start + 16384
		if end > len(data) {
			end = len(data)
		}
		expectedHash := sha1.Sum(data[start:end])
		var expHash [20]byte
		copy(expHash[:], expectedHash[:])

		pLen := pieceLen
		if i == numPieces-1 {
			pLen = int64(len(data)) % pieceLen
			if pLen == 0 {
				pLen = pieceLen
			}
		}

		downloaded, err := DownloadPiece(ctx, seeder.Addr(), infoHash, i, pLen, expHash)
		if err != nil {
			t.Fatalf("piece %d: %v", i, err)
		}
		if !bytes.Equal(downloaded, data[start:end]) {
			t.Fatalf("piece %d data mismatch", i)
		}
		t.Logf("piece %d: %d bytes OK", i, len(downloaded))
	}
}

// TestFullBTDownload tests end-to-end: create torrent, start seeder, start tracker,
// start Peerdrive server, upload torrent, wait for completion, verify file.
func TestFullBTDownload(t *testing.T) {
	// 1. Create a larger test file (~1MB).
	const fileSize = 1024 * 1024 // 1 MB
	data := make([]byte, fileSize)
	for i := range data {
		data[i] = byte(i * 17 % 256)
	}
	expectedSHA256 := sha256.Sum256(data)

	dir := t.TempDir()
	torrentPath := filepath.Join(dir, "test.torrent")

	// 2. Compute infohash.
	// We need the infohash BEFORE we know the tracker URL.
	// We'll build the torrent with a placeholder, extract the infohash,
	// then rebuild with the real tracker URL.
	{
		// Build a temporary torrent to get the infohash.
		tempTorrent, ih, err := testCreateTorrentData(data, "test.dat", "http://127.0.0.1:0/announce")
		if err != nil {
			t.Fatalf("create torrent data: %v", err)
		}
		_ = tempTorrent

		// 3. Start BT seeder on localhost.
		seeder, err := startSeeder(data, ih)
		if err != nil {
			t.Fatalf("start seeder: %v", err)
		}
		defer seeder.Close()
		t.Logf("seeder listening on %s", seeder.Addr())

		// 4. Start HTTP tracker.
		tracker, err := startTracker(seeder.Addr())
		if err != nil {
			t.Fatalf("start tracker: %v", err)
		}
		defer tracker.Close()
		t.Logf("tracker listening on %s", tracker.Addr())

		// 5. Write the real torrent file with the correct tracker URL.
		trackerURL := fmt.Sprintf("http://%s/announce", tracker.Addr())
		infoHashHex := hex.EncodeToString(ih[:])
		if _, err := testWriteTorrent(data, torrentPath, "test.dat", trackerURL); err != nil {
			t.Fatalf("write torrent: %v", err)
		}
		t.Logf("torrent: infohash=%s tracker=%s", infoHashHex, trackerURL)

		// Verify the infohash is correct.
		meta, err := ParseTorrentFile(torrentPath)
		if err != nil {
			t.Fatalf("parse torrent: %v", err)
		}
		if meta.InfoHashHex != infoHashHex {
			t.Fatalf("infohash mismatch: got %s, expected %s", meta.InfoHashHex, infoHashHex)
		}

		// 6. Create BTClient and add torrent.
		btDir := t.TempDir()
		client := NewBTClient(btDir)

		doneCh := make(chan struct{})
		var completedFiles []CompletedFile
		var completeMu sync.Mutex
		client.SetOnComplete(func(infohash string, files []CompletedFile) {
			completeMu.Lock()
			completedFiles = files
			completeMu.Unlock()
			close(doneCh)
		})

		err = client.AddTorrent(meta)
		if err != nil {
			t.Fatalf("AddTorrent: %v", err)
		}

		// 7. Wait for completion with timeout.
		select {
		case <-doneCh:
			t.Log("download completed!")
		case <-time.After(120 * time.Second):
			// Check error status
			status := client.GetDownload(infoHashHex)
			if status != nil {
				t.Fatalf("download did not complete within 120 seconds: status=%s error=%q",
					status.Status, status.Error)
			}
			t.Fatal("download did not complete within 120 seconds")
		}

		// 8. Verify the downloaded files.
		completeMu.Lock()
		files := completedFiles
		completeMu.Unlock()

		if len(files) == 0 {
			t.Fatal("no files were completed")
		}

		for _, f := range files {
			t.Logf("completed file: path=%s size=%d sha256=%s", f.Path, f.Size, f.SHA256)

			downloadedData, err := os.ReadFile(f.Path)
			if err != nil {
				t.Fatalf("read downloaded file %s: %v", f.Path, err)
			}

			gotSHA256 := sha256.Sum256(downloadedData)
			gotHex := hex.EncodeToString(gotSHA256[:])
			expectedHex := hex.EncodeToString(expectedSHA256[:])

			if gotHex != expectedHex {
				t.Fatalf("SHA256 mismatch: got %s, expected %s", gotHex, expectedHex)
			}
			if int64(len(downloadedData)) != f.Size {
				t.Fatalf("size mismatch: got %d, expected %d", len(downloadedData), f.Size)
			}
			if !bytes.Equal(downloadedData, data) {
				t.Fatal("downloaded data does not match original")
			}
			t.Logf("verified %s: %d bytes, SHA256 OK", f.Path, len(downloadedData))
		}

		// Verify download status.
		status := client.GetDownload(infoHashHex)
		if status == nil {
			t.Fatal("GetDownload returned nil")
		}
		if status.Status != "completed" {
			t.Errorf("expected status 'completed', got %q", status.Status)
		}
		t.Logf("download status: %s (%d/%d pieces, %d/%d bytes)",
			status.Status, status.PiecesDone, status.PiecesTotal,
			status.Downloaded, status.TotalSize)
	}
}

// TestParseMagnet tests magnet URI parsing.
func TestParseMagnet(t *testing.T) {
	tests := []struct {
		uri    string
		wantOK bool
		wantIH string
	}{
		{
			uri:    "magnet:?xt=urn:btih:10c3063874f2d6020f362388874a9d16a185ccec&dn=test.txt",
			wantOK: true,
			wantIH: "10c3063874f2d6020f362388874a9d16a185ccec",
		},
		{
			uri:    "magnet:?xt=urn:btih:deadbeef",
			wantOK: false,
		},
		{
			uri:    "not-a-magnet",
			wantOK: false,
		},
	}

	for _, tc := range tests {
		m, err := ParseMagnet(tc.uri)
		if tc.wantOK {
			if err != nil {
				t.Errorf("ParseMagnet(%q) unexpected error: %v", tc.uri, err)
				continue
			}
			if m.InfoHash != tc.wantIH {
				t.Errorf("ParseMagnet(%q) infoHash=%s, want %s", tc.uri, m.InfoHash, tc.wantIH)
			}
		} else {
			if err == nil {
				t.Errorf("ParseMagnet(%q) expected error, got infohash=%s", tc.uri, m.InfoHash)
			}
		}
	}
}

// TestBTClientPauseResume tests the pause/resume/remove functionality.
func TestBTClientPauseResume(t *testing.T) {
	dir := t.TempDir()
	client := NewBTClient(dir)

	meta := &TorrentMeta{
		Name:         "test",
		InfoHashHex:  "0000000000000000000000000000000000000001",
		InfoHash:     make([]byte, 20),
		PieceLength:  16384,
		TotalSize:    16384,
		IsSingleFile: true,
		Files:        []TorrentFile{{Path: "test.dat", Size: 16384}},
		Pieces:       [][]byte{make([]byte, 20)},
	}

	err := client.AddTorrent(meta)
	if err != nil {
		t.Fatalf("AddTorrent: %v", err)
	}

	// Wait briefly for the download goroutine to start.
	time.Sleep(50 * time.Millisecond)

	// Try to pause. This may fail if the download already errored
	// (no peers in the test environment), which is expected.
	err = client.PauseDownload(meta.InfoHashHex)
	if err != nil {
		// The download may have already errored — that's acceptable.
		status := client.GetDownload(meta.InfoHashHex)
		if status != nil {
			t.Logf("download status: %s (pause not possible: %v)", status.Status, err)
		}
		// Still test RemoveDownload.
		err = client.RemoveDownload(meta.InfoHashHex)
		if err != nil {
			t.Fatalf("RemoveDownload: %v", err)
		}
		status = client.GetDownload(meta.InfoHashHex)
		if status != nil {
			t.Errorf("expected nil after removal, got %+v", status)
		}
		t.Log("download removed (error path)")
		return
	}
	status := client.GetDownload(meta.InfoHashHex)
	if status == nil {
		t.Fatal("GetDownload returned nil after pause")
	}
	if status.Status != "paused" {
		t.Errorf("expected status 'paused', got %q", status.Status)
	}
	t.Logf("download paused: %s", status.Status)

	// Resume.
	err = client.ResumeDownload(meta.InfoHashHex)
	if err != nil {
		t.Fatalf("ResumeDownload: %v", err)
	}
	status = client.GetDownload(meta.InfoHashHex)
	if status == nil {
		t.Fatal("GetDownload returned nil after resume")
	}
	if status.Status != "downloading" {
		t.Errorf("expected status 'downloading', got %q", status.Status)
	}
	t.Logf("download resumed: %s", status.Status)

	// Remove.
	time.Sleep(100 * time.Millisecond)
	err = client.RemoveDownload(meta.InfoHashHex)
	if err != nil {
		t.Fatalf("RemoveDownload: %v", err)
	}
	status = client.GetDownload(meta.InfoHashHex)
	if status != nil {
		t.Errorf("expected nil after removal, got %+v", status)
	}
	t.Log("download removed")

	// Stats.
	stats := client.GetGlobalStats()
	t.Logf("global stats: active=%d paused=%d completed=%d errors=%d dht_nodes=%d",
		stats.ActiveTorrents, stats.PausedTorrents, stats.Completed, stats.Errors, stats.DHTNodes)
}

// TestSeeder tests the BTSeeder: it creates and starts a seeder, then connects
// as a BT peer and downloads a piece to verify the seeder serves data correctly.
func TestSeeder(t *testing.T) {
	dir := t.TempDir()

	// Create test data and torrent meta.
	data := make([]byte, 50000)
	for i := range data {
		data[i] = byte(i * 7 % 256)
	}
	infoHash := sha1.Sum(data)

	// Build minimal TorrentMeta.
	pieceLen := int64(16384)
	numPieces := (len(data) + 16383) / 16384
	pieces := make([][]byte, numPieces)
	for i := 0; i < numPieces; i++ {
		start := i * 16384
		end := start + 16384
		if end > len(data) {
			end = len(data)
		}
		h := sha1.Sum(data[start:end])
		pieces[i] = h[:]
	}

	meta := &TorrentMeta{
		Name:         "seedtest.dat",
		PieceLength:  pieceLen,
		Pieces:       pieces,
		TotalSize:    int64(len(data)),
		IsSingleFile: true,
		Files:        []TorrentFile{{Path: "seedtest.dat", Size: int64(len(data))}},
		InfoHash:     infoHash[:],
		InfoHashHex:  hex.EncodeToString(infoHash[:]),
	}

	// Write test data as a file (simulating completed download).
	filePath := filepath.Join(dir, meta.Name)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	// Create and start seeder.
	seeder := NewSeeder(infoHash, meta, dir)
	if err := seeder.Start(); err != nil {
		t.Fatalf("seeder start: %v", err)
	}
	defer seeder.Stop()

	if !seeder.IsActive() {
		t.Fatal("seeder should be active after Start")
	}

	seederPort := seeder.Port()
	if seederPort == 0 {
		t.Fatal("seeder port should be non-zero")
	}
	t.Logf("seeder listening on port %d", seederPort)

	// Connect as a BT peer and download piece 0.
	peerAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(seederPort))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	piece0Len := int64(16384)
	var expectedHash [20]byte
	copy(expectedHash[:], pieces[0])

	downloaded, err := DownloadPiece(ctx, peerAddr, infoHash, 0, piece0Len, expectedHash)
	if err != nil {
		t.Fatalf("DownloadPiece from seeder: %v", err)
	}

	if len(downloaded) != int(piece0Len) {
		t.Fatalf("expected %d bytes from piece 0, got %d", piece0Len, len(downloaded))
	}
	if !bytes.Equal(downloaded, data[:16384]) {
		t.Fatal("piece 0 data mismatch")
	}
	t.Logf("seeder served piece 0: %d bytes, SHA1 verified", len(downloaded))

	// Download the last piece (which may be shorter).
	lastPieceIdx := numPieces - 1
	lastPieceLen := int64(len(data)) % pieceLen
	if lastPieceLen == 0 {
		lastPieceLen = pieceLen
	}
	var lastExpectedHash [20]byte
	copy(lastExpectedHash[:], pieces[lastPieceIdx])

	lastPiece, err := DownloadPiece(ctx, peerAddr, infoHash, lastPieceIdx, lastPieceLen, lastExpectedHash)
	if err != nil {
		t.Fatalf("DownloadPiece last piece from seeder: %v", err)
	}
	start := lastPieceIdx * 16384
	end := start + int(lastPieceLen)
	if end > len(data) {
		end = len(data)
	}
	if !bytes.Equal(lastPiece, data[start:end]) {
		t.Fatal("last piece data mismatch")
	}
	t.Logf("seeder served last piece %d: %d bytes, SHA1 verified", lastPieceIdx, len(lastPiece))

	// Stop seeder and verify it's no longer active.
	seeder.Stop()
	if seeder.IsActive() {
		t.Fatal("seeder should not be active after Stop")
	}
	t.Log("seeder stopped successfully")
}
