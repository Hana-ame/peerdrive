package webrtc

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 本文件是 JS ↔ Go 的帧协议对拍。
//
// 为什么值得单独写一个测试：frame.go 与 frame.js 是同一套协议的两次手写
// 实现，谁都不会因为对方改动而编译失败。线上真正踩过的两个坑都属于这类
// 「两边各自能跑，合起来不通」：
//   - 页面按 dc.readyState 判断就绪，PeerJS 根本没有这个字段
//   - 分片重组时 total 的语义两边理解不一致
// 前者已经让 e2e 红了十几轮。这类问题在 Go 单测里看不见，在 e2e 里只能
// 看到「没收到帧」。这里把两边的编解码直接对着喂，谁动了协议就立刻红。

// parCase 是一条编码用例：Go 与 JS 各自编码，输出必须逐字节相同。
// Data 是 base64 的原始正文。
type parCase struct {
	Name   string `json:"name"`
	Kind   uint8  `json:"kind"`
	Stream uint32 `json:"stream"`
	Data   string `json:"data"`
}

// decCase 是一条解码用例：把同一批帧分别喂给 Go 与 JS，期望结果一致。
// Frames 里的每一项都是一帧 base64。
type decCase struct {
	Name   string   `json:"name"`
	Frames []string `json:"frames"`
}

type parityInput struct {
	Encode []parCase `json:"encode"`
	Decode []decCase `json:"decode"`
}

type jsFrame struct {
	Kind   uint8  `json:"kind"`
	Stream uint32 `json:"stream"`
	Data   string `json:"data"`
}

type jsDecoded struct {
	Name    string     `json:"name"`
	Frames  []string   `json:"frames,omitempty"`
	Decoded []*jsFrame `json:"decoded,omitempty"`
}

type parityOutput struct {
	Cases     []jsDecoded `json:"cases"`
	Constants struct {
		KIND          map[string]uint8 `json:"KIND"`
		HeaderSize    int              `json:"headerSize"`
		MaxPayload    int              `json:"maxPayload"`
		ChunkOverhead int              `json:"chunkOverhead"`
	} `json:"constants"`
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("base64 解码失败: %v", err)
	}
	return b
}

// pattern 造可复现的伪随机内容。不用随机数：失败要能原样重跑。
func pattern(n int, seed byte) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = seed + byte(i*31) + byte(i>>8)
	}
	return out
}

// nodeBin 找 node。CI 上有 setup-node，本地可能没有——
// 没有就 skip，不能让它变成「本地跑不了所以整包红」。
func nodeBin(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"node", "node.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("找不到 node，跳过 JS↔Go 帧协议对拍")
	return ""
}

// runParity 把 input 喂给 frame_parity.mjs，拿回 JS 侧的结果。
func runParity(t *testing.T, in parityInput) parityOutput {
	t.Helper()
	node := nodeBin(t)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	script := filepath.Join(wd, "frame_parity.mjs")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("找不到 %s: %v", script, err)
	}

	payload, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// 走 stdin 就是为了不受长度限制（argv 在 Windows 上约 32 KB）。
	// 一次多片用例的 base64 就有几百 KB 到 1 MB，所以这里不能设上限。
	cmd := exec.Command(node, script)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Dir = wd

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("node frame_parity.mjs 失败: %v\nstderr:\n%s", err, stderr.String())
	}

	var out parityOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("解析 node 输出失败: %v\nstdout 前 500 字节:\n%.500s", err, stdout.String())
	}
	return out
}

// TestFrameParityConstants 先钉住两边共享的标量常量。
//
// 常量漂移是最容易发生、也最难在现象里看出来的：MAX_PAYLOAD 差 1 就会
// 一边切片一边不切，症状是「大文件传过去是坏的」，日志上一切正常。
func TestFrameParityConstants(t *testing.T) {
	out := runParity(t, parityInput{})

	if out.Constants.HeaderSize != headerSize {
		t.Errorf("headerSize: JS %d，Go %d", out.Constants.HeaderSize, headerSize)
	}
	if out.Constants.MaxPayload != MaxFramePayload {
		t.Errorf("maxPayload: JS %d，Go %d", out.Constants.MaxPayload, MaxFramePayload)
	}
	if out.Constants.ChunkOverhead != chunkOverhead {
		t.Errorf("chunkOverhead: JS %d，Go %d", out.Constants.ChunkOverhead, chunkOverhead)
	}

	goKinds := map[string]uint8{
		"HELLO":    uint8(KindHello),
		"CONTROL":  uint8(KindControl),
		"METADATA": uint8(KindMetadata),
		"DATA":     uint8(KindData),
		"CHECK":    uint8(KindCheck),
		"ACK":      uint8(KindAck),
		"ERROR":    uint8(KindError),
	}
	for name, want := range goKinds {
		got, ok := out.Constants.KIND[name]
		if !ok {
			t.Errorf("JS 的 KIND 里没有 %s", name)
			continue
		}
		if got != want {
			t.Errorf("kind %s: JS %d，Go %d", name, got, want)
		}
	}
	if len(out.Constants.KIND) != len(goKinds) {
		t.Errorf("KIND 项数: JS %d，Go %d", len(out.Constants.KIND), len(goKinds))
	}
}

// TestFrameParityEncode 逐字节比对两边的编码结果。
//
// 用例覆盖：空正文、1 字节、正好上限、上限 ±1、多片且最后一片不满、
// 分片头里 seq/total 的高位字节（stream 用 0x01020304 这种好认的值）。
func TestFrameParityEncode(t *testing.T) {
	cases := []struct {
		name   string
		kind   Kind
		stream uint32
		data   []byte
	}{
		{"空正文", KindHello, 0, []byte{}},
		{"1字节", KindData, 1, []byte{0x42}},
		{"普通JSON", KindControl, 7, []byte(`{"action":"ping","at":1700000000}`)},
		{"stream高字节", KindMetadata, 0x01020304, []byte(`{"a":1}`)},
		{"stream全F", KindAck, 0xFFFFFFFF, []byte("x")},
		{"正好上限", KindData, 2, pattern(MaxFramePayload, 0x11)},
		{"上限减一", KindData, 3, pattern(MaxFramePayload-1, 0x22)},
		{"上限加一", KindData, 4, pattern(MaxFramePayload+1, 0x33)},
		{"两片整", KindData, 5, pattern((MaxFramePayload-chunkOverhead)*2, 0x44)},
		{"三片加零头", KindData, 6, pattern((MaxFramePayload-chunkOverhead)*3+777, 0x55)},
		{"大stream多片", KindMetadata, 0xDEADBEEF, pattern(MaxFramePayload*2+3, 0x66)},
	}

	in := parityInput{}
	for _, c := range cases {
		in.Encode = append(in.Encode, parCase{
			Name: c.name, Kind: uint8(c.kind), Stream: c.stream, Data: b64(c.data),
		})
	}

	out := runParity(t, in)
	if len(out.Cases) != len(cases) {
		t.Fatalf("JS 回了 %d 组用例，want %d", len(out.Cases), len(cases))
	}

	for i, c := range cases {
		got := out.Cases[i]
		if got.Name != c.name {
			t.Fatalf("用例顺序错位: JS 第 %d 组是 %q，want %q", i, got.Name, c.name)
		}

		wantFrames, err := EncodeFrame(Frame{Kind: c.kind, Stream: c.stream, Data: c.data})
		if err != nil {
			t.Fatalf("%s: Go EncodeFrame: %v", c.name, err)
		}
		if len(got.Frames) != len(wantFrames) {
			t.Errorf("%s: JS 切了 %d 片，Go 切了 %d 片", c.name, len(got.Frames), len(wantFrames))
			continue
		}
		for j := range wantFrames {
			jsBytes := mustB64(t, got.Frames[j])
			if !bytes.Equal(jsBytes, wantFrames[j]) {
				// 只报差异位置，不打印几万字节。
				t.Errorf("%s 第 %d 片不一致：JS %d 字节，Go %d 字节，首个差异在 %d",
					c.name, j, len(jsBytes), len(wantFrames[j]), firstDiff(jsBytes, wantFrames[j]))
				t.Errorf("   JS 前 16 字节: %x", headBytes(jsBytes, 16))
				t.Errorf("   Go 前 16 字节: %x", headBytes(wantFrames[j], 16))
			}
		}
	}
}

// TestFrameParityDecode 比对两边的解码与重组。
//
// 这一组用的是「JS 编码出来的帧」而不是 Go 编码的帧：只有 JS 自己
// 编出来的字节能被 JS 解回原样，才能说明它和 Go 在真实链路上互通——
// 用 Go 的字节去喂 JS，等于默认 Go 是对的，测不出 JS 侧编码偏移。
func TestFrameParityDecode(t *testing.T) {
	type one struct {
		name   string
		kind   Kind
		stream uint32
		data   []byte
	}
	cases := []one{
		{"空正文", KindHello, 0, []byte{}},
		{"短JSON", KindAck, 9, []byte(`{"ok":true}`)},
		{"越过上限", KindData, 12, pattern(MaxFramePayload+1, 0x99)},
		{"多片", KindData, 13, pattern((MaxFramePayload-chunkOverhead)*2+5, 0xAA)},
		{"二进制全零", KindData, 14, make([]byte, MaxFramePayload*2)},
	}

	// 先编一遍拿到帧序列，再让 JS 去解。
	enc := parityInput{}
	for _, c := range cases {
		enc.Encode = append(enc.Encode, parCase{
			Name: c.name, Kind: uint8(c.kind), Stream: c.stream, Data: b64(c.data),
		})
	}
	encOut := runParity(t, enc)

	dec := parityInput{}
	for i, c := range cases {
		dec.Decode = append(dec.Decode, decCase{Name: c.name, Frames: encOut.Cases[i].Frames})
	}
	out := runParity(t, dec)

	// 用例顺序错位会让后面所有断言张冠李戴，所以先全量对一遍名字。
	// 这里只看 decode 那一节的结果，不动 out.Cases 的整体长度——
	// mjs 把 encode 与 decode 都拼进同一个数组，本次输入的 encode 为空，
	// 所以 cases 里只会有 decode 的项。
	if len(out.Cases) != len(cases) {
		t.Fatalf("JS 回了 %d 组结果，want %d", len(out.Cases), len(cases))
	}
	for i := range cases {
		if out.Cases[i].Name != cases[i].name {
			t.Fatalf("用例顺序错位: JS 第 %d 组 %q，want %q", i, out.Cases[i].Name, cases[i].name)
		}
	}

	for i, c := range cases {
		got := out.Cases[i]
		// 单帧的用例只有一条记录；分片的多条里前面是 null（未收齐）。
		if len(got.Decoded) != len(encOut.Cases[i].Frames) {
			t.Fatalf("%s: JS 回了 %d 条解码结果，want %d", c.name, len(got.Decoded), len(encOut.Cases[i].Frames))
		}
		var last *jsFrame
		for j, d := range got.Decoded {
			if d == nil {
				if j == len(got.Decoded)-1 {
					t.Fatalf("%s: 最后一片之后仍是未收齐", c.name)
				}
				continue
			}
			last = d
		}
		if last == nil {
			t.Fatalf("%s: JS 一条都没解出来", c.name)
		}
		if last.Kind != uint8(c.kind) {
			t.Errorf("%s: kind = %d，want %d", c.name, last.Kind, c.kind)
		}
		if last.Stream != c.stream {
			t.Errorf("%s: stream = %d，want %d", c.name, last.Stream, c.stream)
		}
		if gotData := mustB64(t, last.Data); !bytes.Equal(gotData, c.data) {
			t.Errorf("%s: 重组结果不一致（%d 字节 vs %d 字节）", c.name, len(gotData), len(c.data))
		}
	}
}

// TestFrameParityGoDecodesJSBytes 反方向：JS 编出来的字节，Go 必须能解回原样。
//
// 上面那个用例只证明了「JS 自己能自洽」。真实链路是 JS → Go，所以
// 必须让 Go 去读 JS 的字节。这是整组对拍里最关键的一条。
func TestFrameParityGoDecodesJSBytes(t *testing.T) {
	data := pattern(MaxFramePayload*2+321, 0x7C)
	in := parityInput{Encode: []parCase{
		{Name: "跨片上送", Kind: uint8(KindData), Stream: 4242, Data: b64(data)},
	}}
	out := runParity(t, in)
	if len(out.Cases) != 1 || len(out.Cases[0].Frames) < 2 {
		t.Fatalf("预期多片，实得 %d 组", len(out.Cases))
	}

	var r Reassembler
	var got Frame
	done := false
	for i, b := range out.Cases[0].Frames {
		raw := mustB64(t, b)
		h, err := ParseChunk(raw)
		if err != nil {
			t.Fatalf("第 %d 片 ParseChunk: %v", i, err)
		}
		f, err := r.Push(h)
		if err != nil {
			if err == ErrChunkPending {
				continue
			}
			t.Fatalf("第 %d 片 Push: %v", i, err)
		}
		got, done = f, true
		break
	}
	if !done {
		t.Fatal("JS 编出来的帧 Go 没能重组完成")
	}
	if got.Stream != 4242 {
		t.Errorf("stream = %d，want 4242", got.Stream)
	}
	if got.Kind != KindData {
		t.Errorf("kind = %s，want data", got.Kind)
	}
	if !bytes.Equal(got.Data, data) {
		t.Errorf("JS 编 → Go 解 结果不一致：%d 字节 vs %d 字节", len(got.Data), len(data))
	}
}

// TestFrameParityDisorder 乱序到达。
//
// DataChannel 是可靠有序的，但测试这条是因为：只要组装逻辑依赖顺序，
// 将来任何一次「并行发帧」的优化都会炸。乱序能解出来说明组装真的按
// seq 落位。
func TestFrameParityDisorder(t *testing.T) {
	data := pattern((MaxFramePayload-chunkOverhead)*3+123, 0x5E)
	in := parityInput{Encode: []parCase{
		{Name: "乱序", Kind: uint8(KindMetadata), Stream: 77, Data: b64(data)},
	}}
	out := runParity(t, in)
	frames := out.Cases[0].Frames
	if len(frames) < 3 {
		t.Fatalf("预期至少 3 片，实得 %d", len(frames))
	}

	// 先倒序喂一遍，确认「未按 seq 到达」时 Go 仍能补齐。
	var r Reassembler
	var got Frame
	done := false
	for i := len(frames) - 1; i >= 0; i-- {
		h, err := ParseChunk(mustB64(t, frames[i]))
		if err != nil {
			t.Fatalf("ParseChunk: %v", err)
		}
		f, err := r.Push(h)
		if err == ErrChunkPending {
			continue
		}
		if err != nil {
			t.Fatalf("Push: %v", err)
		}
		got, done = f, true
		break
	}
	if !done {
		t.Fatal("倒序喂入没能重组完成")
	}
	if !bytes.Equal(got.Data, data) {
		t.Error("倒序重组结果不一致")
	}
}

// TestFrameParityFrameJSExists 保证 node 侧能读到 frame.js，
// 且 index.html 确实引用的是它。
//
// 前半段防「打包漏文件」：缺了的话上面所有用例只会报
// 「Cannot find module」，报错指向 node 而不是「少了 frame.js」。
// 后半段防的是更隐蔽的情况：index.html 里又冒出一份自己的 encode，
// 那 frame.js 就成了从未被页面加载的死代码——对拍永远绿，页面却
// 在用另一套实现，等于这道防线整个失效。
func TestFrameParityFrameJSExists(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(wd, "frame.js"))
	if err != nil {
		t.Fatalf("读不到 frame.js: %v", err)
	}
	// 双保险：这个文件里必须还留着协议常量，否则 index.html 拿到的
	// 就是一份空壳，页面会在运行时静默收发不了。
	for _, needle := range []string{"MAX_PAYLOAD", "CHUNK_OVERHEAD", "HEADER_SIZE"} {
		if !strings.Contains(string(b), needle) {
			t.Errorf("frame.js 里找不到 %s", needle)
		}
	}

	// index.html 必须真的引用 frame.js。
	html, err := os.ReadFile(filepath.Join(wd, "index.html"))
	if err != nil {
		t.Fatalf("读不到 index.html: %v", err)
	}
	page := string(html)
	if !strings.Contains(page, `src="./frame.js"`) {
		t.Error("index.html 没有 <script src=\"./frame.js\">，页面不会加载对拍的那份实现")
	}
	if !strings.Contains(page, "PDFrame") {
		t.Error("index.html 没用到 window.PDFrame")
	}
	// 页面里不该再有一份 DataView(...).setUint32(1, stream 之类的编码实现：
	// 只要出现了，就说明有人把它抄回去了，而对拍测的是 frame.js——
	// 两套实现会各自漂移，这道防线也就名存实亡。
	for _, forbidden := range []string{"setUint32(1, stream", "new Uint8Array(5 + CHUNK_OVERHEAD"} {
		if strings.Contains(page, forbidden) {
			t.Errorf("index.html 里又出现了一份编码实现（%q），应只保留 frame.js 那一份", forbidden)
		}
	}
}

func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func headBytes(b []byte, n int) []byte {
	if len(b) < n {
		return b
	}
	return b[:n]
}
