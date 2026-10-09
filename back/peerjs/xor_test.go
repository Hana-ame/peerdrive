package peerjs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// ── XOR 编解码（表驱动）───────────────────────────────────────────────────────

// TestXOR_KnownVectors XOR 编解码已知向量：
// 密钥派生（SHA-256(secret:connID)）与帧级 XOR 的期望密文逐字节钉死，
// 防止未来实现漂移破坏两端一致性（编解码必须严格对称）。
func TestXOR_KnownVectors(t *testing.T) {
	key := deriveXORKey("s3cret", "conn-1")
	require.Equal(t,
		"fd131e8312ce478b985a6d21270767d160393ca43104440fd180c0dbc305bd1c",
		hex.EncodeToString(key), "deriveXORKey must stay stable (wire contract)")

	cases := []struct {
		name     string
		key      []byte
		plain    []byte
		wantCiph []byte
	}{
		{"hello-xor", key, []byte("hello xor"), mustHex(t, "957672ef7dee3fe4ea")},
		{"empty-key-identity", nil, []byte("plain stays"), []byte("plain stays")},
		{"empty-data-identity", key, nil, nil},
		{"single-byte", key, []byte{0x00}, []byte{key[0]}},
		{"key-length-multiple", key, bytes.Repeat([]byte{0xff}, 32), xorAll(t, key, 0xff)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := xorApply(tc.plain, tc.key)
			require.Equal(t, tc.wantCiph, got, "ciphertext mismatch")
			// XOR 自逆：两次应用还原为原文
			require.Equal(t, tc.plain, xorApply(got, tc.key), "round-trip must restore plaintext")
		})
	}
}

// TestXOR_RoundTrip_Table 表驱动 round-trip：多组 secret/connID/数据（含
// 非密钥整数倍长度、随机字节），加密→解密必须还原原文。
//
// Discovery background: 防御性测试——XOR 的对称性（自逆）是两端编解码一致
// 的基础，任何实现漂移（如偏移错误、跨帧状态）都会在这里暴露。
func TestXOR_RoundTrip_Table(t *testing.T) {
	data := [][]byte{
		[]byte(""),
		[]byte("a"),
		bytes.Repeat([]byte{0xAB}, 31),
		bytes.Repeat([]byte{0xAB}, 32),
		bytes.Repeat([]byte{0xAB}, 33),
		makeRandom(t, 64*1024),   // 一个 serveFile 块大小
		makeRandom(t, 64*1024+1), // 非块对齐
	}
	for i, d := range data {
		t.Run(fmt.Sprintf("data-%d-len-%d", i, len(d)), func(t *testing.T) {
			key := deriveXORKey(fmt.Sprintf("test-key-%d", i), fmt.Sprintf("conn-%d", i))
			ct := xorApply(d, key)
			require.Equal(t, len(d), len(ct), "ciphertext length must equal plaintext length")
			require.Equal(t, d, xorApply(ct, key), "round-trip")
		})
	}
}

// TestXOR_DeriveKey_BothEndsConsistent 密钥派生一致性：同一 (secret, connID)
// 两端（offerer 生成的 connectionId / answerer 从 OFFER 复用同一 connectionId）
// 必须派生同一把 key；不同 connID 或不同 secret 必须不同。
//
// Discovery background: 连接级密钥的契约——connID 由 offerer 生成、经 OFFER
// 传给 answerer 复用，这是两端无需额外握手就能拿到同一密钥的依据。
func TestXOR_DeriveKey_BothEndsConsistent(t *testing.T) {
	secret := "test-xor-shared-seed"
	connID := "conn-from-offerer"
	require.Equal(t, deriveXORKey(secret, connID), deriveXORKey(secret, connID))
	require.NotEqual(t, deriveXORKey(secret, connID), deriveXORKey(secret, "conn-other"))
	require.NotEqual(t, deriveXORKey(secret, connID), deriveXORKey("test-other-seed", connID))
	// 派生 == SHA-256(secret + ":" + connID)（与实现注释一致）
	sum := sha256.Sum256([]byte(secret + ":" + connID))
	require.Equal(t, sum[:], deriveXORKey(secret, connID))
}

// TestXOR_WrongKey_Mismatch 错误 key 解不出明文：用 keyA 加密、keyB 解密，
// 结果必须 != 明文（垃圾帧），且与明文逐字节不同。
//
// Discovery background: 兼容性契约——错 key 的帧在收侧解出垃圾，文本帧 JSON
// 解析失败被丢弃 → 取文件确定性失败，而不是静默返回损坏数据。
func TestXOR_WrongKey_Mismatch(t *testing.T) {
	keyA := deriveXORKey("test-xor-seed-a", "conn-1")
	keyB := deriveXORKey("test-xor-seed-b", "conn-1")
	plain := []byte(`{"type":"req","hash":"abcd"}`)
	ct := xorApply(plain, keyA)
	got := xorApply(ct, keyB)
	require.NotEqual(t, plain, got, "wrong key must not restore plaintext")
	require.False(t, json.Valid(got), "wrong-key text frame must not parse as JSON (garbage)")
}

// TestXOR_StreamChunks_Consistent 流式分块一致性：把大数据切成 serveFile 的
// 64KB 块，逐块独立加密→解密拼接，必须还原完整原文（块边界对齐、无跨块状态）。
// 同时钉住"同明文同 key 产出同密文"——帧级重置是 XOR 混淆的模式可分析边界
// （见 xor.go 头注释），测试把它写死为已认知的取舍。
func TestXOR_StreamChunks_Consistent(t *testing.T) {
	const chunkSize = 64 * 1024
	// 构造：两块完全相同的 64KB + 一块不同内容的尾块——既验证块边界对齐，
	// 也钉住"同明文同 key 产出同密文"（帧级密钥流重置的已认知代价）。
	chunk0 := makeRandom(t, chunkSize)
	orig := append(append([]byte{}, chunk0...), chunk0...)
	orig = append(orig, makeRandom(t, 12345)...)
	key := deriveXORKey("stream-secret", "conn-s")

	var rebuilt []byte
	identicalChunkCiphers := 0
	for off := 0; off < len(orig); off += chunkSize {
		end := off + chunkSize
		if end > len(orig) {
			end = len(orig)
		}
		chunk := orig[off:end]
		ct := xorApply(chunk, key)
		// 块独立：相同的明文块 → 相同的密文（帧级重置，无跨块状态）
		if off > 0 && bytes.Equal(orig[off-chunkSize:off], chunk) {
			require.Equal(t, xorApply(orig[off-chunkSize:off], key), ct,
				"identical plaintext chunk must produce identical ciphertext (frame-level reset)")
			identicalChunkCiphers++
		}
		rebuilt = append(rebuilt, xorApply(ct, key)...)
	}
	require.Equal(t, orig, rebuilt, "decrypted chunks concatenated must restore the full stream")
	require.Greater(t, identicalChunkCiphers, 0, "test must actually exercise repeated plaintext chunks")
}

// TestXOR_Disabled_Identity 开关行为：关闭（nil key）时 xorApply 恒等——
// 返回原 slice 且不分配（关 = 现状零变化，含分配行为）。
func TestXOR_Disabled_Identity(t *testing.T) {
	data := []byte("payload")
	got := xorApply(data, nil)
	require.True(t, &got[0] == &data[0], "disabled must return the same slice (no copy)")
	require.Equal(t, data, got)
}

// ── XOR 在 Connection 上的线缆行为（fakeDC 驱动）────────────────────────────

// TestConnection_XOR_Wire_SendReceive 开启 XOR 后：
//   - 发送侧：Send/SendText/SendFrame 落线字节是密文（≠ 明文）
//   - 收侧：注入密文帧 → onMessage 回调拿到明文（连接级编解码对称）
func TestConnection_XOR_Wire_SendReceive(t *testing.T) {
	p, _ := newTestPeer()
	c, dc := newTestConn(p, "conn-xor")
	openFake(dc)
	c.xorKey = deriveXORKey("s3cret", "conn-xor")

	// 发送侧
	require.NoError(t, c.Send([]byte("hello xor")))
	require.NoError(t, c.SendJSON(map[string]any{"type": "hello"}))
	body := []byte("body-bytes")
	require.NoError(t, c.SendFrame(map[string]any{"type": "data", "size": len(body)}, body))

	require.Len(t, dc.events, 4)
	// bin 帧（Send）必须是密文
	require.True(t, isBin(dc.events[0]))
	bin := []byte(dc.events[0][4:])
	require.NotEqual(t, []byte("hello xor"), bin, "wire bytes must be ciphertext")
	require.Equal(t, []byte("hello xor"), xorApply(bin, c.xorKey), "decrypt wire bytes")

	// text 帧（SendJSON）必须是密文
	require.True(t, isText(dc.events[1]))
	require.NotContains(t, dc.events[1], `"hello"`, "plaintext must not appear on wire")

	// SendFrame：text 头 + bin 体都是密文
	require.True(t, isText(dc.events[2]))
	require.True(t, isBin(dc.events[3]))
	require.NotContains(t, dc.events[2], `"data"`, "header plaintext must not appear on wire")
	require.NotEqual(t, body, []byte(dc.events[3][4:]), "body wire bytes must be ciphertext")

	// 收侧：注入密文帧 → 回调拿到明文
	var got []Frame
	c.OnMessage(func(f Frame) { got = append(got, f) })
	require.NotNil(t, dc.onMsg)
	dc.onMsg(Frame{IsText: true, Data: xorApply([]byte(`{"type":"meta","total":9}`), c.xorKey)})
	dc.onMsg(Frame{IsText: false, Data: xorApply([]byte("chunk-data"), c.xorKey)})
	require.Len(t, got, 2)
	require.True(t, got[0].IsText)
	require.Equal(t, `{"type":"meta","total":9}`, string(got[0].Data), "receiver must see plaintext")
	require.False(t, got[1].IsText)
	require.Equal(t, []byte("chunk-data"), got[1].Data)
}

// TestConnection_XOR_Off_Wire_NoChange 开关行为（关 = 现状零变化）：未配置
// xorKey 时 Send/SendText/SendFrame 的落线字节与加密引入前完全一致
// （text 明文 JSON / bin 原文）。
func TestConnection_XOR_Off_Wire_NoChange(t *testing.T) {
	p, _ := newTestPeer()
	c, dc := newTestConn(p, "conn-plain")
	openFake(dc)
	require.Nil(t, c.xorKey, "default must be disabled")

	require.NoError(t, c.Send([]byte{1, 2, 3}))
	require.NoError(t, c.SendJSON(map[string]any{"type": "hello"}))
	require.NoError(t, c.SendFrame(map[string]any{"type": "data", "size": 2}, []byte{9, 8}))
	require.Equal(t, []string{
		"bin:\x01\x02\x03",
		`text:{"type":"hello"}`,
		`text:{"size":2,"type":"data"}`,
		"bin:\x09\x08",
	}, dc.events)
}

// ── 真实双 peer（转发式信令 + 本机 WebRTC 直连）─────────────────────────────

// relaySignaller 最小 PeerJS 协议信令转发服务器：客户端 WS 连接（?id=&key=&token=），
// 连接后发 OPEN；收到的消息按 dst 转发（src 覆写为发送方 id，与真实服务器一致
// ——handleOffer 依赖 m.Src 作为对端 peer id）。
//
// Discovery background: peerjs 模块此前没有"真实双 peer"测试（peer_test 用
// fakeSignaller 无网络；contract_test 只测单客户端信令面）。XOR 是传输层
// 关注点，必须在真实 WebRTC DataChannel 上验证两端编解码一致 + 错 key 的
// 确定性失败形态，转发式信令即可离线跑通（同机 host candidate 直连，无需 STUN）。
// 坑：gorilla/websocket 每个 conn 只允许"一个读 + 一个写"——两端各自的 handler
// goroutine 会并发向对方的 conn WriteJSON（转发），不加锁是 -race 必现的数据
// 竞争；safeWS 用每 conn 写锁串行化（读仍由各 handler 自己的 goroutine 独占）。
type relaySignaller struct {
	mu      sync.Mutex
	key     string
	clients map[string]*safeWS
}

// safeWS 每 conn 写锁包装（gorilla 并发写同一 conn 是数据竞争）。
type safeWS struct {
	mu sync.Mutex
	c  *websocket.Conn
}

func (s *safeWS) WriteJSON(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.c.WriteJSON(v)
}

func newRelaySignaller(key string) *relaySignaller {
	return &relaySignaller{key: key, clients: map[string]*safeWS{}}
}

func (r *relaySignaller) up() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(r.handleWS))
}

func (r *relaySignaller) handleWS(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	if q.Get("key") != r.key || q.Get("id") == "" || q.Get("token") == "" {
		http.Error(w, "invalid key", http.StatusInternalServerError)
		return
	}
	id := q.Get("id")
	u := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := u.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	sw := &safeWS{c: conn}
	r.mu.Lock()
	r.clients[id] = sw
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.clients, id)
		r.mu.Unlock()
		_ = conn.Close()
	}()
	_ = sw.WriteJSON(Message{Type: MsgOpen})
	for {
		var m Message
		if err := conn.ReadJSON(&m); err != nil {
			return
		}
		if m.Dst == "" {
			continue // HEARTBEAT 等无路由目标的消息
		}
		m.Src = id // 真实服务器覆写 src
		r.mu.Lock()
		dst := r.clients[m.Dst]
		r.mu.Unlock()
		if dst != nil {
			_ = dst.WriteJSON(m)
		}
	}
}

// xorPeerOptions 构造指向 relay 的信令 Options（显式 id、长心跳间隔防噪声、
// XOR 开关按用例配置）。
func xorPeerOptions(host, port, id, secret string, xorOn bool) Options {
	return Options{
		Host:         host,
		Port:         port,
		Secure:       false,
		Path:         "/",
		Key:          "k1",
		ID:           id,
		Token:        "tok",
		PingInterval: time.Hour,
		XOREnable:    xorOn,
		XORKey:       secret,
	}
}

// newRelayPeer 起一个连到 relay 的 Peer（显式 id，免 /id 端点）。
func newRelayPeer(t *testing.T, hs *httptest.Server, id, secret string, xorOn bool) *Peer {
	t.Helper()
	host, port, _ := strings.Cut(strings.TrimPrefix(hs.URL, "http://"), ":")
	p := NewPeer(id, xorPeerOptions(host, port, id, secret, xorOn))
	require.NoError(t, p.Dial(context.Background()))
	t.Cleanup(p.Close)
	return p
}

// waitFrame 等待收侧回调送达一帧（带超时）。
func waitFrame(t *testing.T, ch chan Frame) Frame {
	t.Helper()
	select {
	case f := <-ch:
		return f
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for frame over real WebRTC")
		return Frame{}
	}
}

// waitFrameType 等待指定类型（wantText）的帧：SendFrame 落线是"text 头 + bin
// 体"两帧，收侧按序送达；按类型过滤掉头帧，取目标帧。
func waitFrameType(t *testing.T, ch chan Frame, wantText bool) Frame {
	t.Helper()
	for {
		select {
		case f := <-ch:
			if f.IsText == wantText {
				return f
			}
			// 头帧：继续等目标帧
		case <-time.After(15 * time.Second):
			t.Fatalf("timed out waiting for %s frame over real WebRTC", map[bool]string{true: "text", false: "binary"}[wantText])
			return Frame{}
		}
	}
}

// TestXOR_RealDualPeer_RoundTrip 真实双 peer（转发式信令 + 本机 WebRTC 直连）、
// 两端同 secret 开启 XOR：文本帧与二进制帧跨连接往返后必须是明文一致——
// 传输层加密对上层透明（编解码对称）。参照主模块集成测试 TestTwoNodesInterop
// 的起法（同机 host candidate 直连），但加密断言在库层做。
func TestXOR_RealDualPeer_RoundTrip(t *testing.T) {
	srv := newRelaySignaller("k1")
	hs := srv.up()
	defer hs.Close()

	pA := newRelayPeer(t, hs, "xor-a", "shared-secret", true)
	pB := newRelayPeer(t, hs, "xor-b", "shared-secret", true)

	// A（answerer）侧收帧通道 + 回发通道
	type recv struct {
		conn *Connection
		ch   chan Frame
	}
	aRecv := make(chan recv, 1)
	pA.OnConnection(func(c *Connection) {
		ch := make(chan Frame, 8)
		c.OnMessage(func(f Frame) { ch <- f })
		aRecv <- recv{conn: c, ch: ch}
	})

	// B（offerer）侧收帧通道
	bCh := make(chan Frame, 8)
	conn, err := pB.Connect(context.Background(), "xor-a", "peerdrive")
	require.NoError(t, err)
	conn.OnMessage(func(f Frame) { bCh <- f })
	opened := make(chan struct{})
	conn.OnOpen(func(*Connection) { close(opened) })
	select {
	case <-opened:
	case <-time.After(15 * time.Second):
		t.Fatal("DataChannel did not open over relay signaling")
	}

	// B → A：二进制帧（数据块）——SendFrame 落线是"text 头 + bin 体"两帧，
	// 收侧先收到头；按类型过滤到 bin 体。
	body := makeRandom(t, 700*1024) // 跨多个 64KB 逻辑块的单帧
	require.NoError(t, conn.SendFrame(map[string]any{"type": "data", "size": len(body)}, body))
	aSide := <-aRecv
	a := waitFrameType(t, aSide.ch, false)
	require.Equal(t, body, a.Data, "peer A must receive plaintext (XOR transparent)")

	// B → A：文本帧（JSON 控制头）
	require.NoError(t, conn.SendJSON(map[string]any{"type": "meta", "total": 700 * 1024}))
	ta := waitFrameType(t, aSide.ch, true)
	require.JSONEq(t, `{"type":"meta","total":716800}`, string(ta.Data))

	// A → B：回发（answerer 侧连接同样加密）
	require.NoError(t, aSide.conn.SendFrame(map[string]any{"type": "done", "size": len(body)}, body[:1024]))
	fb := waitFrameType(t, bCh, false)
	require.Equal(t, body[:1024], fb.Data, "peer B must receive plaintext reply")
}

// TestXOR_RealDualPeer_WrongKey_Garbage 兼容性边界：两端 XOR 密钥不一致时，
// WebRTC 传输层照常连通（错 key 不破坏传输），但应用层帧在收侧解出垃圾——
// 文本帧 JSON 解析失败被上层丢弃 → 取文件确定性失败，而不是静默损坏。
func TestXOR_RealDualPeer_WrongKey_Garbage(t *testing.T) {
	srv := newRelaySignaller("k1")
	hs := srv.up()
	defer hs.Close()

	pA := newRelayPeer(t, hs, "xor-a", "test-xor-seed-a", true)
	pB := newRelayPeer(t, hs, "xor-b", "test-xor-seed-b", true) // 不同 secret

	aCh := make(chan Frame, 8)
	pA.OnConnection(func(c *Connection) {
		c.OnMessage(func(f Frame) { aCh <- f })
	})
	conn, err := pB.Connect(context.Background(), "xor-a", "peerdrive")
	require.NoError(t, err)
	opened := make(chan struct{})
	conn.OnOpen(func(*Connection) { close(opened) })
	select {
	case <-opened:
	case <-time.After(15 * time.Second):
		t.Fatal("DataChannel did not open (wrong key must not break transport)")
	}

	plain := `{"type":"req","hash":"deadbeef"}`
	require.NoError(t, conn.SendText(plain))
	f := waitFrame(t, aCh)
	require.True(t, f.IsText, "wrong key preserves frame type")
	require.NotEqual(t, plain, string(f.Data),
		"A must NOT see B's plaintext (B's key != A's key)")
	require.False(t, json.Valid(f.Data),
		"A decrypts with its own key → garbage that cannot parse as JSON")
}

// ── 测试辅助 ────────────────────────────────────────────────────────────────

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}

// xorAll 计算 len(key) 个重复字节 plain 与 key 逐位异或的期望密文。
func xorAll(t *testing.T, key []byte, plain byte) []byte {
	t.Helper()
	want := make([]byte, len(key))
	for i := range want {
		want[i] = plain ^ key[i]
	}
	return want
}

func makeRandom(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*31 + 7) % 251) // 确定性伪随机，避免测试依赖 crypto/rand
	}
	return b
}
