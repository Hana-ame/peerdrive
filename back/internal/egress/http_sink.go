package egress

// http_sink.go: HTTP / Gin 消费端输出适配器。
// 支持 RFC 7233 206 Partial Content、ETag 强缓存与 http.Flusher 流式下发。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// HTTPEgressSink 将出流转换为标准 HTTP 响应。
type HTTPEgressSink struct {
	w          http.ResponseWriter
	flusher    http.Flusher
	headerSent bool
	statusCode int
}

// NewHTTPEgressSink 创建 HTTP 输出适配器。
func NewHTTPEgressSink(w http.ResponseWriter) *HTTPEgressSink {
	var flusher http.Flusher
	if f, ok := w.(http.Flusher); ok {
		flusher = f
	}
	return &HTTPEgressSink{
		w:       w,
		flusher: flusher,
	}
}

// WriteMeta 发送 HTTP 状态码与响应头。
func (s *HTTPEgressSink) WriteMeta(meta EgressMeta) error {
	if s.headerSent {
		return ErrHeaderAlreadySent
	}

	h := s.w.Header()
	h.Set("Accept-Ranges", "bytes")
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	if meta.ETag != "" {
		h.Set("ETag", meta.ETag)
	}
	if meta.MimeType != "" {
		h.Set("Content-Type", meta.MimeType)
	} else {
		h.Set("Content-Type", "application/octet-stream")
	}

	// 判断是否为分片请求（206 Partial Content）：
	// 偏移大于 0，或者请求尺寸小于总大小
	isPartial := meta.Offset > 0 || (meta.TotalSize >= 0 && meta.ContentSize < meta.TotalSize)
	if isPartial && meta.TotalSize >= 0 && meta.ContentSize > 0 {
		s.statusCode = http.StatusPartialContent
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", meta.Offset, meta.Offset+meta.ContentSize-1, meta.TotalSize))
	} else {
		s.statusCode = http.StatusOK
	}

	if meta.ContentSize >= 0 {
		h.Set("Content-Length", strconv.FormatInt(meta.ContentSize, 10))
	}

	s.w.WriteHeader(s.statusCode)
	s.headerSent = true
	return nil
}

// WriteChunk 发送数据块载荷。
func (s *HTTPEgressSink) WriteChunk(offset int64, chunk []byte) error {
	if !s.headerSent {
		return ErrPrematureTermination
	}
	_, err := s.w.Write(chunk)
	if err == nil && s.flusher != nil {
		s.flusher.Flush()
	}
	return err
}

// WriteDone 完成响应。
func (s *HTTPEgressSink) WriteDone() error {
	if s.flusher != nil {
		s.flusher.Flush()
	}
	return nil
}

// WriteError 发送 HTTP 错误响应。
func (s *HTTPEgressSink) WriteError(code string, message string, httpStatus int) error {
	if s.headerSent {
		// Header 已发送，无法更改 HTTP 状态码，直接中断
		return ErrPrematureTermination
	}
	s.w.Header().Set("Content-Type", "application/json")
	if httpStatus <= 0 {
		httpStatus = http.StatusInternalServerError
	}
	s.w.WriteHeader(httpStatus)
	s.headerSent = true
	_ = json.NewEncoder(s.w).Encode(map[string]any{
		"error": message,
		"code":  code,
	})
	return nil
}

// WaitForBackpressure HTTP 基于内核 TCP 套接字自动处理背压，仅检查上下文。
func (s *HTTPEgressSink) WaitForBackpressure(ctx context.Context) error {
	return ctx.Err()
}
