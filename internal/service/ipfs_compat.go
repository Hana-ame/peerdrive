// IPFSCompatLayer 提供可选的 IPFS 兼容层。
// 启用后，已存储的文件会额外拷贝一份到 IPFS 块存储（按 CID 键值），
// 并在 libp2p host 上注册 Bitswap 协议处理器，使外部 IPFS 节点
// 能通过标准 IPFS 协议（Bitswap / HTTP Gateway）获取文件。
package service

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"peerdrive/internal/log"
	"peerdrive/pkg/hashutil"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
	"google.golang.org/protobuf/encoding/protowire"
)

const (
	// Bitswap 协议 ID
	BitswapProtocolV10 = "/ipfs/bitswap/1.0.0"
	BitswapProtocolV11 = "/ipfs/bitswap/1.1.0"
	BitswapProtocolV12 = "/ipfs/bitswap/1.2.0"

	// Bitswap protobuf 消息的字段编号
	bitswapFieldWantlist     = 1 // Message.wantlist
	bitswapFieldBlocks       = 2 // Message.blocks (legacy)
	bitswapFieldPayload      = 3 // Message.payload (1.2.0)
	bitswapFieldPendingBytes = 4 // Message.pendingBytes (1.2.0)

	// Wantlist.Entry 的字段编号
	wantlistFieldEntries = 1 // Wantlist.entries (repeated)
	wantlistFieldFull    = 2 // Wantlist.full

	// Entry 的字段编号
	entryFieldBlock    = 1 // Entry.block (CID bytes)
	entryFieldCancel   = 2 // Entry.cancel
	entryFieldWantType = 3 // Entry.wantType (0=Have, 1=Block)
	entryFieldSendDontHave = 4 // Entry.sendDontHave

	// Want type constants
	wantTypeHave  = 0
	wantTypeBlock = 1

	// Payload.Block 的字段编号
	payloadFieldPrefix = 1 // Block.prefix
	payloadFieldData   = 2 // Block.data
)

// blockDirLen 是块存储中子目录前缀的长度（取 CID 的前 N 个字符）。
const blockDirLen = 2

// IPFSCompatLayer 管理 IPFS 兼容层：块存储和 Bitswap 处理器。
type IPFSCompatLayer struct {
	enabled    bool
	storageDir string // SHA256 内容寻址存储的根目录
	blockstore string // IPFS 块存储根目录（storage/ipfs-blocks/）
	p2p        *P2PService
	mu         sync.RWMutex
	logPrefix  string
}

// NewIPFSCompatLayer 创建新的 IPFS 兼容层实例。
// storageDir 是 SHA256 内容寻址存储的根目录。
// blockstore 是 IPFS 块存储的根目录；若为空则默认 <storageDir>/ipfs-blocks。
func NewIPFSCompatLayer(storageDir, blockstore string, p2p *P2PService) *IPFSCompatLayer {
	if blockstore == "" {
		blockstore = filepath.Join(storageDir, "ipfs-blocks")
	}
	return &IPFSCompatLayer{
		storageDir: storageDir,
		blockstore: blockstore,
		p2p:        p2p,
		logPrefix:  "ipfs-compat",
	}
}

// Enabled 返回 IPFS 兼容层是否已启用。
func (l *IPFSCompatLayer) Enabled() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.enabled
}

// Enable 启用 IPFS 兼容层：创建块存储目录，将已有文件拷贝到块存储，
// 在 libp2p host 上注册 Bitswap 协议处理器。
func (l *IPFSCompatLayer) Enable() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.enabled {
		log.LogDebug("%s: already enabled", l.logPrefix)
		return nil
	}

	// 1. 创建块存储根目录
	if err := os.MkdirAll(l.blockstore, 0755); err != nil {
		return fmt.Errorf("%s: create blockstore dir: %w", l.logPrefix, err)
	}
	log.LogInfo("%s: blockstore dir ready at %s", l.logPrefix, l.blockstore)

	// 2. 如果 P2P host 可用，注册 Bitswap 处理器
	if l.p2p != nil && l.p2p.Host != nil {
		l.p2p.Host.SetStreamHandler(protocol.ID(BitswapProtocolV12), l.handleBitswap)
		l.p2p.Host.SetStreamHandler(protocol.ID(BitswapProtocolV11), l.handleBitswap)
		l.p2p.Host.SetStreamHandler(protocol.ID(BitswapProtocolV10), l.handleBitswap)
		log.LogInfo("%s: registered Bitswap handlers on libp2p host", l.logPrefix)
	} else {
		log.LogWarn("%s: no libp2p host available, Bitswap handler not registered", l.logPrefix)
	}

	l.enabled = true
	log.LogInfo("%s: enabled successfully", l.logPrefix)
	return nil
}

// Disable 禁用 IPFS 兼容层：取消注册 Bitswap 协议处理器，
// 但保留块存储目录中的已有文件。
func (l *IPFSCompatLayer) Disable() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.enabled {
		return
	}

	// 取消注册 Bitswap 处理器
	if l.p2p != nil && l.p2p.Host != nil {
		l.p2p.Host.RemoveStreamHandler(protocol.ID(BitswapProtocolV12))
		l.p2p.Host.RemoveStreamHandler(protocol.ID(BitswapProtocolV11))
		l.p2p.Host.RemoveStreamHandler(protocol.ID(BitswapProtocolV10))
		log.LogInfo("%s: removed Bitswap handlers", l.logPrefix)
	}

	l.enabled = false
	log.LogInfo("%s: disabled", l.logPrefix)
}

// AddFile 将指定 SHA256 哈希的文件拷贝到 IPFS 块存储。
// 文件从 SHA256 内容寻址路径 <storageDir>/<hash[:2]>/<hash> 读取，
// 写入到 <blockstore>/<cid[:2]>/<cid>.block。
func (l *IPFSCompatLayer) AddFile(sha256hash string) error {
	l.mu.RLock()
	enabled := l.enabled
	l.mu.RUnlock()

	if !enabled {
		return nil
	}

	// 校验 SHA256 哈希
	if len(sha256hash) != 64 {
		return fmt.Errorf("%s: invalid sha256 hash length %d", l.logPrefix, len(sha256hash))
	}

	// 计算 CID
	cidStr := hashutil.SHA256ToCID(sha256hash)
	if cidStr == "" {
		return fmt.Errorf("%s: failed to compute CID for hash %s", l.logPrefix, sha256hash)
	}

	// 如果块已存在，无需重复写入
	if l.blockExists(cidStr) {
		log.LogDebug("%s: block already exists for CID %s", l.logPrefix, cidStr)
		return nil
	}

	// 读取源文件（SHA256 内容寻址存储）
	srcPath := filepath.Join(l.storageDir, sha256hash[:2], sha256hash)
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("%s: read source file %s: %w", l.logPrefix, srcPath, err)
	}

	// 写入块存储
	if err := l.writeBlock(cidStr, data); err != nil {
		return fmt.Errorf("%s: write block %s: %w", l.logPrefix, cidStr, err)
	}

	log.LogInfo("%s: added file %s -> CID %s (%d bytes)", l.logPrefix, sha256hash, cidStr, len(data))
	return nil
}

// HasCID 检查指定 CID 的块是否存在于块存储中。
func (l *IPFSCompatLayer) HasCID(cidStr string) bool {
	return l.blockExists(cidStr)
}

// GetBlock 返回指定 CID 的原始块数据。
func (l *IPFSCompatLayer) GetBlock(cidStr string) ([]byte, error) {
	path := l.blockPath(cidStr)
	if path == "" {
		return nil, fmt.Errorf("%s: invalid CID: %s", l.logPrefix, cidStr)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: block not found: %s", l.logPrefix, cidStr)
	}
	return data, nil
}

// BlockCount 返回块存储中当前缓存的块数量。
func (l *IPFSCompatLayer) BlockCount() int {
	count := 0
	entries, err := os.ReadDir(l.blockstore)
	if err != nil {
		return 0
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		subDir := filepath.Join(l.blockstore, entry.Name())
		blocks, err := os.ReadDir(subDir)
		if err != nil {
			continue
		}
		count += len(blocks)
	}
	return count
}

// BlockstorePath 返回块存储的根目录路径。
func (l *IPFSCompatLayer) BlockstorePath() string {
	return l.blockstore
}

// ─── internal helpers ───────────────────────────────────────────

// blockPath 返回指定 CID 在块存储中的完整路径。
// 格式：<blockstore>/<cid[:2]>/<cid>.block
func (l *IPFSCompatLayer) blockPath(cidStr string) string {
	if len(cidStr) < blockDirLen {
		return ""
	}
	return filepath.Join(l.blockstore, cidStr[:blockDirLen], cidStr+".block")
}

// blockExists 检查指定 CID 的块文件是否存在。
func (l *IPFSCompatLayer) blockExists(cidStr string) bool {
	path := l.blockPath(cidStr)
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// writeBlock 将原始文件数据以 CID 为键写入块存储。
func (l *IPFSCompatLayer) writeBlock(cidStr string, data []byte) error {
	if len(cidStr) < blockDirLen {
		return fmt.Errorf("cid too short: %s", cidStr)
	}
	blockDir := filepath.Join(l.blockstore, cidStr[:blockDirLen])
	if err := os.MkdirAll(blockDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(blockDir, cidStr+".block"), data, 0644)
}

// ─── Bitswap protocol handler ───────────────────────────────────

// handleBitswap 处理 Bitswap 协议流。
// 解析传入的 protobuf 消息，提取 Wantlist 中的 CID，
// 在块存储中查找对应块并返回。
func (l *IPFSCompatLayer) handleBitswap(stream network.Stream) {
	defer stream.Close()

	peerID := stream.Conn().RemotePeer().String()
	log.LogDebug("%s: bitswap request from %s", l.logPrefix, peerID)

	// 读取 varint 前缀的 protobuf 消息
	data, err := readVarintPrefixed(stream)
	if err != nil {
		log.LogWarn("%s: bitswap read error from %s: %v", l.logPrefix, peerID, err)
		return
	}

	if len(data) == 0 {
		return
	}

	// 解析 Wantlist，提取所有请求的条目
	entries := parseBitswapWantlist(data)
	if len(entries) == 0 {
		log.LogDebug("%s: no wanted CIDs in request from %s", l.logPrefix, peerID)
		return
	}

	// 统计非 cancel 的条目数
	activeCount := 0
	for _, e := range entries {
		if !e.Cancel {
			activeCount++
		}
	}
	log.LogDebug("%s: peer %s wants %d entries (%d active)", l.logPrefix, peerID, len(entries), activeCount)

	if activeCount == 0 {
		log.LogDebug("%s: all entries cancelled from %s", l.logPrefix, peerID)
		return
	}

	// 构建响应 payload
	response := l.buildPayloadResponse(entries)
	if len(response) == 0 {
		log.LogDebug("%s: no blocks to send to %s", l.logPrefix, peerID)
		return
	}

	// 写入响应
	if err := writeVarintPrefixed(stream, response); err != nil {
		log.LogWarn("%s: bitswap write error to %s: %v", l.logPrefix, peerID, err)
		return
	}

	log.LogInfo("%s: sent response to %s (%d bytes)", l.logPrefix, peerID, len(response))
}

// bitswapEntry represents a parsed Bitswap wantlist entry.
type bitswapEntry struct {
	CID          cid.Cid
	Cancel       bool
	WantType     int32 // 0=Have, 1=Block
	SendDontHave bool
}

// parseBitswapWantlist 从 Bitswap protobuf 消息中提取所有请求的条目。
func parseBitswapWantlist(data []byte) []bitswapEntry {
	var entries []bitswapEntry
	for len(data) > 0 {
		num, wtype, n := protowire.ConsumeTag(data)
		if n < 0 {
			break
		}
		data = data[n:]

		if num == bitswapFieldWantlist && wtype == protowire.BytesType {
			// Field 1 = Wantlist (length-delimited sub-message)
			wantlistData, n := protowire.ConsumeBytes(data)
			if n < 0 {
				break
			}
			data = data[n:]

			// 解析 Wantlist 中的 entries
			parsed := parseWantlistEntries(wantlistData)
			entries = append(entries, parsed...)
		} else {
			// 跳过其他字段
			n = skipField(wtype, data)
			if n < 0 {
				break
			}
			data = data[n:]
		}
	}
	return entries
}

// parseWantlistEntries 从 Wantlist 子消息中解析 entries 列表。
func parseWantlistEntries(data []byte) []bitswapEntry {
	var entries []bitswapEntry
	for len(data) > 0 {
		num, wtype, n := protowire.ConsumeTag(data)
		if n < 0 {
			break
		}
		data = data[n:]

		if num == wantlistFieldEntries && wtype == protowire.BytesType {
			// Field 1 = entries (repeated length-delimited sub-messages)
			entryData, n := protowire.ConsumeBytes(data)
			if n < 0 {
				break
			}
			data = data[n:]

			// 解析 Entry 中的所有字段
			if entry := parseEntry(entryData); entry != nil {
				entries = append(entries, *entry)
			}
		} else {
			n = skipField(wtype, data)
			if n < 0 {
				break
			}
			data = data[n:]
		}
	}
	return entries
}

// parseEntry 从 Entry 子消息中提取所有字段。
func parseEntry(data []byte) *bitswapEntry {
	var entry bitswapEntry
	var cidBytes []byte
	for len(data) > 0 {
		num, wtype, n := protowire.ConsumeTag(data)
		if n < 0 {
			break
		}
		data = data[n:]

		switch {
		case num == entryFieldBlock && wtype == protowire.BytesType:
			// Field 1 = block (CID bytes)
			val, n := protowire.ConsumeBytes(data)
			if n >= 0 {
				cidBytes = val
			}
		case num == entryFieldCancel && wtype == protowire.VarintType:
			// Field 2 = cancel (bool)
			val, n := protowire.ConsumeVarint(data)
			if n >= 0 {
				entry.Cancel = val != 0
			}
		case num == entryFieldWantType && wtype == protowire.VarintType:
			// Field 3 = wantType (int32)
			val, n := protowire.ConsumeVarint(data)
			if n >= 0 {
				entry.WantType = int32(val)
			}
		case num == entryFieldSendDontHave && wtype == protowire.VarintType:
			// Field 4 = sendDontHave (bool)
			val, n := protowire.ConsumeVarint(data)
			if n >= 0 {
				entry.SendDontHave = val != 0
			}
		default:
			n = skipField(wtype, data)
			if n < 0 {
				break
			}
		}

		if n < 0 {
			break
		}
		data = data[n:]
	}

	if cidBytes == nil {
		return nil
	}
	_, c, err := cid.CidFromBytes(cidBytes)
	if err != nil {
		return nil
	}
	entry.CID = c
	return &entry
}

// buildPayloadResponse 根据请求的 entries 列表构建 Bitswap 响应 payload。
// 跳过 cancelled 条目。如果是 WANT_HAVE，只发送 prefix 表示 HAVE 确认；
// 如果是 WANT_BLOCK，发送完整的 prefix + data。
// 对于 HAVE 请求但块不存在，如果 SendDontHave 为 true，发送 empty 块表示 DONT_HAVE。
func (l *IPFSCompatLayer) buildPayloadResponse(entries []bitswapEntry) []byte {
	var msg []byte

	for _, e := range entries {
		if e.Cancel {
			continue
		}

		cidStr := e.CID.String()
		haveBlock := l.blockExists(cidStr)

		if !haveBlock {
			if !e.SendDontHave {
				continue
			}
			// Send DONT_HAVE: block with prefix only (no data)
		}

		// 构建 Block 子消息（payload 中的每个元素）
		var block []byte

		// Field 1: prefix (CID prefix bytes)
		prefix := e.CID.Prefix()
		prefixBytes := prefix.Bytes()
		block = protowire.AppendTag(block, payloadFieldPrefix, protowire.BytesType)
		block = protowire.AppendBytes(block, prefixBytes)

		// Field 2: data (raw block bytes) — only for WANT_BLOCK when we have the data
		if haveBlock && e.WantType == wantTypeBlock {
			data, err := os.ReadFile(l.blockPath(cidStr))
			if err == nil {
				block = protowire.AppendTag(block, payloadFieldData, protowire.BytesType)
				block = protowire.AppendBytes(block, data)
			}
		}

		// 追加到 payload 字段 (field 3)
		msg = protowire.AppendTag(msg, bitswapFieldPayload, protowire.BytesType)
		msg = protowire.AppendBytes(msg, block)
	}

	return msg
}

// ─── protobuf wire format helpers ───────────────────────────────

// skipField 跳过当前 protobuf 字段的剩余字节，返回跳过的字节数。
func skipField(wtype protowire.Type, data []byte) int {
	switch wtype {
	case protowire.VarintType:
		_, n := protowire.ConsumeVarint(data)
		return n
	case protowire.Fixed32Type:
		_, n := protowire.ConsumeFixed32(data)
		return n
	case protowire.Fixed64Type:
		_, n := protowire.ConsumeFixed64(data)
		return n
	case protowire.BytesType:
		_, n := protowire.ConsumeBytes(data)
		return n
	default:
		return 0
	}
}

// byteReader 适配 io.Reader 为 io.ByteReader（用于 binary.ReadUvarint）。
type byteReader struct {
	reader io.Reader
}

func (r byteReader) ReadByte() (byte, error) {
	var b [1]byte
	_, err := io.ReadFull(r.reader, b[:])
	return b[0], err
}

// readVarintPrefixed 从 reader 中读取 varint 前缀的消息。
// 格式：[varint-length][message-bytes]
func readVarintPrefixed(r io.Reader) ([]byte, error) {
	br := byteReader{reader: r}
	length, err := binary.ReadUvarint(br)
	if err != nil {
		return nil, fmt.Errorf("read varint length: %w", err)
	}

	if length == 0 {
		return nil, nil
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("read message body: %w", err)
	}

	return data, nil
}

// writeVarintPrefixed 将数据以 varint 前缀格式写入 writer。
// 格式：[varint-length][message-bytes]
func writeVarintPrefixed(w io.Writer, data []byte) error {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], uint64(len(data)))

	if _, err := w.Write(buf[:n]); err != nil {
		return fmt.Errorf("write varint length: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write message body: %w", err)
	}
	return nil
}
