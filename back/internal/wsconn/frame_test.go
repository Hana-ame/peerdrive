package wsconn

import "testing"

func TestOpcodeValues(t *testing.T) {
	// 发现背景：opcode 常量刻意复制而非 import gorilla，目的是让线格式契约
	// 在传输层边界上可见。golden 向量套件会在漂移时失败；这里再钉一遍
	// 具体值，因为常量漂移是最容易静默发生的错误。
	for _, tc := range []struct {
		got  int
		want int
		name string
	}{
		{OpcodeContinuation, 0x00, "continuation"},
		{OpcodeText, 0x01, "text"},
		{OpcodeBinary, 0x02, "binary"},
		{OpcodeClose, 0x08, "close"},
		{OpcodePing, 0x09, "ping"},
		{OpcodePong, 0x0A, "pong"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s opcode = 0x%02x, want 0x%02x (RFC 6455 §5.5)",
				tc.name, tc.got, tc.want)
		}
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMessageToFrame(t *testing.T) {
	// 发现背景：text/binary 判定只在 MessageToFrame 一处定义，前端靠
	// IsText 区分控制帧与数据帧。这里断言映射是「仅 OpcodeText 为文本」，
	// 包括未被 gorilla 返回的保留 opcode。
	payload := []byte{0x01, 0x02, 0x03}
	for _, tc := range []struct {
		op   int
		text bool
	}{
		{OpcodeText, true},
		{OpcodeBinary, false},
		{OpcodeContinuation, false},
		{OpcodeClose, false},
		{OpcodePing, false},
		{OpcodePong, false},
		{0x03, false},
		{0x04, false},
		{0x05, false},
		{0x06, false},
		{0x0B, false},
		{0x0F, false},
	} {
		f := MessageToFrame(tc.op, payload)
		if f.IsText != tc.text {
			t.Errorf("MessageToFrame(0x%02x).IsText = %v, want %v",
				tc.op, f.IsText, tc.text)
		}
		if !bytesEqual(f.Data, payload) {
			t.Errorf("MessageToFrame(0x%02x) did not preserve the payload bytes", tc.op)
		}
	}
}
