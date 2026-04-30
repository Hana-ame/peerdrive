// BitTorrent wire protocol implementation -- handshake, message read/write,
// single piece download (with SHA1 verification), and multi-piece download.
package p2p_bt

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"time"

	"peerdrive/internal/log"
)

const (
	// BitTorrent wire protocol constants.
	btProtocolName = "BitTorrent protocol"

	// Message IDs.
	msgChoke         = 0
	msgUnchoke       = 1
	msgInterested    = 2
	msgNotInterested = 3
	msgHave          = 4
	msgBitfield      = 5
	msgRequest       = 6
	msgPiece         = 7
	msgCancel        = 8
	msgPort          = 9

	// Default block size for piece requests.
	defaultBlockSize = 16 * 1024 // 16 KiB

	// Handshake timeout.
	handshakeTimeout = 10 * time.Second

	// Read/write timeouts for piece data.
	pieceTimeout = 30 * time.Second
)

// Bitfield tracks which pieces a peer has.
type Bitfield []byte

// HasPiece checks if the bitfield indicates the given piece index is present.
func (bf Bitfield) HasPiece(index int) bool {
	byteIdx := index / 8
	bitIdx := 7 - uint(index%8) // big-endian bit ordering
	return byteIdx < len(bf) && (bf[byteIdx]>>bitIdx)&1 == 1
}

// SetPiece marks the given piece index as present in the bitfield.
func (bf Bitfield) SetPiece(index int) {
	byteIdx := index / 8
	if byteIdx >= len(bf) {
		return
	}
	bitIdx := 7 - uint(index%8)
	bf[byteIdx] |= 1 << bitIdx
}

// NumPieces returns the number of bits (pieces) in the bitfield.
func (bf Bitfield) NumPieces() int {
	return len(bf) * 8
}

// NewBitfield creates a bitfield for the given number of pieces.
func NewBitfield(numPieces int) Bitfield {
	return make(Bitfield, (numPieces+7)/8)
}

// btPeerID returns a 20-byte peer ID identifying this client.
func btPeerID() [20]byte {
	var id [20]byte
	copy(id[:], []byte("-PD0001-123456789012"))
	return id
}

// btHandshake performs the BitTorrent handshake over an existing TCP connection.
// It validates the peer's response and returns the peer's 20-byte ID.
func btHandshake(conn net.Conn, infoHash [20]byte) (peerID [20]byte, err error) {
	defer log.LogDuration("BT.btHandshake")()
	log.LogDebug("[bt-wire] handshake begin: sending infohash=%s", hex.EncodeToString(infoHash[:]))

	_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))

	// Build handshake: <19><protocol><8 reserved><20 infohash><20 peerid>
	hs := make([]byte, 68)
	hs[0] = 19
	copy(hs[1:20], btProtocolName)
	// Reserved bytes (bytes 20-27) -- all zero is fine for basic protocol.
	// Byte 20 bit 5 (DHT support) could be set: hs[27] |= 0x01
	hs[27] |= 0x01 // support DHT
	copy(hs[28:48], infoHash[:])
	pid := btPeerID()
	copy(hs[48:68], pid[:])

	if _, err := conn.Write(hs); err != nil {
		log.LogError("[bt-wire] handshake write failed: %v", err)
		return peerID, fmt.Errorf("handshake write: %w", err)
	}
	log.LogDebug("[bt-wire] handshake sent %d bytes, waiting for response", len(hs))

	// Read response: 68 bytes.
	resp := make([]byte, 68)
	if _, err := io.ReadFull(conn, resp); err != nil {
		log.LogError("[bt-wire] handshake read failed: %v", err)
		return peerID, fmt.Errorf("handshake read: %w", err)
	}
	log.LogDebug("[bt-wire] handshake response received %d bytes", len(resp))

	// Validate protocol string.
	if resp[0] != 19 || string(resp[1:20]) != btProtocolName {
		return peerID, fmt.Errorf("handshake: invalid protocol string")
	}

	// Validate infohash matches.
	var respInfoHash [20]byte
	copy(respInfoHash[:], resp[28:48])
	if respInfoHash != infoHash {
		return peerID, fmt.Errorf("handshake: infohash mismatch (sent %s, got %s)",
			hex.EncodeToString(infoHash[:]),
			hex.EncodeToString(respInfoHash[:]))
	}
	log.LogInfo("[bt-wire] handshake infohash verified OK: %s", hex.EncodeToString(respInfoHash[:]))

	copy(peerID[:], resp[48:68])
	log.LogInfo("[bt-wire] handshake successful with peer=%s", hex.EncodeToString(peerID[:]))
	return peerID, nil
}

// btReadMessage reads a single BT wire protocol message from the connection.
// Returns (messageID, payload, error). msgID 255 indicates a keep-alive.
func btReadMessage(conn net.Conn) (uint8, []byte, error) {
	// 4-byte length prefix (big-endian), excluding the length field itself.
	var lenBuf [4]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return 0, nil, fmt.Errorf("read msg length: %w", err)
	}
	msgLen := binary.BigEndian.Uint32(lenBuf[:])

	if msgLen == 0 {
		// Keep-alive -- no message ID, no payload.
		return 255, nil, nil // 255 indicates keep-alive
	}

	// Read message ID + payload.
	msg := make([]byte, msgLen)
	if _, err := io.ReadFull(conn, msg); err != nil {
		return 0, nil, fmt.Errorf("read msg body: %w", err)
	}

	return msg[0], msg[1:], nil
}

// btSendMessage sends a BT wire protocol message.
func btSendMessage(conn net.Conn, msgID uint8, payload []byte) error {
	length := uint32(1 + len(payload)) // 1 byte for msgID + payload
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], length)
	if _, err := conn.Write(buf[:]); err != nil {
		return fmt.Errorf("write msg length: %w", err)
	}
	if _, err := conn.Write([]byte{msgID}); err != nil {
		return fmt.Errorf("write msg id: %w", err)
	}
	if len(payload) > 0 {
		if _, err := conn.Write(payload); err != nil {
			return fmt.Errorf("write msg payload: %w", err)
		}
	}
	return nil
}

// btSendRequest sends a request message for a block.
func btSendRequest(conn net.Conn, pieceIndex, offset, length uint32) error {
	payload := make([]byte, 12)
	binary.BigEndian.PutUint32(payload[0:4], pieceIndex)
	binary.BigEndian.PutUint32(payload[4:8], offset)
	binary.BigEndian.PutUint32(payload[8:12], length)
	return btSendMessage(conn, msgRequest, payload)
}

// DownloadPiece downloads a single piece from the given peer over the BT wire
// protocol. It connects to the peer, performs the handshake, waits to be
// unchoked, requests blocks, reassembles them, and verifies the SHA1 hash.
func DownloadPiece(
	ctx context.Context,
	peerAddr string,
	infoHash [20]byte,
	pieceIndex int,
	pieceLength int64,
	expectedHash [20]byte,
) ([]byte, error) {
	defer log.LogDuration("BT.DownloadPiece")()
	log.LogDebug("[bt-wire] DownloadPiece peer=%s piece=%d len=%d",
		peerAddr, pieceIndex, pieceLength)

	// Dial peer with context.
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", peerAddr)
	if err != nil {
		log.LogError("[bt-wire] dial %s failed: %v", peerAddr, err)
		return nil, fmt.Errorf("dial peer %s: %w", peerAddr, err)
	}
	defer conn.Close()
	log.LogDebug("[bt-wire] connected to %s", peerAddr)

	// Handshake.
	peerID, err := btHandshake(conn, infoHash)
	if err != nil {
		log.LogError("[bt-wire] handshake with %s failed: %v", peerAddr, err)
		return nil, fmt.Errorf("handshake %s: %w", peerAddr, err)
	}
	_ = peerID // peer identity available if needed

	// Message loop -- wait for unchoke.
	unchoked := false
	interested := false

	_ = conn.SetDeadline(time.Now().Add(pieceTimeout))

	for !unchoked {
		msgID, payload, err := btReadMessage(conn)
		if err != nil {
			return nil, fmt.Errorf("read message from %s: %w", peerAddr, err)
		}

		switch msgID {
		case msgUnchoke:
			unchoked = true
			log.LogDebug("[bt-wire] unchoked by %s", peerAddr)
		case msgChoke:
			// Peer choked us. If we haven't sent interested yet, we can wait.
			unchoked = false
		case msgBitfield:
			log.LogDebug("[bt-wire] bitfield from %s (%d bytes)", peerAddr, len(payload))
			// Check if peer has this piece.
			bf := Bitfield(payload)
			if !bf.HasPiece(pieceIndex) {
				return nil, fmt.Errorf("peer %s does not have piece %d", peerAddr, pieceIndex)
			}
			log.LogDebug("[bt-wire] peer %s has piece %d (from bitfield)", peerAddr, pieceIndex)
		case msgHave:
			if len(payload) >= 4 {
				idx := binary.BigEndian.Uint32(payload[:4])
				if int(idx) == pieceIndex && !unchoked {
					log.LogDebug("[bt-wire] peer %s has piece %d (from have msg)", peerAddr, pieceIndex)
				}
			}
		case 255: // keep-alive
			_ = conn.SetDeadline(time.Now().Add(pieceTimeout))
			continue
		default:
			log.LogDebug("[bt-wire] msg %d from %s (%d bytes)", msgID, peerAddr, len(payload))
		}

		// After we've read initial messages, send interested.
		if !interested {
			if err := btSendMessage(conn, msgInterested, nil); err != nil {
				return nil, fmt.Errorf("send interested: %w", err)
			}
			interested = true
			log.LogDebug("[bt-wire] sent interested to %s", peerAddr)
		}
	}

	// We are unchoked. Request blocks and assemble the piece.
	data := make([]byte, pieceLength)
	var offset int64
	blockSize := int64(defaultBlockSize)
	blockNum := 0

	for offset < pieceLength {
		// Determine block size for this request.
		blockLen := blockSize
		if offset+blockLen > pieceLength {
			blockLen = pieceLength - offset
		}

		// Send request.
		if err := btSendRequest(conn,
			uint32(pieceIndex),
			uint32(offset),
			uint32(blockLen),
		); err != nil {
			return nil, fmt.Errorf("send request piece=%d offset=%d: %w",
				pieceIndex, offset, err)
		}
		log.LogDebug("[bt-wire] sent request piece=%d offset=%d len=%d (block %d)",
			pieceIndex, offset, blockLen, blockNum)
		blockNum++

		// Read piece message(s). We may get multiple messages -- handle other
		// message types in between.
		received := false
		for !received {
			_ = conn.SetDeadline(time.Now().Add(pieceTimeout))

			msgID, payload, err := btReadMessage(conn)
			if err != nil {
				return nil, fmt.Errorf("read piece response: %w", err)
			}

			switch msgID {
			case msgPiece:
				if len(payload) < 8 {
					return nil, fmt.Errorf("piece msg too short (%d bytes)", len(payload))
				}
				idx := binary.BigEndian.Uint32(payload[0:4])
				beg := binary.BigEndian.Uint32(payload[4:8])
				blockData := payload[8:]

				if int(idx) != pieceIndex {
					log.LogWarn("[bt-wire] got piece %d, expected %d", idx, pieceIndex)
					continue
				}
				if int64(beg) != offset {
					log.LogWarn("[bt-wire] got offset %d, expected %d", beg, offset)
					continue
				}

				copy(data[beg:], blockData)
				offset += int64(len(blockData))
				received = true

				log.LogInfo("[bt-wire] received block piece=%d offset=%d size=%d (total received=%d/%d)",
					pieceIndex, beg, len(blockData), offset, pieceLength)

			case msgChoke:
				return nil, fmt.Errorf("peer choked us mid-download")

			case msgUnchoke:
				// Still unchoked, continue.
				continue

			case msgHave:
				// Peer advertising another piece, ignore.
				continue

			case 255: // keep-alive
				_ = conn.SetDeadline(time.Now().Add(pieceTimeout))
				continue

			default:
				log.LogDebug("[bt-wire] ignoring msg %d during piece download", msgID)
			}
		}
	}

	// Verify SHA1 hash.
	hash := sha1.Sum(data)
	if hash != expectedHash {
		return nil, fmt.Errorf("piece %d hash mismatch: got %s, expected %s",
			pieceIndex,
			hex.EncodeToString(hash[:]),
			hex.EncodeToString(expectedHash[:]),
		)
	}

	log.LogInfo("[bt-wire] downloaded piece %d from %s (%d bytes, SHA1 verified)",
		pieceIndex, peerAddr, len(data))
	return data, nil
}

// DownloadAllPieces downloads all pieces from the given peer, verifying each
// piece's SHA1 hash. Returns a map of file path to data bytes.
func DownloadAllPieces(
	ctx context.Context,
	peerAddr string,
	infoHash [20]byte,
	meta *TorrentMeta,
	concurrency int,
) (map[string][]byte, error) {
	defer log.LogDuration("BT.DownloadAllPieces")()
	log.LogInfo("[bt-wire] DownloadAllPieces peer=%s name=%s pieces=%d concurrency=%d",
		peerAddr, meta.Name, len(meta.Pieces), concurrency)

	if concurrency <= 0 {
		concurrency = 4
	}

	type pieceResult struct {
		index int
		data  []byte
		err   error
	}

	numPieces := len(meta.Pieces)
	pieces := make([][]byte, numPieces)
	resultCh := make(chan pieceResult, numPieces)

	// Determine the length of each piece.
	pieceLen := func(index int) int64 {
		if index == numPieces-1 {
			lastPieceSize := meta.TotalSize % meta.PieceLength
			if lastPieceSize > 0 {
				return lastPieceSize
			}
		}
		return meta.PieceLength
	}

	// Launch workers.
	sem := make(chan struct{}, concurrency)
	for i := 0; i < numPieces; i++ {
		i := i
		go func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			var expectedHash [20]byte
			copy(expectedHash[:], meta.Pieces[i])

			data, err := DownloadPiece(ctx, peerAddr, infoHash, i,
				pieceLen(i), expectedHash)
			resultCh <- pieceResult{index: i, data: data, err: err}
		}()
	}

	// Collect results.
	for i := 0; i < numPieces; i++ {
		res := <-resultCh
		if res.err != nil {
			return nil, fmt.Errorf("piece %d: %w", res.index, res.err)
		}
		pieces[res.index] = res.data
	}

	// Assemble files from pieces.
	result := make(map[string][]byte)
	if meta.IsSingleFile {
		var fullData []byte
		for _, p := range pieces {
			fullData = append(fullData, p...)
		}
		result[meta.Name] = fullData
	} else {
		// For multi-file, reconstruct full data and then split by file boundaries.
		var fullData []byte
		for _, p := range pieces {
			fullData = append(fullData, p...)
		}
		offset := int64(0)
		for _, f := range meta.Files {
			result[f.Path] = fullData[offset : offset+f.Size]
			offset += f.Size
		}
	}

	log.LogInfo("[bt-wire] DownloadAllPieces completed for %s (%d files)", meta.Name, len(result))
	return result, nil
}

// connectToPeer opens a TCP connection to a peer address.
func connectToPeer(addr string) (net.Conn, error) {
	log.LogDebug("[bt-wire] connecting to peer %s", addr)
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		log.LogError("[bt-wire] connect to %s failed: %v", addr, err)
		return nil, fmt.Errorf("connect to %s: %w", addr, err)
	}
	log.LogDebug("[bt-wire] connected to %s", addr)
	return conn, nil
}

// doHandshake performs a BT handshake over an existing connection with the
// given infohash. It logs the process with [bt-wire] prefix.
func doHandshake(conn net.Conn, infoHash [20]byte) error {
	peerID, err := btHandshake(conn, infoHash)
	if err != nil {
		return fmt.Errorf("handshake: %w", err)
	}
	log.LogDebug("[bt-wire] handshake done, peer id=%s", hex.EncodeToString(peerID[:]))
	return nil
}
