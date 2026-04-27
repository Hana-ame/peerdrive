// Package service 提供 P2P 中继转发服务。
// 当一个客户端请求的文件只存在于 NAT 后的节点时，VPS 中继节点
// 通过 libp2p stream 连接到目标节点，获取文件数据并流式转发给
// HTTP 客户端。
//
// 支持的协议：
//   /peerdrive/exchange/1.0.0  — 全量文件交换（主协议）
//   /peerdrive/chunk/1.0.0    — 范围请求支持（Content-Range）
//
// 路由注册示例（在 router.go 中）：
//
//	relaySvc := service.NewRelayService(p2pSvc, cfg)
//	r.GET("/relay/proxy", relaySvc.ProxyDownload)
//
// curl 测试：
//
//	# 通过中继下载文件（直连后端节点）
//	curl -o file.bin "http://localhost:3000/relay/proxy?hash=<sha256>&peer=<peerID>"
//
//	# 范围请求（断点续传）
//	curl -o file.bin -H "Range: bytes=0-1048575" \
//	  "http://localhost:3000/relay/proxy?hash=<sha256>&peer=<peerID>"

package service

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"peerdrive/internal/repository"
	"peerdrive/pkg/hashutil"

	"github.com/gin-gonic/gin"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const (
	// RelayExchangeTimeout is the read deadline for the initial exchange
	// handshake (sending hash + reading status line).
	RelayExchangeTimeout = 30 * time.Second

	// RelayTransferTimeout is the total time allowed for a relay transfer,
	// including the handshake and the full data stream.
	RelayTransferTimeout = 5 * time.Minute

	// RelayConnectTimeout is the timeout for connecting to a remote peer.
	RelayConnectTimeout = 15 * time.Second

	// relayCopyBufSize is the buffer size used when copying stream data
	// to the HTTP response writer.
	relayCopyBufSize = 32 * 1024
)

// RelayService forwards file requests from HTTP clients to P2P peers
// that are behind NAT and cannot be reached via HTTP directly.
//
// The public VPS node (bwh.moonchan.xyz) acts as the relay: it maintains
// persistent libp2p connections to known peers and, upon receiving a relay
// request, opens an exchange-protocol stream to the target peer, fetches
// the file content, and streams it back over HTTP.
//
// Zero value: not usable. Use NewRelayService to construct.
type RelayService struct {
	p2p *P2PService
}

// NewRelayService 创建中继服务实例，需传入已启动的 P2PService。
func NewRelayService(p2p *P2PService) *RelayService {
	return &RelayService{p2p: p2p}
}

// ──────────────────────────────────────────────
//  Internal helpers
// ──────────────────────────────────────────────

// streamReadCloser wraps a Reader and a Closer into a single io.ReadCloser.
type streamReadCloser struct {
	io.Reader
	closer io.Closer
}

func (s *streamReadCloser) Close() error {
	if s.closer != nil {
		return s.closer.Close()
	}
	return nil
}

// connectToPeer ensures the relay is connected to the given peer. If the
// peer is not currently connected, it looks up the peer's address in the
// discovered-peers cache and attempts to connect.
func (r *RelayService) connectToPeer(ctx context.Context, peerID peer.ID) error {
	if !r.p2p.IsEnabled() {
		return fmt.Errorf("p2p not enabled")
	}
	if r.p2p.Host.Network().Connectedness(peerID) == network.Connected {
		return nil
	}
	for _, pi := range r.p2p.GetDiscoveredPeers() {
		if pi.ID == peerID {
			connCtx, cancel := context.WithTimeout(ctx, RelayConnectTimeout)
			defer cancel()
			return r.p2p.Host.Connect(connCtx, pi)
		}
	}
	return fmt.Errorf("peer %s is not connected or discovered; cannot relay", peerID)
}

// openExchangeStream opens an exchange-protocol stream to the target peer,
// sends the file hash, and parses the status response. On success it returns
// an io.ReadCloser positioned at the start of the file payload, along with
// the total file size reported by the peer.
//
// The returned reader is bounded to exactly fileSize bytes. The caller MUST
// close the reader when done.
func (r *RelayService) openExchangeStream(ctx context.Context, peerID peer.ID, hash string) (io.ReadCloser, int64, error) {
	if err := r.connectToPeer(ctx, peerID); err != nil {
		return nil, 0, err
	}

	stream, err := r.p2p.Host.NewStream(ctx, peerID, protocol.ID(ProtocolExchange))
	if err != nil {
		return nil, 0, fmt.Errorf("open exchange stream to %s: %w", peerID, err)
	}

	// Short deadline for the handshake phase.
	stream.SetReadDeadline(time.Now().Add(RelayExchangeTimeout))

	if _, err := fmt.Fprintf(stream, "%s\n", hash); err != nil {
		stream.Close()
		return nil, 0, fmt.Errorf("send exchange request: %w", err)
	}

	reader := bufio.NewReader(stream)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		stream.Close()
		return nil, 0, fmt.Errorf("read exchange status from %s: %w", peerID, err)
	}

	var fileSize int64
	if _, err := fmt.Sscanf(statusLine, "OK %d", &fileSize); err != nil || fileSize <= 0 {
		stream.Close()
		return nil, 0, fmt.Errorf("peer %s exchange error: %s", peerID, strings.TrimSpace(statusLine))
	}

	// Extend deadline for the full data-transfer phase.
	stream.SetReadDeadline(time.Now().Add(RelayTransferTimeout))

	// Return a reader bounded to fileSize bytes, reading through the
	// buffered reader so any bytes pre-read beyond the status line are
	// accounted for.
	return &streamReadCloser{
		Reader: io.LimitReader(reader, fileSize),
		closer: stream,
	}, fileSize, nil
}

// openChunkStream opens a chunk-protocol stream and requests a specific
// byte range [offset, offset+length). It returns a reader for the raw
// chunk payload.
func (r *RelayService) openChunkStream(ctx context.Context, peerID peer.ID, hash string, offset, length int64) (io.ReadCloser, error) {
	if err := r.connectToPeer(ctx, peerID); err != nil {
		return nil, err
	}

	stream, err := r.p2p.Host.NewStream(ctx, peerID, protocol.ID(ProtocolChunk))
	if err != nil {
		return nil, fmt.Errorf("open chunk stream to %s: %w", peerID, err)
	}

	stream.SetReadDeadline(time.Now().Add(RelayTransferTimeout))

	if _, err := fmt.Fprintf(stream, "CHUNK %s %d %d\n", hash, offset, length); err != nil {
		stream.Close()
		return nil, fmt.Errorf("send chunk request: %w", err)
	}

	return &streamReadCloser{
		Reader: stream,
		closer: stream,
	}, nil
}

// requestFileSize queries the peer for the file size using the "SIZE"
// sub-command of the exchange protocol.
func (r *RelayService) requestFileSize(ctx context.Context, peerID peer.ID, hash string) (int64, error) {
	stream, err := r.p2p.Host.NewStream(ctx, peerID, protocol.ID(ProtocolExchange))
	if err != nil {
		return 0, fmt.Errorf("open size stream: %w", err)
	}
	defer stream.Close()

	stream.SetReadDeadline(time.Now().Add(RelayExchangeTimeout))

	if _, err := fmt.Fprintf(stream, "SIZE %s\n", hash); err != nil {
		return 0, fmt.Errorf("send size request: %w", err)
	}

	var status string
	var size int64
	if _, err := fmt.Fscanf(stream, "%s %d\n", &status, &size); err != nil {
		return 0, fmt.Errorf("read size response: %w", err)
	}
	if status != "OK" {
		return 0, fmt.Errorf("peer size error: %s", status)
	}
	return size, nil
}

// ──────────────────────────────────────────────
//  Public API
// ──────────────────────────────────────────────

// RelayFileRequest 通过 libp2p exchange 协议从中继对端获取文件并返回流式读取器。
func (r *RelayService) RelayFileRequest(ctx context.Context, hash string, targetPeerID peer.ID) (io.ReadCloser, error) {
	reader, _, err := r.openExchangeStream(ctx, targetPeerID, hash)
	return reader, err
}

// RelayFileToHTTP streams a file from a P2P peer directly to an HTTP
// ResponseWriter. It supports HTTP range requests via the Content-Range
// mechanism.
//
// Parameters:
//   - w:            the HTTP response writer to stream into.
//   - hash:         the SHA-256 content hash of the requested file.
//   - targetPeerID: the libp2p peer ID of the node hosting the file.
//   - rangeHeader:  the value of the HTTP Range header (or "" for the
//                   full file). Example: "bytes=0-1048575".
//
// The method sets Content-Type, Content-Length, and (when applicable)
// Content-Range headers before writing the response body.
func (r *RelayService) RelayFileToHTTP(w http.ResponseWriter, hash string, targetPeerID peer.ID, rangeHeader string) error {
	ctx, cancel := context.WithTimeout(context.Background(), RelayTransferTimeout)
	defer cancel()

	if !r.p2p.IsEnabled() {
		return fmt.Errorf("p2p not enabled on this node")
	}

	if err := r.connectToPeer(ctx, targetPeerID); err != nil {
		return fmt.Errorf("connect to peer %s: %w", targetPeerID, err)
	}

	// Determine file size.
	fileSize, err := r.requestFileSize(ctx, targetPeerID, hash)
	if err != nil {
		return fmt.Errorf("get file size for %s: %w", hash, err)
	}

	contentType := detectContentType(hash)

	// Handle HTTP range request.
	if rangeHeader != "" {
		start, end, ok := parseRange(rangeHeader, fileSize)
		if ok {
			return r.serveRange(ctx, w, targetPeerID, hash, start, end, fileSize, contentType)
		}
		// Invalid or unsatisfiable range: fall through to serve full file.
	}

	// No range (or invalid range): serve the entire file.
	return r.serveFull(ctx, w, targetPeerID, hash, fileSize, contentType)
}

// serveFull streams the complete file from the exchange protocol to the
// HTTP response writer.
func (r *RelayService) serveFull(ctx context.Context, w http.ResponseWriter, peerID peer.ID, hash string, fileSize int64, contentType string) error {
	dataReader, _, err := r.openExchangeStream(ctx, peerID, hash)
	if err != nil {
		return err
	}
	defer dataReader.Close()

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(fileSize, 10))
	w.WriteHeader(http.StatusOK)

	_, err = io.Copy(w, dataReader)
	if err != nil {
		return fmt.Errorf("stream full file: %w", err)
	}
	return nil
}

// serveRange streams a byte range of the file using the chunk protocol
// and writes a 206 Partial Content response.
func (r *RelayService) serveRange(ctx context.Context, w http.ResponseWriter, peerID peer.ID, hash string, start, end, fileSize int64, contentType string) error {
	chunkSize := end - start + 1

	chunkReader, err := r.openChunkStream(ctx, peerID, hash, start, chunkSize)
	if err != nil {
		return fmt.Errorf("open chunk stream for range %d-%d: %w", start, end, err)
	}
	defer chunkReader.Close()

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, fileSize))
	w.Header().Set("Content-Length", strconv.FormatInt(chunkSize, 10))
	w.WriteHeader(http.StatusPartialContent)

	_, err = io.CopyN(w, chunkReader, chunkSize)
	if err != nil {
		return fmt.Errorf("stream range %d-%d: %w", start, end, err)
	}
	return nil
}

// ProxyDownload is a Gin HTTP handler for GET /relay/proxy.
//
// Query parameters:
//   - hash (required): the SHA-256 content hash of the file to relay.
//   - peer (required): the libp2p peer ID of the node hosting the file.
//
// The handler supports the standard HTTP Range header for partial
// content delivery (Content-Range / 206 Partial Content).
//
// Registration example:
//
//	relaySvc := service.NewRelayService(p2pSvc)
//	r.GET("/relay/proxy", relaySvc.ProxyDownload)
func (r *RelayService) ProxyDownload(c *gin.Context) {
	hash := c.Query("hash")
	peerStr := c.Query("peer")

	if !hashutil.IsValidSHA256(hash) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or missing 'hash' parameter; expected 64-char hex SHA-256"})
		return
	}
	if peerStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing required 'peer' parameter"})
		return
	}

	targetPeer, err := peer.Decode(peerStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid peer ID %q: %s", peerStr, err)})
		return
	}

	if !r.p2p.IsEnabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "P2P is not enabled on this node"})
		return
	}

	rangeHeader := c.GetHeader("Range")

	if err := r.RelayFileToHTTP(c.Writer, hash, targetPeer, rangeHeader); err != nil {
		// If headers have already been sent (streaming in progress),
		// we can no longer write a JSON error response. Gin will log
		// the error internally and terminate the connection.
		if !c.Writer.Written() {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "relay failed: " + err.Error()})
		}
	}
}

// ──────────────────────────────────────────────
//  Utility functions
// ──────────────────────────────────────────────

// detectContentType returns an appropriate Content-Type for the given
// content hash. It first checks the file_meta table for a stored MIME
// type. If none is found it falls back to guessing from the stored
// filename extension. The final fallback is application/octet-stream.
func detectContentType(hash string) string {
	meta, err := repository.GetFileMeta(hash)
	if err == nil && meta != nil {
		if meta.MimeType != "" {
			return meta.MimeType
		}
		if meta.Filename != "" {
			ext := filepath.Ext(meta.Filename)
			if mimeType := mime.TypeByExtension(ext); mimeType != "" {
				return mimeType
			}
		}
	}
	return "application/octet-stream"
}

// parseRange parses an HTTP Range header value and returns the
// 0-indexed inclusive byte range (start, end) plus a boolean indicating
// success.
//
// Supported forms:
//
//	bytes=0-499       → first 500 bytes
//	bytes=500-999     → a specific range
//	bytes=500-        → from byte 500 to end-of-file
//	bytes=-500        → last 500 bytes (suffix range)
//
// If the range is unsatisfiable (start beyond file size, etc.) the
// function returns ok=false, allowing the caller to serve the full
// entity instead.
func parseRange(rangeVal string, fileSize int64) (start, end int64, ok bool) {
	if fileSize <= 0 {
		return 0, 0, false
	}
	if !strings.HasPrefix(rangeVal, "bytes=") {
		return 0, 0, false
	}
	rangeVal = strings.TrimPrefix(rangeVal, "bytes=")

	parts := strings.SplitN(rangeVal, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}

	startStr := strings.TrimSpace(parts[0])
	endStr := strings.TrimSpace(parts[1])

	// Suffix range: "bytes=-500" → last 500 bytes.
	if startStr == "" {
		suffix, err := strconv.ParseInt(endStr, 10, 64)
		if err != nil || suffix <= 0 {
			return 0, 0, false
		}
		if suffix > fileSize {
			suffix = fileSize
		}
		return fileSize - suffix, fileSize - 1, true
	}

	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 || start >= fileSize {
		return 0, 0, false
	}

	// Open-ended range: "bytes=500-" → from start to end of file.
	if endStr == "" {
		return start, fileSize - 1, true
	}

	end, err = strconv.ParseInt(endStr, 10, 64)
	if err != nil || end < start {
		return 0, 0, false
	}
	if end >= fileSize {
		end = fileSize - 1
	}

	return start, end, true
}
