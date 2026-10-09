package egress

// egress.go: 统一消费出口核心模型与抽象契约。
// 架构定位：解耦数据源提供端（Ingress / source.Source）与消费下发端（Egress / HTTP, WS, PeerJS）。
// 详见规范文档 doc/UNIFIED-EGRESS-ABSTRACTION.md。

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"sync"
)

// 通用出口管线错误常量。
var (
	ErrInvalidHash          = errors.New("egress: invalid 64-hex sha256 hash")
	ErrLoopDetected         = errors.New("egress: request trace loop detected")
	ErrNotFound             = errors.New("egress: content not found")
	ErrRangeNotSatisfiable  = errors.New("egress: requested range not satisfiable")
	ErrHeaderAlreadySent    = errors.New("egress: headers already sent")
	ErrPrematureTermination = errors.New("egress: transmission aborted prematurely")
)

// DefaultChunkSize 统一流式传输块大小（64KB 兼顾 SCTP 吞吐与内存流控粒度）。
const DefaultChunkSize = 64 * 1024

// chunkPool 64KB 块缓冲池：并发下发复用，避免为每个块重复分配内存导致 GC 激增。
var chunkPool = sync.Pool{
	New: func() any {
		b := make([]byte, DefaultChunkSize)
		return &b
	},
}

// GetChunkBuffer 从池中借取 64KB 临时缓冲区。
func GetChunkBuffer() []byte {
	return *chunkPool.Get().(*[]byte)
}

// PutChunkBuffer 归还缓冲区到池中。
func PutChunkBuffer(b []byte) {
	if cap(b) >= DefaultChunkSize {
		b = b[:DefaultChunkSize]
		chunkPool.Put(&b)
	}
}

// ContentProvider 数据源提供端接口（与 source.Manager 和 transport.FileRouter 契约完全对齐）。
// 统一出口管线仅依赖此精简接口，解耦具体数据源实现与传输协议。
type ContentProvider interface {
	// OpenRange 流式打开指定 hash 的分片内容（offset < 0 归一为 0；size < 0 读取至 EOF）。
	OpenRange(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error)
	// InfoSize 查询指定 hash 的总长度（如果支持）。不支持或未知时返回 -1 或错误。
	InfoSize(ctx context.Context, hash string) (int64, error)
}

// EgressRequest 统一出流请求模型。
type EgressRequest struct {
	Hash     string          // 必填：64 位小写 hex SHA-256 哈希
	Offset   int64           // 请求起始偏移量（默认 0）
	Size     int64           // 请求长度（-1 表示读取至 EOF）
	Trace    []string        // 防环链路跟踪节点 ID 列表
	ReqID    string          // 请求标识符（用于异步协议帧关联与日志追踪）
	ClientID string          // 消费对端标识（PeerID / SessionID / RemoteAddr）
	Filename string          // 可选文件名（用于推导 MIME 类型与 Content-Disposition）
	MimeType string          // 可选明确指定的 MIME 类型
	IsRange  bool            // 标记该请求是否由显式 Range 头发起（影响 206 状态码决策）
	IsLocal  bool            // 是否为本地管理会话
	Context  context.Context // 请求上下文生命周期控制
}

// EgressMeta 出流内容元数据。
type EgressMeta struct {
	Hash        string // 64 位小写 hex SHA-256
	TotalSize   int64  // 内容总大小（-1 表示未知长度流）
	Offset      int64  // 实际下发起始偏移量
	ContentSize int64  // 本次响应下发字节数
	MimeType    string // MIME 类型（如 video/mp4, application/octet-stream）
	ETag        string // 缓存指纹（即 hash 或带引号指纹）
	ReqID       string // 对应的请求 ID
	IsRange     bool   // 标记本次下发是否为 Range 分片响应（影响 206 状态码决策）
}

// EgressSink 协议输出适配器接口（消费端下发接收器）。
type EgressSink interface {
	// WriteMeta 下发元数据头（HTTP 状态码与响应头 / WS admin-bin 声明头 / PeerJS meta 帧）。
	WriteMeta(meta EgressMeta) error

	// WriteChunk 下发单块数据载荷（默认最大 64KB）。
	WriteChunk(offset int64, chunk []byte) error

	// WriteDone 发送正常传输完成信号。
	WriteDone() error

	// WriteError 发送异常终止信号。
	WriteError(code string, message string, httpStatus int) error

	// WaitForBackpressure 当底层写缓冲超过阈值时挂起等待，避免生产者压垮慢消费者。
	WaitForBackpressure(ctx context.Context) error
}

// GuessMimeType 根据文件名、显式 MIME 以及样本魔数推导 MIME 类型。
func GuessMimeType(explicitMime, filename string, sample []byte) string {
	if explicitMime != "" && explicitMime != "application/octet-stream" {
		return explicitMime
	}
	if filename != "" {
		if ext := filepath.Ext(filename); ext != "" {
			if t := mime.TypeByExtension(ext); t != "" {
				return t
			}
		}
	}
	if len(sample) > 0 {
		sniffLen := len(sample)
		if sniffLen > 512 {
			sniffLen = 512
		}
		ct := http.DetectContentType(sample[:sniffLen])
		if ct != "application/octet-stream" {
			return ct
		}
	}
	if explicitMime != "" {
		return explicitMime
	}
	return "application/octet-stream"
}
