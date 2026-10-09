package egress

// pipeline.go: 统一出流调度管线。
// 调度 ContentProvider 提供的数据流，处理 Range 钳制、防环阻断、64KB 块池复用与背压协调。

import (
	"context"
	"errors"
	"fmt"
	"io"

	"peerdrive/internal/log"
	hashutil "peerdrive/pkg/hashutil"
)

// Pipeline 统一出口调度管线。
type Pipeline struct {
	provider ContentProvider
	nodeID   string // 当前节点 ID（用于防环 trace 校验）
}

// NewPipeline 创建统一出口调度管线实例。
func NewPipeline(provider ContentProvider, nodeID string) *Pipeline {
	return &Pipeline{
		provider: provider,
		nodeID:   nodeID,
	}
}

// Serve 将内容根据统一请求调度泵出到指定的 EgressSink。
func (p *Pipeline) Serve(req EgressRequest, sink EgressSink) error {
	ctx := req.Context
	if ctx == nil {
		ctx = context.Background()
	}

	// 0. 上下文预检：若调用前已被取消，直接退出防无意义 IO
	if err := ctx.Err(); err != nil {
		_ = sink.WriteError("CANCELED", err.Error(), 499)
		return err
	}

	// 1. 哈希校验：严格 64 位小写 hex，防恶意构造输入
	if !hashutil.IsStrictSHA256(req.Hash) {
		_ = sink.WriteError("INVALID_HASH", "invalid hash format", 400)
		return ErrInvalidHash
	}

	// 2. 防环检查：若 Trace 链路已包含本节点 ID，说明出现回源环路，立刻终止
	if p.nodeID != "" {
		for _, id := range req.Trace {
			if id == p.nodeID {
				_ = sink.WriteError("LOOP_DETECTED", "loop detected in fetch trace", 409)
				return ErrLoopDetected
			}
		}
	}

	if p.provider == nil {
		_ = sink.WriteError("UNAVAILABLE", "no content provider configured", 503)
		return errors.New("egress: nil content provider")
	}

	// 3. 查询总大小以校验与钳制请求 Range
	total := int64(-1)
	if size, err := p.provider.InfoSize(ctx, req.Hash); err == nil && size >= 0 {
		total = size
	}

	// 4. 计算实际起始偏移与请求长度（边界保护）
	actualOffset := req.Offset
	if actualOffset < 0 {
		actualOffset = 0
	}
	if total >= 0 && actualOffset > total {
		actualOffset = total
	}

	actualSize := req.Size
	// 未指定或传 <=0 时，默认语义为读取至文件末尾（EOF）
	if actualSize <= 0 {
		actualSize = -1
	}
	if total >= 0 {
		if actualSize < 0 || actualOffset+actualSize > total {
			actualSize = total - actualOffset
		}
	}

	// 针对超出非空文件尾的 Range 请求直接返回 416
	if total > 0 && actualOffset >= total {
		_ = sink.WriteError("RANGE_NOT_SATISFIABLE", "requested range not satisfiable", 416)
		return ErrRangeNotSatisfiable
	}

	// 5. 转发下游上下文：将本节点 ID 追加进链路跟踪
	fwdTrace := append(append([]string{}, req.Trace...), p.nodeID)
	reqCtx := context.WithValue(ctx, "trace", fwdTrace)

	stream, err := p.provider.OpenRange(reqCtx, req.Hash, actualOffset, actualSize)
	if err != nil {
		_ = sink.WriteError("NOT_FOUND", fmt.Sprintf("content not found: %v", err), 404)
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	defer stream.Close()

	// 6. 构造元数据并发送 Meta
	meta := EgressMeta{
		Hash:        req.Hash,
		TotalSize:   total,
		Offset:      actualOffset,
		ContentSize: actualSize,
		MimeType:    GuessMimeType(req.MimeType, req.Filename, nil),
		ETag:        fmt.Sprintf("\"%s\"", req.Hash),
		ReqID:       req.ReqID,
	}

	if err := sink.WriteMeta(meta); err != nil {
		return err
	}

	// 若内容长度确定为 0，直接结束（如空文件）
	if actualSize == 0 {
		return sink.WriteDone()
	}

	// 7. 分块循环泵送（基于 64KB 块池）
	buf := GetChunkBuffer()
	defer PutChunkBuffer(buf)

	sent := int64(0)
	for {
		select {
		case <-ctx.Done():
			_ = sink.WriteError("CANCELED", ctx.Err().Error(), 499)
			return ctx.Err()
		default:
		}

		// 协调底层背压（SCTP/WS 慢写时避免发送方积压撑爆内存）
		if err := sink.WaitForBackpressure(ctx); err != nil {
			return err
		}

		toRead := int64(len(buf))
		if actualSize >= 0 {
			remain := actualSize - sent
			if remain <= 0 {
				break
			}
			if remain < toRead {
				toRead = remain
			}
		}

		n, readErr := stream.Read(buf[:toRead])
		if n > 0 {
			if writeErr := sink.WriteChunk(actualOffset+sent, buf[:n]); writeErr != nil {
				return writeErr
			}
			sent += int64(n)
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			log.LogWarn("egress: read failed for %s at offset %d: %v", req.Hash, actualOffset+sent, readErr)
			_ = sink.WriteError("READ_FAILED", readErr.Error(), 500)
			return readErr
		}
	}

	return sink.WriteDone()
}
