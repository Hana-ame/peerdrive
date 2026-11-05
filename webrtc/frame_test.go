package webrtc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestKindString(t *testing.T) {
	cases := map[Kind]string{
		KindHello:    "hello",
		KindControl:  "control",
		KindMetadata: "metadata",
		KindData:     "data",
		KindCheck:    "check",
		KindAck:      "ack",
		KindError:    "error",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", k, got, want)
		}
	}
	if got := Kind(200).String(); got != "kind(200)" {
		t.Errorf("unknown kind = %q", got)
	}
}

func TestKindIsText(t *testing.T) {
	if !KindMetadata.IsText() || !KindControl.IsText() || !KindCheck.IsText() {
		t.Error("文本类帧应返回 true")
	}
	if KindData.IsText() {
		t.Error("KindData 不应被当作文本")
	}
}

func TestEncodeSingleFrame(t *testing.T) {
	f := Frame{Kind: KindData, Stream: 7, Data: []byte("hello")}
	out, err := EncodeFrame(f)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("小消息应只产生 1 帧，实得 %d", len(out))
	}
	b := out[0]
	if Kind(b[0]) != KindData {
		t.Errorf("kind = %d", b[0])
	}
	if got := binary.BigEndian.Uint32(b[1:5]); got != 7 {
		t.Errorf("stream = %d", got)
	}
	if !bytes.Equal(b[headerSize:], []byte("hello")) {
		t.Errorf("body = %q", b[headerSize:])
	}
}

func TestEncodeEmptyFrame(t *testing.T) {
	out, err := EncodeFrame(Frame{Kind: KindHello, Stream: 1})
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	if len(out) != 1 || len(out[0]) != headerSize {
		t.Fatalf("空 payload 应只有帧头，实得 %d 帧 %d 字节", len(out), len(out[0]))
	}
}

func TestChunkRoundTrip(t *testing.T) {
	// 造一段明显超过单帧上限的二进制，含 NUL 与高位字节，验证二进制安全。
	size := MaxFramePayload*2 + 1234
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i * 31 % 256)
	}

	frames, err := EncodeFrame(Frame{Kind: KindData, Stream: 42, Data: data})
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	if len(frames) < 2 {
		t.Fatalf("大数据应被切成多帧，实得 %d", len(frames))
	}

	var r Reassembler
	var got Frame
	for i, b := range frames {
		h, err := ParseChunk(b)
		if err != nil {
			t.Fatalf("ParseChunk(%d): %v", i, err)
		}
		f, err := r.Push(h)
		if err != nil {
			if errors.Is(err, ErrChunkPending) {
				continue
			}
			t.Fatalf("Push(%d): %v", i, err)
		}
		got = f
	}

	if got.Kind != KindData || got.Stream != 42 {
		t.Errorf("kind/stream = %s/%d", got.Kind, got.Stream)
	}
	if !bytes.Equal(got.Data, data) {
		t.Errorf("重组结果与原数据不一致: %d vs %d 字节", len(got.Data), len(data))
	}
}

func TestReassemblerOutOfOrder(t *testing.T) {
	data := bytes.Repeat([]byte("abcdefgh"), 40000) // 320000 字节，必然多片
	frames, err := EncodeFrame(Frame{Kind: KindData, Stream: 9, Data: data})
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}

	var r Reassembler
	// 倒序喂入，验证按 seq 归位。
	var got Frame
	done := false
	for i := len(frames) - 1; i >= 0; i-- {
		h, err := ParseChunk(frames[i])
		if err != nil {
			t.Fatalf("ParseChunk: %v", err)
		}
		f, err := r.Push(h)
		if err == nil {
			got = f
			done = true
			break
		}
		if !errors.Is(err, ErrChunkPending) {
			t.Fatalf("Push: %v", err)
		}
	}
	if !done {
		t.Fatal("倒序喂入未能重组完成")
	}
	if !bytes.Equal(got.Data, data) {
		t.Error("倒序重组结果不一致")
	}
}

func TestParseChunkShort(t *testing.T) {
	if _, err := ParseChunk([]byte{1, 2}); !errors.Is(err, ErrShortFrame) {
		t.Errorf("短帧应返回 ErrShortFrame，实得 %v", err)
	}
}

func TestReassemblerRejectsBadSeq(t *testing.T) {
	var r Reassembler
	_, err := r.Push(ChunkHeader{Kind: KindData, Stream: 1, IsPart: true, Seq: 5, Total: 2})
	if !errors.Is(err, ErrChunkMismatch) {
		t.Errorf("seq >= total 应返回 ErrChunkMismatch，实得 %v", err)
	}
}

func TestHashBytes(t *testing.T) {
	// 空串的 sha256 是众所周知的定值。
	const want = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := HashBytes(nil); got != want {
		t.Errorf("HashBytes(nil) = %q", got)
	}
}
