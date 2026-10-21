package egress

// ws_sink.go: WebSocket 消费端输出适配器。
// 支持 admin-bin 声明帧头与二进制块分发，适配本地前端管理面长连接。

import (
	"context"
)

// WSSender 抽象 WebSocket 连接发送能力。
type WSSender interface {
	SendJSON(v any) error
	SendBinary(data []byte) error
}

// FrameSender 抽象带帧头的两段式帧发送能力（与 transport.Session 对齐）。
type FrameSender interface {
	SendJSON(v any) error
	SendFrame(header any, body []byte) error
}

type frameSenderAdapter struct {
	fs FrameSender
}

func (a *frameSenderAdapter) SendJSON(v any) error {
	return a.fs.SendJSON(v)
}

func (a *frameSenderAdapter) SendBinary(data []byte) error {
	return a.fs.SendFrame(nil, data)
}

// WebSocketEgressSink 将出流转换为 WebSocket 帧协议。
type WebSocketEgressSink struct {
	sender       WSSender
	reqID        string
	backpressure func(ctx context.Context) error
}

// NewWebSocketEgressSink 创建 WebSocket 输出适配器。
func NewWebSocketEgressSink(sender WSSender) *WebSocketEgressSink {
	return &WebSocketEgressSink{
		sender: sender,
	}
}

// NewWebSocketEgressSinkFromFrameSender 从底层 FrameSender 构造 WebSocket 输出适配器。
func NewWebSocketEgressSinkFromFrameSender(fs FrameSender) *WebSocketEgressSink {
	return NewWebSocketEgressSink(&frameSenderAdapter{fs: fs})
}

// SetBackpressureHook 设置背压流控等待回调。
func (s *WebSocketEgressSink) SetBackpressureHook(hook func(ctx context.Context) error) {
	s.backpressure = hook
}

// WriteMeta 发送 admin-bin 声明头帧。
func (s *WebSocketEgressSink) WriteMeta(meta EgressMeta) error {
	s.reqID = meta.ReqID
	header := map[string]any{
		"type":   "admin-bin",
		"hash":   meta.Hash,
		"total":  meta.TotalSize,
		"offset": meta.Offset,
		"size":   meta.ContentSize,
		"mime":   meta.MimeType,
		"reqId":  meta.ReqID,
	}
	return s.sender.SendJSON(header)
}

// WriteChunk 发送单个二进制数据分块。
func (s *WebSocketEgressSink) WriteChunk(offset int64, chunk []byte) error {
	return s.sender.SendBinary(chunk)
}

// WriteDone 传输完成。
func (s *WebSocketEgressSink) WriteDone() error {
	return nil
}

// WriteError 发送错误控制帧。
func (s *WebSocketEgressSink) WriteError(code string, message string, httpStatus int) error {
	errFrame := map[string]any{
		"type":   "err",
		"code":   code,
		"msg":    message,
		"status": httpStatus,
		"reqId":  s.reqID,
	}
	return s.sender.SendJSON(errFrame)
}

// WaitForBackpressure 等待底层 WebSocket 发送缓冲释放。
func (s *WebSocketEgressSink) WaitForBackpressure(ctx context.Context) error {
	if s.backpressure != nil {
		return s.backpressure(ctx)
	}
	return ctx.Err()
}
