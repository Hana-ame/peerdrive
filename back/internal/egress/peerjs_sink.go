package egress

// peerjs_sink.go: PeerJS / WebRTC DataChannel 消费端输出适配器。
// 支持 meta / data / done / err 协议帧流与底层 SCTP 缓冲背压调度。

import (
	"context"
)

// PeerJSSender 抽象 PeerJS / DataChannel 发送能力。
type PeerJSSender interface {
	SendJSON(v any) error
	SendFrame(header any, body []byte) error
}

// PeerJSEgressSink 将出流转换为 PeerJS WebRTC DataChannel 帧协议。
type PeerJSEgressSink struct {
	sender       PeerJSSender
	hash         string
	reqID        string
	initialOff   int64
	sentBytes    int64
	backpressure func(ctx context.Context) error
}

// NewPeerJSEgressSink 创建 PeerJS DataChannel 输出适配器。
func NewPeerJSEgressSink(sender PeerJSSender) *PeerJSEgressSink {
	return &PeerJSEgressSink{
		sender: sender,
	}
}

// SetBackpressureHook 设置背压流控等待回调（如监听 DataChannel.BufferedAmount）。
func (s *PeerJSEgressSink) SetBackpressureHook(hook func(ctx context.Context) error) {
	s.backpressure = hook
}

// WriteMeta 发送 meta 协议帧。
func (s *PeerJSEgressSink) WriteMeta(meta EgressMeta) error {
	s.hash = meta.Hash
	s.reqID = meta.ReqID
	s.initialOff = meta.Offset
	s.sentBytes = 0

	frame := map[string]any{
		"type":  "meta",
		"hash":  meta.Hash,
		"total": meta.TotalSize,
		"reqId": meta.ReqID,
	}
	return s.sender.SendJSON(frame)
}

// WriteChunk 发送 data 帧声明头与二进制数据块。
func (s *PeerJSEgressSink) WriteChunk(offset int64, chunk []byte) error {
	header := map[string]any{
		"type":   "data",
		"hash":   s.hash,
		"offset": offset,
		"size":   int64(len(chunk)),
		"reqId":  s.reqID,
	}
	s.sentBytes += int64(len(chunk))
	return s.sender.SendFrame(header, chunk)
}

// WriteDone 发送 done 结束协议帧。
func (s *PeerJSEgressSink) WriteDone() error {
	frame := map[string]any{
		"type":   "done",
		"hash":   s.hash,
		"offset": s.initialOff,
		"size":   s.sentBytes,
		"reqId":  s.reqID,
	}
	return s.sender.SendJSON(frame)
}

// WriteError 发送 err 错误协议帧。
func (s *PeerJSEgressSink) WriteError(code string, message string, httpStatus int) error {
	frame := map[string]any{
		"type":  "err",
		"hash":  s.hash,
		"msg":   message,
		"code":  code,
		"reqId": s.reqID,
	}
	return s.sender.SendJSON(frame)
}

// WaitForBackpressure 等待底层 DataChannel 写缓冲降低至阈值。
func (s *PeerJSEgressSink) WaitForBackpressure(ctx context.Context) error {
	if s.backpressure != nil {
		return s.backpressure(ctx)
	}
	return ctx.Err()
}
