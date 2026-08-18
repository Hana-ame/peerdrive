package peerjs

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── message.go / 工具函数 ────────────────────────────────────────────────

// 发现背景：防御性测试——工具函数基础行为：帧构造/序列化正确性，协议上层依赖
func TestNewMessage(t *testing.T) {
	m := NewMessage(MsgOffer, "dst-node", OfferPayload{ConnectionID: "c1"})
	assert.Equal(t, MsgOffer, m.Type)
	assert.Equal(t, "dst-node", m.Dst)
	var p OfferPayload
	require.NoError(t, json.Unmarshal(m.Payload, &p))
	assert.Equal(t, "c1", p.ConnectionID)

	// payload 为 nil 时 Payload 为空
	m2 := NewMessage(MsgHeartbeat, "", nil)
	assert.Empty(t, m2.Payload)
}

// 发现背景：防御性测试——ID 规则（首尾字母数字）是信令注册成功的前提，规则回归保护
func TestValidID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"peerdrive-abc123", true},
		{"a", true},
		{"a-b_c d", true}, // 中间允许 - _ 空格
		{"", false},
		{"-abc", false}, // 首字符必须字母数字
		{"abc-", false},
		{"a b!", false}, // 非法字符
		{"ab\ncd", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, validID(c.id), "id=%q", c.id)
	}
}

// 发现背景：防御性测试——connectionId 提取是信令路由键，提取错误会导致消息错配
func TestPayloadConnectionID(t *testing.T) {
	m := NewMessage(MsgCandidate, "dst", CandidatePayload{ConnectionID: "conn-42"})
	assert.Equal(t, "conn-42", payloadConnectionID(m))
	assert.Empty(t, payloadConnectionID(NewMessage(MsgAnswer, "dst", nil)))
}

// ── Peer：信令路由（fakeSignaller 驱动） ─────────────────────────────────

// offer 构造一条合法的 data OFFER 消息。
// 用真实 pion 生成的 offer SDP（SetRemoteDescription 才能成功——假 SDP
// 会导致 handleOffer 失败、连接被关闭，测试走不到真实路径）。
func offer(t *testing.T, src, connID string) Message {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	require.NoError(t, err)
	defer func() { _ = pc.Close() }()
	_, err = pc.CreateDataChannel("test", nil)
	require.NoError(t, err)
	offer, err := pc.CreateOffer(nil)
	require.NoError(t, err)
	require.NoError(t, pc.SetLocalDescription(offer))

	m := NewMessage(MsgOffer, "", OfferPayload{
		SDP:          &offer,
		Type:         ConnData,
		ConnectionID: connID,
		Label:        "test",
	})
	m.Src = src
	return m
}

// TestRouteOffer_AnswererUsesOffererConnectionID 回归：answerer 必须沿用 offerer 的
// connectionId（曾因新生成 ID 导致 ANSWER 路由不到、ICE 卡 checking）。
//
// 发现背景：双节点 E2E（真实 0.peerjs.com 信令）——B 发起连接后收到 ANSWER
// 但 ICE 永远停在 checking、DataChannel 不 open。加调试日志定位：A 回 ANSWER
// 时用的是自己新生成的 connectionId，B 按该 id 在 conns 里找不到连接，消息被丢弃。
func TestRouteOffer_AnswererUsesOffererConnectionID(t *testing.T) {
	p, f := newTestPeer()
	_ = f

	p.OnConnection(func(c *Connection) {})

	f.inject(offer(t, "remote-node", "conn-from-offerer"))

	p.mu.Lock()
	conn := p.conns["conn-from-offerer"]
	p.mu.Unlock()
	require.NotNil(t, conn, "连接必须以 offerer 提供的 connectionId 注册")
	assert.Equal(t, "conn-from-offerer", conn.ID)
	assert.Equal(t, "remote-node", conn.PeerID)

	// 应答必须是 ANSWER 且带同一 connectionId
	var found bool
	for _, m := range f.sentSnapshot() {
		if m.Type == MsgAnswer {
			var p AnswerPayload
			require.NoError(t, json.Unmarshal(m.Payload, &p))
			assert.Equal(t, "conn-from-offerer", p.ConnectionID)
			found = true
		}
	}
	assert.True(t, found, "必须发出 ANSWER 应答")
}

// TestRouteOffer_DuplicateConnectionID_ClosesOld 回归：重复 connectionId 时
// 旧连接必须完整 Close（曾只 pc.Close：残留 conns map、done 永不关闭、
// onClose 不触发 → 上层挂起）。
//
// 发现背景：
//  1. 代码审阅发现「重复 OFFER 只关 pc 不清理注册表」的泄漏路径
//  2. 写本测试时又暴露连环 bug：改成 old.Close() 后在持 p.mu 时调用 →
//     Close→forgetConnection 需要同一把锁 → Go mutex 非重入死锁 → 测试卡死。
//     修复：锁内只取出 old，解锁后再 Close。
func TestRouteOffer_DuplicateConnectionID_ClosesOld(t *testing.T) {
	p, f := newTestPeer()
	p.OnConnection(func(c *Connection) {})

	f.inject(offer(t, "remote-node", "conn-x"))

	p.mu.Lock()
	first := p.conns["conn-x"]
	p.mu.Unlock()
	require.NotNil(t, first)

	// 第二次同 connectionId 的 OFFER
	f.inject(offer(t, "remote-node", "conn-x"))

	// 旧连接 done 必须已关闭（connectLoop 依赖它退出重连）
	select {
	case <-first.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("旧连接的 Done 未关闭：泄漏（修复前行为）")
	}
	// 旧连接必须从 conns 移除，新连接接管
	p.mu.Lock()
	cur := p.conns["conn-x"]
	p.mu.Unlock()
	assert.NotNil(t, cur)
	assert.NotSame(t, first, cur, "必须是新连接")
}

// TestRouteExpire_ClosesConnection 回归：EXPIRE（OFFER 入队过期）必须关闭
// 连接并触发 Done（connectLoop 依赖它重连；修复前永远等 OnOpen）。
//
// 发现背景：双节点 E2E——B 比 A 先启动（A 未上线），B 的 OFFER 在信令服务器
// 入队后过期，服务端回 EXPIRE；B 的 connectLoop 仍在等 OnOpen，20s 后超时
// 且不重试，A 上线后也连不上。修复：EXPIRE → Close（触发 Done）→ 循环重连。
func TestRouteExpire_ClosesConnection(t *testing.T) {
	p, f := newTestPeer()
	c, _ := newTestConn(p, "conn-e") // 直接注册，不依赖 OFFER 流程

	exp := NewMessage(MsgExpire, "", struct {
		ConnectionID string `json:"connectionId"`
	}{ConnectionID: "conn-e"})
	exp.Src = "remote-node"
	f.inject(exp)

	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("EXPIRE 后连接未关闭")
	}
	p.mu.Lock()
	_, still := p.conns["conn-e"]
	p.mu.Unlock()
	assert.False(t, still, "连接应从注册表移除")
}

// TestRouteLeave_ClosesAllFromPeer LEAVE 关闭该远端的所有连接。
//
// 发现背景：代码审阅发现 handleLeave 在持有 p.mu 时逐个 c.Close()——
// Close→forgetConnection 需要同一把锁 → Go mutex 非重入死锁。
// 本测试在 -race 下直接复现（3 节点场景多个连接时卡死），
// 修复为锁内收集、解锁后关闭。
func TestRouteLeave_ClosesAllFromPeer(t *testing.T) {
	p, f := newTestPeer()
	_, _ = newTestConn(p, "c-a")
	_, _ = newTestConn(p, "c-b")
	_, _ = newTestConn(p, "c-c")

	// c-a/c-b 属于 remote-node，c-c 属于 other-node
	p.mu.Lock()
	p.conns["c-a"].PeerID = "remote-node"
	p.conns["c-b"].PeerID = "remote-node"
	p.conns["c-c"].PeerID = "other-node"
	p.mu.Unlock()

	leave := NewMessage(MsgLeave, "", nil)
	leave.Src = "remote-node"
	f.inject(leave)

	p.mu.Lock()
	_, hasA := p.conns["c-a"]
	_, hasB := p.conns["c-b"]
	_, hasC := p.conns["c-c"]
	p.mu.Unlock()
	assert.False(t, hasA, "remote-node 的连接应全部关闭")
	assert.False(t, hasB)
	assert.True(t, hasC, "其他节点的连接不受影响")
}

// TestRouteCandidate_UnknownConnID_Ignored 未知 connectionId 的候选不 panic。
//
// 发现背景：防御性测试——对端可能发来乱序/过期的 ICE 候选（或恶意注入），
// route 对未知 connectionId 必须安全丢弃而不是 panic（曾考虑过 nil conn 解引用）。
func TestRouteCandidate_UnknownConnID_Ignored(t *testing.T) {
	p, f := newTestPeer()
	cand := NewMessage(MsgCandidate, "", CandidatePayload{ConnectionID: "nope"})
	assert.NotPanics(t, func() { f.inject(cand) })
	_ = p
}

// TestRouteHeartbeat_Ignored 心跳消息无副作用。
//
// 发现背景：peerjs-server 会向客户端发 HEARTBEAT（服务端保活探测），
// 客户端不应应答也不应产生任何副作用（peerjs-client 行为对齐）。
func TestRouteHeartbeat_Ignored(t *testing.T) {
	_, f := newTestPeer()
	assert.NotPanics(t, func() {
		f.inject(NewMessage(MsgHeartbeat, "", nil))
	})
	assert.Empty(t, f.sent, "心跳不应产生发送")
}

// ── Peer：Close 幂等性与生命周期 ─────────────────────────────────────────

// TestPeerClose_Idempotent 回归：Close 重复调用不 panic（曾担心 closed channel 重复关闭）。
//
// 发现背景：代码审阅——Peer.Close / Connection.Close / signaller.Close 三处
// 都有「close(channel)」逻辑，重复调用会 panic（close of closed channel），
// 而 service 层 startLoop/Close 可能并发触发多次关闭。
func TestPeerClose_Idempotent(t *testing.T) {
	p, _ := newTestPeer()
	p.Close()
	assert.NotPanics(t, p.Close)
	assert.NotPanics(t, p.Close)
}

// TestPeerClose_ClosesAllConnections Peer.Close 关闭所有连接并触发 Done。
//
// 发现背景：closeOnce 保证每个 Connection 只清理一次；本测试验证 Peer 级
// 关闭能级联到全部连接（无连接被遗漏在 conns map 里等泄漏）。
func TestPeerClose_ClosesAllConnections(t *testing.T) {
	p, _ := newTestPeer()
	c1, _ := newTestConn(p, "c1")
	c2, _ := newTestConn(p, "c2")

	p.Close()

	select {
	case <-c1.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("c1 未关闭")
	}
	select {
	case <-c2.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("c2 未关闭")
	}
}

// TestSetICEServers NewPeerWithSignaller 路径必须有 ICE 配置入口（修复前恒 nil）。
//
// 发现背景：代码审阅——NewPeerWithSignaller（自定义信令）路径下 p.iceServers
// 恒为 nil，WebRTC 只有局域网 host 候选、无法跨公网打洞；Options.ICEServers
// 只覆盖了 PeerJS 云信令路径。
func TestSetICEServers(t *testing.T) {
	p, _ := newTestPeer()
	servers := []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}}
	p.SetICEServers(servers)

	// 通过 Connect 建立连接时使用该配置（真实 pion PC 创建，无网络）
	conn, err := p.Connect(context.Background(), "remote-node", "t")
	require.NoError(t, err)
	assert.NotNil(t, conn)
	conn.Close()
}

// ── Connection：帧协议 ────────────────────────────────────────────────────

// TestConnection_SendJSON_UsesTextFrame 回归：JSON 头必须是文本帧
// （曾用二进制帧导致对端把控制头当数据块吞掉）。
//
// 发现背景：双节点 E2E——Go 端 JSON 头用 dc.Send([]byte)（二进制帧，PPID 53）
// 发送，peerjs 浏览器端把全部消息当数据块收集，done 帧永远不触发、拉取超时；
// 加日志发现对端收到的是二进制帧。修复：头用 SendText（PPID 51）。
func TestConnection_SendJSON_UsesTextFrame(t *testing.T) {
	p, _ := newTestPeer()
	c, dc := newTestConn(p, "c1")
	openFake(dc)

	require.NoError(t, c.SendJSON(map[string]any{"type": "hello"}))
	require.Len(t, dc.events, 1)
	assert.Contains(t, dc.events[0], "text:")
	assert.Contains(t, dc.events[0], `"hello"`)
}

// TestConnection_Send_BinaryFrame 数据块必须是二进制帧。
//
// 发现背景：与 TestConnection_SendJSON_UsesTextFrame 同一 E2E——协议靠
// 「文本帧=控制头 / 二进制帧=数据块」区分，Send 必须保持二进制语义。
func TestConnection_Send_BinaryFrame(t *testing.T) {
	p, _ := newTestPeer()
	c, dc := newTestConn(p, "c1")
	openFake(dc)

	require.NoError(t, c.Send([]byte{1, 2, 3}))
	require.Len(t, dc.events, 1)
	assert.Equal(t, "bin:\x01\x02\x03", dc.events[0])
}

// TestConnection_SendFrame_Atomic 回归：并发发送时 data 头与二进制体必须
// 原子连续（曾因无锁交织导致接收端 expect 状态机挂错请求）。
//
// 发现背景：代码审阅——多个 goroutine 并发 serveFile 时，若 data 头与
// 二进制体不连续落线，接收端把二进制块挂到错误的请求上（reqId 状态机错乱）。
// SendFrame 的 sendMu 保证一帧原子；本测试 8 goroutine × 50 轮暴力验证。
func TestConnection_SendFrame_Atomic(t *testing.T) {
	p, _ := newTestPeer()
	c, dc := newTestConn(p, "c1")
	openFake(dc)

	const workers = 8
	const perWorker = 50
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				header := map[string]any{"type": "data", "w": w, "i": i}
				body := []byte{byte(w), byte(i)}
				if err := c.SendFrame(header, body); err != nil {
					t.Errorf("SendFrame: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// 每个 text: 头必须紧跟对应的 bin: 体
	assert.GreaterOrEqual(t, len(dc.events), workers*perWorker*2)
	for i := 0; i+1 < len(dc.events); i += 2 {
		assert.True(t, isText(dc.events[i]), "事件 %d 应为文本头: %v", i, dc.events[i])
		assert.True(t, isBin(dc.events[i+1]), "事件 %d 应为二进制体: %v", i+1, dc.events[i+1])
	}
}

func isText(e string) bool { return len(e) > 5 && e[:5] == "text:" }
func isBin(e string) bool  { return len(e) > 4 && e[:4] == "bin:" }

// TestConnection_SendFrame_BeforeOpen_Error 未 open 时发送返回错误。
//
// 发现背景：防御性测试——answerer 侧 OnConnection 回调在 DataChannel open
// 之前触发（handleOffer 立即回调），上层若在此时发送必须得到明确错误
// 而不是静默丢失或 panic。
func TestConnection_SendFrame_BeforeOpen_Error(t *testing.T) {
	p, _ := newTestPeer()
	c, _ := newTestConn(p, "c1") // 未 openFake
	assert.Error(t, c.SendFrame(map[string]any{"type": "data"}, []byte{1}))
	assert.Error(t, c.SendJSON(map[string]any{"type": "x"}))
	assert.Error(t, c.Send([]byte{1}))
}

// TestConnection_Close_Idempotent 回归：Close 重复调用不 panic、Done 只触发一次。
//
// 发现背景：代码审阅——Connection.Close 由多个触发源并发调用（ICE 状态回调、
// 远端 dc 关闭、上层主动、Peer.Close 级联），closeOnce 必须保证 onClose 只
// 触发一次（上层 pending 请求依赖它精确清理）。
func TestConnection_Close_Idempotent(t *testing.T) {
	p, _ := newTestPeer()
	c, _ := newTestConn(p, "c1")

	closed := 0
	c.OnClose(func(*Connection) { closed++ })
	c.Close()
	c.Close()
	c.Close()

	assert.Equal(t, 1, closed, "onClose 只应触发一次")
	select {
	case <-c.Done():
	default:
		t.Fatal("Done 应已关闭")
	}
	// 已从注册表移除
	p.mu.Lock()
	_, ok := p.conns["c1"]
	p.mu.Unlock()
	assert.False(t, ok)
}

// TestConnection_RemoteClose_CleansUp 回归：远端关闭 dc 必须触发本端清理
// （曾未接线 pionChannel.OnClose，连接悬挂到 ICE 超时兜底）。
//
// 发现背景：代码审阅——DataChannel 接口有 OnClose 但 pion 适配层从未接线，
// 对端主动关 dc 后本端连接悬挂数秒~分钟（等 ICE disconnected 兜底），
// 上层 pending 请求只能靠 5 分钟超时释放。修复：attach 时 dc.OnClose → c.Close()。
func TestConnection_RemoteClose_CleansUp(t *testing.T) {
	p, _ := newTestPeer()
	c, dc := newTestConn(p, "c1")
	openFake(dc)

	// 模拟远端关闭：触发 dc.OnClose
	require.NotNil(t, dc.onCls)
	dc.onCls()

	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("远端关闭后本端未清理")
	}
}

// TestConnection_OnMessage_RoutesFrames 文本/二进制帧按类型回调。
//
// 发现背景：防御性测试——帧类型区分是协议根基（见 SendJSON/Send 测试），
// 验证接收侧同样按 IsText 正确分流（service 层 bindConn 依赖此行为）。
func TestConnection_OnMessage_RoutesFrames(t *testing.T) {
	p, _ := newTestPeer()
	c, dc := newTestConn(p, "c1")
	openFake(dc)

	var gotText, gotBin bool
	c.OnMessage(func(f Frame) {
		if f.IsText {
			gotText = string(f.Data) == "hello"
		} else {
			gotBin = len(f.Data) == 3
		}
	})
	require.NotNil(t, dc.onMsg)
	dc.onMsg(Frame{IsText: true, Data: []byte("hello")})
	dc.onMsg(Frame{IsText: false, Data: []byte{1, 2, 3}})
	assert.True(t, gotText)
	assert.True(t, gotBin)
}

// TestConnection_CallbackRegistration_Concurrent 回归：OnOpen/OnMessage/OnClose
// 的注册（业务 goroutine 调用 setter）与 attach 回调触发（pion 回调
// goroutine 读快照）并发执行——handlerMu 保护，-race 下验证无竞态。
//
// 发现背景：-race 集成测试连跑必挂（自托管信令 + 同机 WebRTC 时序快，
// connectLoop 的 OnOpen 注册与 DataChannel open 回调竞争读写 c.onOpen）；
// 真实网络下同样存在（时序慢不易触发）。修复：handlerMu 保护三字段，
// 触发侧取快照再回调。
func TestConnection_CallbackRegistration_Concurrent(t *testing.T) {
	p, _ := newTestPeer()
	c, dc := newTestConn(p, "c1")
	openFake(dc)

	const rounds = 50
	var wg sync.WaitGroup
	for i := 0; i < rounds; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			c.OnOpen(func(*Connection) {})
			c.OnMessage(func(Frame) {})
			c.OnClose(func(*Connection) {})
		}()
		go func() {
			defer wg.Done()
			// 触发 attach 注册的闭包（内部读快照，与上面的 setter 竞争）
			if dc.onOpen != nil {
				dc.onOpen()
			}
			if dc.onMsg != nil {
				dc.onMsg(Frame{IsText: true, Data: []byte("x")})
			}
			if dc.onCls != nil {
				dc.onCls()
			}
		}()
	}
	wg.Wait()

	// 快照读不破坏注册语义：触发后仍能收到最新注册的回调
	var got bool
	c.OnMessage(func(f Frame) { got = string(f.Data) == "y" })
	dc.onMsg(Frame{IsText: true, Data: []byte("y")})
	assert.True(t, got, "并发后注册的回调必须仍生效")
}
