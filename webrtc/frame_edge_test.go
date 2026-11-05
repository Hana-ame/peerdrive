package webrtc

import (
	"bytes"
	"errors"
	"testing"
)

// 这些用例补的是 frame_test.go 里空的那些格子：正好压在上限的边界、
// 半路换 stream、重复分片、以及各种畸形输入。分片重组这块出错的
// 表现往往不是报错而是「数据悄悄错了」，所以字节级比对必须做。

func TestEncodeAtExactPayloadLimit(t *testing.T) {
	// 正好等于上限时必须走单帧：如果这里判成多帧，说明比较符写成了
	// < 而不是 <=，64 KiB 的块会被无谓地切碎。
	data := bytes.Repeat([]byte{0xAB}, MaxFramePayload)
	out, err := EncodeFrame(Frame{Kind: KindData, Stream: 1, Data: data})
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("正好 %d 字节应产生 1 帧，实得 %d", MaxFramePayload, len(out))
	}
	if len(out[0]) != headerSize+MaxFramePayload {
		t.Errorf("帧长 = %d，want %d", len(out[0]), headerSize+MaxFramePayload)
	}
	// 单帧不带分片头：正文直接从第 5 字节开始，内容就是原始数据。
	if !bytes.Equal(out[0][headerSize:], data) {
		t.Error("单帧正文与原始数据不一致")
	}
}

func TestEncodeOneByteOverLimit(t *testing.T) {
	data := bytes.Repeat([]byte{0xCD}, MaxFramePayload+1)
	out, err := EncodeFrame(Frame{Kind: KindData, Stream: 2, Data: data})
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("超 1 字节应切成 2 帧，实得 %d", len(out))
	}
	// 每帧都不能超过上限，否则 SCTP 层会分片，我们就失控了。
	for i, b := range out {
		if len(b) > MaxFramePayload {
			t.Errorf("第 %d 帧 %d 字节，超过上限 %d", i, len(b), MaxFramePayload)
		}
	}
}

func TestChunkFrameSizeWithinLimit(t *testing.T) {
	// 分片帧含 9 字节额外头，切块时必须把这部分算进去，
	// 否则 64 KiB 的上限会被突破。
	data := bytes.Repeat([]byte{0x11}, MaxFramePayload*3)
	out, err := EncodeFrame(Frame{Kind: KindData, Stream: 3, Data: data})
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	for i, b := range out {
		if len(b) > MaxFramePayload {
			t.Errorf("第 %d 片 %d 字节，超过 %d", i, len(b), MaxFramePayload)
		}
	}
}

func TestChunkSequenceAndTotalConsistent(t *testing.T) {
	data := bytes.Repeat([]byte{0x5A}, MaxFramePayload*2+100)
	out, _ := EncodeFrame(Frame{Kind: KindMetadata, Stream: 88, Data: data})

	for i, b := range out {
		h, err := ParseChunk(b)
		if err != nil {
			t.Fatalf("ParseChunk(%d): %v", i, err)
		}
		if !h.IsPart {
			t.Fatalf("第 %d 片没被识别为分片", i)
		}
		if h.Seq != uint32(i) {
			t.Errorf("第 %d 片的 seq = %d", i, h.Seq)
		}
		if h.Total != uint32(len(out)) {
			t.Errorf("第 %d 片的 total = %d，want %d", i, h.Total, len(out))
		}
		if h.Stream != 88 || h.Kind != KindMetadata {
			t.Errorf("第 %d 片的 kind/stream = %s/%d", i, h.Kind, h.Stream)
		}
	}
}

func TestWriteFrame(t *testing.T) {
	var buf bytes.Buffer
	data := bytes.Repeat([]byte{0x7E}, MaxFramePayload+50)
	f := Frame{Kind: KindData, Stream: 4, Data: data}
	if err := WriteFrame(&buf, f); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	// 写出长度必须等于 EncodeFrame 各片之和，且内容逐字节一致。
	frames, err := EncodeFrame(f)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	want := 0
	for _, b := range frames {
		want += len(b)
	}
	if buf.Len() != want {
		t.Errorf("写出 %d 字节，want %d", buf.Len(), want)
	}
	if !bytes.Equal(buf.Bytes(), bytes.Join(frames, nil)) {
		t.Error("WriteFrame 写出的字节与 EncodeFrame 不一致")
	}
}

func TestWriteFrameEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, Frame{Kind: KindHello, Stream: 1}); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	if buf.Len() != headerSize {
		t.Errorf("空消息应只写 %d 字节帧头，实得 %d", headerSize, buf.Len())
	}
}

type failWriter struct{ n int }

func (f *failWriter) Write(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, errors.New("写满了")
	}
	f.n--
	return len(p), nil
}

func TestWriteFramePropagatesWriteError(t *testing.T) {
	// 中间某帧写失败必须把错误抛出去。吞掉的话上层会以为整条发出去了。
	w := &failWriter{n: 1}
	data := bytes.Repeat([]byte{1}, MaxFramePayload*2)
	err := WriteFrame(w, Frame{Kind: KindData, Stream: 1, Data: data})
	if err == nil {
		t.Fatal("写失败应返回错误")
	}
}

func TestReassemblerDuplicateChunk(t *testing.T) {
	// 重传导致同一片到两次，不能把 got 加两次，否则永远集不齐。
	data := bytes.Repeat([]byte{0x22}, MaxFramePayload+500)
	frames, _ := EncodeFrame(Frame{Kind: KindData, Stream: 5, Data: data})

	var r Reassembler
	h0, _ := ParseChunk(frames[0])
	if _, err := r.Push(h0); !errors.Is(err, ErrChunkPending) {
		t.Fatalf("第一片后应 pending，实得 %v", err)
	}
	// 同一片重复喂入。
	if _, err := r.Push(h0); !errors.Is(err, ErrChunkPending) {
		t.Fatalf("重复片应仍然 pending，实得 %v", err)
	}

	var got Frame
	done := false
	for _, b := range frames[1:] {
		h, _ := ParseChunk(b)
		f, err := r.Push(h)
		if err == nil {
			got, done = f, true
			break
		}
		if !errors.Is(err, ErrChunkPending) {
			t.Fatalf("Push: %v", err)
		}
	}
	if !done {
		t.Fatal("重复片之后应仍能集齐")
	}
	if !bytes.Equal(got.Data, data) {
		t.Error("重复片导致数据错位")
	}
}

func TestReassemblerSwitchStreamDiscardsOld(t *testing.T) {
	// 上一组没传完就来了另一个 stream 的分片，应当放弃旧的、重新开始，
	// 而不是把两组的字节混在一起。两个都得是多片，才能走到「比对
	// stream 后 Reset」那条分支——单帧会直接穿透过去。
	old := bytes.Repeat([]byte{0xAA}, MaxFramePayload+100)
	oldFrames, _ := EncodeFrame(Frame{Kind: KindData, Stream: 10, Data: old})
	newData := bytes.Repeat([]byte{0xBB}, MaxFramePayload+100)
	newFrames, _ := EncodeFrame(Frame{Kind: KindData, Stream: 11, Data: newData})

	var r Reassembler
	h0, _ := ParseChunk(oldFrames[0])
	if _, err := r.Push(h0); !errors.Is(err, ErrChunkPending) {
		t.Fatalf("应 pending，实得 %v", err)
	}

	// 新 stream 的第一片：应当丢弃旧组并重新计数。
	h1, _ := ParseChunk(newFrames[0])
	if _, err := r.Push(h1); !errors.Is(err, ErrChunkPending) {
		t.Fatalf("换 stream 后新组第一片应 pending，实得 %v", err)
	}

	// 把新组剩下的喂完，必须完整拿到新组的数据，且一个字节都不含旧的。
	var got Frame
	done := false
	for _, b := range newFrames[1:] {
		h, _ := ParseChunk(b)
		f, err := r.Push(h)
		if err == nil {
			got, done = f, true
			break
		}
		if !errors.Is(err, ErrChunkPending) {
			t.Fatalf("Push: %v", err)
		}
	}
	if !done {
		t.Fatal("新 stream 没能集齐")
	}
	if got.Stream != 11 {
		t.Errorf("stream = %d，want 11", got.Stream)
	}
	if !bytes.Equal(got.Data, newData) {
		t.Error("换 stream 后数据混入了旧组")
	}
}

func TestReassemblerSwitchKindDiscardsOld(t *testing.T) {
	// 同样的 stream 号但 kind 变了，也必须重来——否则 ack 的字节会
	// 混进 metadata 里。
	big := bytes.Repeat([]byte{0xBB}, MaxFramePayload+100)

	var r Reassembler
	f1, _ := EncodeFrame(Frame{Kind: KindMetadata, Stream: 20, Data: big})
	h0, _ := ParseChunk(f1[0])
	if _, err := r.Push(h0); !errors.Is(err, ErrChunkPending) {
		t.Fatalf("应 pending，实得 %v", err)
	}

	f2, _ := EncodeFrame(Frame{Kind: KindAck, Stream: 20, Data: []byte(`{"ok":true}`)})
	h, _ := ParseChunk(f2[0])
	got, err := r.Push(h)
	if err != nil {
		t.Fatalf("kind 变化后的单帧应直接返回，实得 %v", err)
	}
	if got.Kind != KindAck {
		t.Errorf("kind = %s，want ack", got.Kind)
	}
}

func TestReassemblerRejectsZeroTotal(t *testing.T) {
	var r Reassembler
	_, err := r.Push(ChunkHeader{Kind: KindData, Stream: 1, IsPart: true, Seq: 0, Total: 0})
	if !errors.Is(err, ErrChunkMismatch) {
		t.Errorf("total=0 应返回 ErrChunkMismatch，实得 %v", err)
	}
}

func TestReassemblerResetAllowsReuse(t *testing.T) {
	data := bytes.Repeat([]byte{0x33}, MaxFramePayload+200)
	frames, _ := EncodeFrame(Frame{Kind: KindData, Stream: 1, Data: data})

	var r Reassembler
	for _, b := range frames {
		h, _ := ParseChunk(b)
		if _, err := r.Push(h); err == nil {
			break
		}
	}
	// Reset 之后应当能重新收一组新的。
	r.Reset()
	frames2, _ := EncodeFrame(Frame{Kind: KindData, Stream: 2, Data: []byte("again")})
	h, _ := ParseChunk(frames2[0])
	got, err := r.Push(h)
	if err != nil {
		t.Fatalf("Reset 后 Push: %v", err)
	}
	if string(got.Data) != "again" {
		t.Errorf("data = %q", got.Data)
	}
}

func TestParseChunkTruncatedChunkHeader(t *testing.T) {
	// 标了分片但只剩不到 9 字节的额外头，必须报 ErrShortFrame 而不是
	// 越界读出一堆零。
	b := make([]byte, headerSize+3)
	b[0] = byte(KindData)
	b[headerSize] = 1
	if _, err := ParseChunk(b); !errors.Is(err, ErrShortFrame) {
		t.Errorf("截断的分片头应返回 ErrShortFrame，实得 %v", err)
	}
}

func TestParseChunkNoBody(t *testing.T) {
	// 只有 5 字节帧头，正文为空——这是合法的最小帧。
	b := make([]byte, headerSize)
	b[0] = byte(KindHello)
	h, err := ParseChunk(b)
	if err != nil {
		t.Fatalf("ParseChunk: %v", err)
	}
	if len(h.Body) != 0 || h.IsPart {
		t.Errorf("空正文帧解析错了: %+v", h)
	}
}

func TestParseChunkBodyStartingWithOne(t *testing.T) {
	// 正文第一个字节恰好是 0x01 会被误判成分片——这是协议本身的歧义
	// （单帧没有分片标志位，只能靠正文首字节猜）。上层 JSON 以 { 开头、
	// 二进制块有自己的 stream 约定，所以实际不冲突。这里把行为钉住，
	// 将来若要改协议能立刻看到影响面。
	b := make([]byte, headerSize+chunkOverhead)
	b[0] = byte(KindData)
	// 分片标志位；后面 seq 与 total 都留 0（第 6..13 字节全 0）。
	// 注意别在这里写行尾注释：几行相邻的行尾注释在 gofmt 眼里是一张
	// 需要按列对齐的表格，手工空格对不上就会 gofmt -l 报未格式化，
	// CI 第一步就退出，go test 根本不跑——这已经踩过一次。
	b[headerSize] = 1
	h, err := ParseChunk(b)
	if err != nil {
		t.Fatalf("ParseChunk: %v", err)
	}
	if !h.IsPart {
		t.Error("首字节为 1 时会被当成分片，这是当前协议的行为")
	}
	if h.Total != 0 {
		t.Errorf("total = %d，want 0", h.Total)
	}
	// total=0 的分片喂给 Reassembler 必须被拒，不能死循环等待。
	var r Reassembler
	if _, err := r.Push(h); !errors.Is(err, ErrChunkMismatch) {
		t.Errorf("total=0 应返回 ErrChunkMismatch，实得 %v", err)
	}
}

func TestReassemblerNonPartPassthrough(t *testing.T) {
	// 非分片帧不该受之前残留状态影响。
	var r Reassembler
	h0 := ChunkHeader{Kind: KindData, Stream: 1, IsPart: true, Seq: 0, Total: 3}
	if _, err := r.Push(h0); !errors.Is(err, ErrChunkPending) {
		t.Fatalf("应 pending，实得 %v", err)
	}
	// 未完成的分片状态会残留；一条独立的非分片帧仍应原样返回。
	got, err := r.Push(ChunkHeader{Kind: KindAck, Stream: 9, Body: []byte("x")})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if got.Kind != KindAck || string(got.Data) != "x" {
		t.Errorf("got = %s/%q", got.Kind, got.Data)
	}
}
