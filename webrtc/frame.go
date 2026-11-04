// Package webrtc 在 PeerJS 信令之上建立 DataChannel，并在通道里跑一套
// 带类型的多路复用帧协议，用来传文件本体、metadata、控制信号与校验。
//
// 分工：
//   - peerjs 只负责信令（wss 上的 OFFER / ANSWER / CANDIDATE）
//   - 本包负责 PeerConnection、DataChannel 与数据帧编解码
//
// 浏览器侧用标准 PeerJS 发起 connect()，Server 侧在收到 OFFER 时建
// PeerConnection 并应答，之后双方在 DataChannel 上收发帧。
package webrtc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// 帧格式（大端）：
//
//	+--------+--------+------------------+
//	| type   | stream |      payload     |
//	| 1 byte | 4 byte |   变长（可空）    |
//	+--------+--------+------------------+
//
// 一个逻辑消息可能被拆成多个分片帧，靠 flag 位与 seq 重组；分片帧的 payload
// 结构为：flag(1) + seq(4) + total(4) + data。非分片帧直接是 data。
const (
	// headerSize 是 type + stream 的长度。
	headerSize = 5
	// MaxFramePayload 是单帧 payload 上限。SCTP 的默认 max message size 通常
	// 远大于此，这里取 64 KiB 与常见 MTU 留足余量，同时保证单帧足够小以便
	// 在网络上被平滑调度。
	MaxFramePayload = 64 * 1024
	// chunkOverhead 是分片帧附加的 flag+seq+total 字节数。
	chunkOverhead = 9
)

// Kind 是帧的类型。所有类型的 payload 都是二进制安全的。
type Kind uint8

const (
	// KindHello 是连接建立后双方互发的握手，携带角色与协议版本。
	KindHello Kind = 1
	// KindControl 是控制信号（取消、暂停、结束等），payload 是 UTF-8 JSON。
	KindControl Kind = 2
	// KindMetadata 是文件或目录的 metadata，payload 是 UTF-8 JSON。
	KindMetadata Kind = 3
	// KindData 是文件本体或任意二进制块。
	KindData Kind = 4
	// KindCheck 是校验（如整文件 sha256），payload 是 UTF-8 JSON。
	KindCheck Kind = 5
	// KindAck 是对某个消息的确认，payload 是 UTF-8 JSON。
	KindAck Kind = 6
	// KindError 是对端报告的错误。
	KindError Kind = 7
)

func (k Kind) String() string {
	switch k {
	case KindHello:
		return "hello"
	case KindControl:
		return "control"
	case KindMetadata:
		return "metadata"
	case KindData:
		return "data"
	case KindCheck:
		return "check"
	case KindAck:
		return "ack"
	case KindError:
		return "error"
	}
	return fmt.Sprintf("kind(%d)", uint8(k))
}

// IsText 表示该类型的 payload 是 UTF-8 JSON，便于上层直接解码。
func (k Kind) IsText() bool {
	switch k {
	case KindControl, KindMetadata, KindCheck, KindAck, KindError, KindHello:
		return true
	}
	return false
}

var (
	// ErrFrameTooLarge 表示单帧超出 MaxFramePayload。
	ErrFrameTooLarge = errors.New("webrtc: 帧过大")
	// ErrShortFrame 表示帧头不完整。
	ErrShortFrame = errors.New("webrtc: 帧头不完整")
	// ErrChunkPending 表示分片尚未收齐。
	ErrChunkPending = errors.New("webrtc: 分片未收齐")
	// ErrChunkMismatch 表示分片的 total 与实际到达的不一致。
	ErrChunkMismatch = errors.New("webrtc: 分片数量不一致")
)

// Frame 是一条完整消息。
type Frame struct {
	Kind   Kind
	Stream uint32
	Data   []byte
}

// EncodeFrame 把一条消息切成一个或多个帧的 payload 序列。
// 返回的每个元素都是一个可直接 dc.Send 的完整帧（含 5 字节头）。
//
// 注意：PeerJS 的 json serializer 发送的是二进制帧，因此这里全程用
// dc.Send 传 []byte，不使用 text frame。
func EncodeFrame(f Frame) ([][]byte, error) {
	if len(f.Data) <= MaxFramePayload {
		out := make([]byte, headerSize+len(f.Data))
		out[0] = byte(f.Kind)
		binary.BigEndian.PutUint32(out[1:5], f.Stream)
		copy(out[headerSize:], f.Data)
		return [][]byte{out}, nil
	}

	chunk := MaxFramePayload - chunkOverhead
	total := (len(f.Data) + chunk - 1) / chunk
	if total > int(^uint32(0)) {
		return nil, ErrFrameTooLarge
	}
	out := make([][]byte, 0, total)
	for seq := 0; seq < total; seq++ {
		lo := seq * chunk
		hi := lo + chunk
		if hi > len(f.Data) {
			hi = len(f.Data)
		}
		buf := make([]byte, headerSize+chunkOverhead+(hi-lo))
		buf[0] = byte(f.Kind)
		binary.BigEndian.PutUint32(buf[1:5], f.Stream)
		buf[5] = 1 // flag: 分片
		binary.BigEndian.PutUint32(buf[6:10], uint32(seq))
		binary.BigEndian.PutUint32(buf[10:14], uint32(total))
		copy(buf[headerSize+chunkOverhead:], f.Data[lo:hi])
		out = append(out, buf)
	}
	return out, nil
}

// ChunkHeader 描述一个分片帧的元信息，供 Decoder 内部使用。
type ChunkHeader struct {
	Kind   Kind
	Stream uint32
	IsPart bool
	Seq    uint32
	Total  uint32
	Body   []byte
}

// ParseChunk 解析单个帧的头部与正文（不做重组）。
func ParseChunk(b []byte) (ChunkHeader, error) {
	if len(b) < headerSize {
		return ChunkHeader{}, ErrShortFrame
	}
	h := ChunkHeader{
		Kind:   Kind(b[0]),
		Stream: binary.BigEndian.Uint32(b[1:5]),
	}
	rest := b[headerSize:]
	if len(rest) == 0 || rest[0] != 1 {
		h.Body = rest
		return h, nil
	}
	if len(rest) < chunkOverhead {
		return ChunkHeader{}, ErrShortFrame
	}
	h.IsPart = true
	h.Seq = binary.BigEndian.Uint32(rest[1:5])
	h.Total = binary.BigEndian.Uint32(rest[5:9])
	h.Body = rest[chunkOverhead:]
	return h, nil
}

// Reassembler 把单个 stream 上的分片拼回完整消息。
// 非并发安全，每个连接一个实例即可（DataChannel 的 OnMessage 是串行的）。
type Reassembler struct {
	stream uint32
	kind   Kind
	total  uint32
	parts  [][]byte
	got    uint32
}

// Reset 清空当前缓冲。
func (r *Reassembler) Reset() {
	r.total = 0
	r.parts = r.parts[:0]
	r.got = 0
}

// Push 喂入一个分片；集齐时返回完整消息，否则返回 ErrChunkPending。
func (r *Reassembler) Push(h ChunkHeader) (Frame, error) {
	if !h.IsPart {
		return Frame{Kind: h.Kind, Stream: h.Stream, Data: h.Body}, nil
	}
	if h.Total == 0 {
		return Frame{}, ErrChunkMismatch
	}
	if r.total == 0 {
		r.stream = h.Stream
		r.kind = h.Kind
		r.total = h.Total
		r.parts = make([][]byte, h.Total)
		r.got = 0
	} else if r.total != h.Total || r.kind != h.Kind || r.stream != h.Stream {
		// 上一组分片没传完就来了新的，放弃旧的那组。
		r.Reset()
		r.stream = h.Stream
		r.kind = h.Kind
		r.total = h.Total
		r.parts = make([][]byte, h.Total)
	}
	if h.Seq >= h.Total {
		r.Reset()
		return Frame{}, ErrChunkMismatch
	}
	if r.parts[h.Seq] == nil {
		r.parts[h.Seq] = append([]byte(nil), h.Body...)
		r.got++
	}
	if r.got != r.total {
		return Frame{}, ErrChunkPending
	}

	size := 0
	for _, p := range r.parts {
		if p == nil {
			r.Reset()
			return Frame{}, ErrChunkPending
		}
		size += len(p)
	}
	data := make([]byte, 0, size)
	for _, p := range r.parts {
		data = append(data, p...)
	}
	f := Frame{Kind: r.kind, Stream: r.stream, Data: data}
	r.Reset()
	return f, nil
}

// WriteFrame 是一个便捷函数：编码后逐帧写出。
func WriteFrame(w io.Writer, f Frame) error {
	frames, err := EncodeFrame(f)
	if err != nil {
		return err
	}
	for _, b := range frames {
		if _, err := w.Write(b); err != nil {
			return err
		}
	}
	return nil
}
