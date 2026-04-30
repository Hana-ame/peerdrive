// Package p2p_bt implements a BitTorrent wire protocol seeder that serves
// completed downloads to peers over TCP connections.
package p2p_bt

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"peerdrive/internal/log"
)

// BTSeeder serves a completed torrent download to BT wire protocol peers.
// It listens on a TCP port, accepts incoming connections, performs the
// BitTorrent handshake, and responds to piece requests with data from the
// downloaded files on disk.
type BTSeeder struct {
	infoHash [20]byte
	meta     *TorrentMeta
	dataDir  string
	port     int
	listener net.Listener
	stopCh   chan struct{}
	wg       sync.WaitGroup
	started  bool
	mu       sync.Mutex
}

// NewSeeder creates a new BTSeeder for a completed download.
// infoHash is the 20-byte torrent info hash.
// meta is the parsed torrent metadata (needed for piece layout).
// dataDir is the directory containing the downloaded files.
func NewSeeder(infoHash [20]byte, meta *TorrentMeta, dataDir string) *BTSeeder {
	return &BTSeeder{
		infoHash: infoHash,
		meta:     meta,
		dataDir:  dataDir,
		stopCh:   make(chan struct{}),
	}
}

// Start begins listening for incoming peer connections on a random TCP port.
func (s *BTSeeder) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return fmt.Errorf("seeder already started")
	}

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return fmt.Errorf("seeder listen: %w", err)
	}

	s.listener = listener
	s.port = listener.Addr().(*net.TCPAddr).Port
	s.started = true

	s.wg.Add(1)
	go s.acceptLoop()

	log.LogInfo("[bt-seeder] started on port %d for infohash=%s name=%q",
		s.port, hex.EncodeToString(s.infoHash[:]), s.meta.Name)
	return nil
}

// Stop shuts down the seeder listener and waits for active connections to drain.
func (s *BTSeeder) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return
	}

	close(s.stopCh)
	if s.listener != nil {
		s.listener.Close()
	}
	s.wg.Wait()
	s.started = false

	log.LogInfo("[bt-seeder] stopped for infohash=%s", hex.EncodeToString(s.infoHash[:]))
}

// Port returns the TCP port the seeder is listening on.
func (s *BTSeeder) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

// IsActive returns true if the seeder is currently running.
func (s *BTSeeder) IsActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started
}

// acceptLoop accepts incoming TCP connections and spawns a handler for each.
func (s *BTSeeder) acceptLoop() {
	defer s.wg.Done()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.stopCh:
				return
			default:
				log.LogWarn("[bt-seeder] accept error: %v", err)
				return
			}
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serveConn(conn)
		}()
	}
}

// serveConn handles a single BT wire protocol peer connection.
// It performs the handshake, sends a bitfield, and responds to piece requests.
func (s *BTSeeder) serveConn(conn net.Conn) {
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

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
		log.LogWarn("[bt-seeder] infohash mismatch from peer")
		return
	}

	// 2. Send handshake response (68 bytes).
	resp := make([]byte, 68)
	resp[0] = 19
	copy(resp[1:20], btProtocolName)
	resp[27] |= 0x01 // DHT support
	copy(resp[28:48], s.infoHash[:])
	var pid [20]byte
	copy(pid[:], []byte("-PDSEED-12345678901"))
	copy(resp[48:68], pid[:])
	if _, err := conn.Write(resp); err != nil {
		return
	}

	// 3. Build and send bitfield message.
	numPieces := len(s.meta.Pieces)
	bfLen := (numPieces + 7) / 8
	bf := make([]byte, bfLen)
	for i := 0; i < numPieces; i++ {
		bf[i/8] |= 1 << (7 - uint(i%8))
	}

	// Write bitfield: [4-byte length][1-byte id][bitfield]
	msgLen := uint32(1 + len(bf))
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], msgLen)
	if _, err := conn.Write(hdr[:]); err != nil {
		return
	}
	conn.Write([]byte{msgBitfield})
	conn.Write(bf)

	// 4. Handle messages from peer.
	for {
		select {
		case <-s.stopCh:
			return
		default:
		}

		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

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

			if length > 1<<17 { // max 128KB per request
				continue
			}

			block, err := s.readBlock(int(pIdx), int64(off), int64(length))
			if err != nil {
				log.LogWarn("[bt-seeder] read block piece=%d offset=%d: %v", pIdx, off, err)
				continue
			}

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

// readBlock reads a block of data from the torrent's files on disk.
// It maps the piece index + offset to the correct byte range in the file(s).
func (s *BTSeeder) readBlock(pieceIndex int, offset, length int64) ([]byte, error) {
	// Compute absolute byte position in the torrent's data stream.
	absOffset := int64(pieceIndex)*s.meta.PieceLength + offset

	// Verify the range is within the total torrent size.
	if absOffset+length > s.meta.TotalSize {
		return nil, fmt.Errorf("block beyond file bounds")
	}

	// For single-file torrents, read directly from the single file.
	if s.meta.IsSingleFile && len(s.meta.Files) == 1 {
		filePath := filepath.Join(s.dataDir, s.meta.Name)
		f, err := os.Open(filePath)
		if err != nil {
			return nil, err
		}
		defer f.Close()

		data := make([]byte, length)
		_, err = f.ReadAt(data, absOffset)
		if err != nil {
			return nil, err
		}
		return data, nil
	}

	// For multi-file torrents, locate the right file and read the block.
	var fileOffset int64
	for _, tf := range s.meta.Files {
		fileSize := tf.Size
		if absOffset < fileOffset+fileSize {
			// This file contains our starting position.
			readStart := absOffset - fileOffset
			filePath := filepath.Join(s.dataDir, tf.Path)
			f, err := os.Open(filePath)
			if err != nil {
				return nil, err
			}
			defer f.Close()

			data := make([]byte, length)
			n, err := f.ReadAt(data, readStart)
			if err != nil && err != io.EOF {
				return nil, err
			}
			return data[:n], nil
		}
		fileOffset += fileSize
	}

	return nil, fmt.Errorf("block offset %d out of range", absOffset)
}
