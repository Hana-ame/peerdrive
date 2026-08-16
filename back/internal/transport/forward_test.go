package transport

// forward_test.go：forward v2（PeerJS DataChannel 端口转发）握手与数据透传测试。
//
// 覆盖（发现背景标注）：
//   - 握手全流程 + 数据双向透传（服务端侧：echo TCP 服务 ↔ 隧道）
//   - 坏 key / 端口越权 / nonce 重放 → fwd-err（权限控制与密钥交换核心断言）
//   - 客户端 OpenForward 全链路（fakeSession 扮演服务端）
//
// 说明：不走真实 WebRTC/WS——用 fakeSession + bindConn 全链路（含 uploadWorker，
// 转发块经 fwdCh 投递由 worker 写隧道），与文件帧路由共用同一套泵内逻辑。

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	peerjs "github.com/Hana-ame/go-peerjs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startEchoServer 起一个 loopback echo TCP 服务，返回监听端口与关闭函数。
func startEchoServer(t *testing.T, tag string) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				// echo：收到什么原样回什么（收齐一行再回，断言简单）
				buf := make([]byte, 4096)
				for {
					n, err := conn.Read(buf)
					if n > 0 {
						// 前缀 tag 便于断言区分多个服务
						resp := append([]byte(tag+":"), buf[:n]...)
						if _, werr := conn.Write(resp); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, func() { ln.Close() }
}

// bindFakeServer 把 fakeSession 全链路绑定到 service（bindConn + conns/pending 注册），
// 返回 st 便于断言隧道状态。
func bindFakeServer(t *testing.T, svc *PeerJSService, sess *fakeSession) *connState {
	t.Helper()
	svc.mu.Lock()
	svc.conns[sess.id] = sess
	svc.mu.Unlock()
	svc.bindConn(sess)
	st := svc.stateFor(sess)
	require.NotNil(t, st)
	return st
}

// waitFrameType 轮询等待最近一帧类型为 want（bindConn 分派是异步 goroutine，
// feed 后不能立即断言）。超时返回空串。
func waitFrameType(t *testing.T, sess *fakeSession, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		frames := sess.sentFrames()
		if len(frames) > 0 {
			last := frames[len(frames)-1]
			if t0, _ := last.header["type"].(string); t0 == want {
				return last.header
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待帧 %q 超时（当前最近帧: %v）", want, func() []string {
		out := []string{}
		for _, fr := range sess.sentFrames() {
			if tt, ok := fr.header["type"].(string); ok {
				out = append(out, tt)
			}
		}
		return out
	}())
	return nil
}

// fwdHMAC 计算客户端侧应答（与服务端验证逻辑对称）。
func fwdHMAC(key, nonceHex string) string {
	nonce, _ := hex.DecodeString(nonceHex)
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(nonce)
	return hex.EncodeToString(mac.Sum(nil))
}

// TestForward_HandshakeAndData 服务端握手全流程 + 双向数据透传：
//  1. 收到 fwd-open → 回 fwd-challenge（带 nonce）
//  2. 收到合法 fwd-auth → 回 fwd-ok，隧道建立（服务端 dial 到 echo 服务）
//  3. 本地 TCP → 隧道（经 pump 发 fwd-data 头+块）→ echo 服务
//  4. echo 回包 → 隧道另一端 TCP 收到
func TestForward_HandshakeAndData(t *testing.T) {
	svc := newTestPeerJSService(t)
	echoPort, stopEcho := startEchoServer(t, "srv")
	defer stopEcho()
	svc.SetForwardRules(map[string][]int{"testkey": {echoPort}})

	sess := &fakeSession{id: "client"}
	st := bindFakeServer(t, svc, sess)

	// 1. fwd-open → fwd-challenge
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-open","port":` + fmt.Sprint(echoPort) + `,"reqId":"h1"}`)})
	ch := waitFrameType(t, sess, "fwd-challenge")
	nonce, _ := ch["nonce"].(string)
	require.NotEmpty(t, nonce)

	// 2. fwd-auth → fwd-ok
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-auth","hmac":"` + fwdHMAC("testkey", nonce) + `","reqId":"h1"}`)})
	waitFrameType(t, sess, "fwd-ok")
	st.mu.Lock()
	require.NotNil(t, st.fwd, "隧道应已建立")
	require.Equal(t, echoPort, st.fwd.port)
	st.mu.Unlock()

	// 3. 客户端方向数据：注入 fwd-data 头+块 → 服务端 dial 的 TCP 应收到
	// （bindConn 泵内路由：头帧标 pending → 二进制块投 fwdCh → worker 写 TCP）
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-data","reqId":"h1"}`)})
	sess.feed(peerjs.Frame{IsText: false, Data: []byte("ping-data")})
	// 4. echo 回程：服务端 pump 读 TCP → SendFrame(fwd-data) 给 fakeSession
	deadline := time.Now().Add(3 * time.Second)
	var gotBody []byte
	for time.Now().Before(deadline) {
		frs := sess.sentFrames()
		gotBody = nil
		for _, fr := range frs {
			if fr.header["type"] == "fwd-data" && len(fr.body) > 0 {
				gotBody = fr.body
			}
		}
		if strings.Contains(string(gotBody), "srv:ping-data") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Equal(t, "srv:ping-data", string(gotBody), "echo 回包应经隧道回到客户端侧")
}

// TestForward_AuthRejected 权限控制：坏 key / 端口越权 / nonce 重放全部 fwd-err，
// 且不建立隧道（规则细节不泄露：三种失败报文不区分具体原因以外的信息）。
func TestForward_AuthRejected(t *testing.T) {
	svc := newTestPeerJSService(t)
	echoPort, stopEcho := startEchoServer(t, "srv")
	defer stopEcho()
	svc.SetForwardRules(map[string][]int{"goodkey": {echoPort}})
	sess := &fakeSession{id: "client"}
	bindFakeServer(t, svc, sess)

	openAndGetNonce := func(t *testing.T, reqID string) string {
		sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-open","port":` + fmt.Sprint(echoPort) + `,"reqId":"` + reqID + `"}`)})
		ch := waitFrameType(t, sess, "fwd-challenge")
		nonce, _ := ch["nonce"].(string)
		return nonce
	}
	// 坏 key：HMAC 用错误密钥 → fwd-err
	nonce1 := openAndGetNonce(t, "badk")
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-auth","hmac":"` + fwdHMAC("wrongkey", nonce1) + `","reqId":"badk"}`)})
	waitFrameType(t, sess, "fwd-err")

	// 端口越权单独在 TestForward_PortNotAuthorized 覆盖（本测试 open 固定用合法端口）

	// 重放：同一 nonce 提交两次——第一次合法建隧道，第二次必须 fwd-err
	nonce3 := openAndGetNonce(t, "replay")
	hm := fwdHMAC("goodkey", nonce3)
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-auth","hmac":"` + hm + `","reqId":"replay"}`)})
	waitFrameType(t, sess, "fwd-ok")
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-auth","hmac":"` + hm + `","reqId":"replay"}`)})
	last := waitFrameType(t, sess, "fwd-err")
	msg, _ := last["msg"].(string)
	assert.Equal(t, "no challenge", msg, "nonce 重放必须拒绝（已消费）")

	// 合法请求确实建了隧道（重放被拒的前提成立）
	st := svc.stateFor(sess)
	st.mu.Lock()
	tunneled := st.fwd != nil
	st.mu.Unlock()
	assert.True(t, tunneled, "合法的 replay 请求建了隧道（后续重放被拒）")
}

// TestForward_PortNotAuthorized 端口越权单独验证（上面那次实际是同端口合法，
// 补一个真正越权端口的断言）。
func TestForward_PortNotAuthorized(t *testing.T) {
	svc := newTestPeerJSService(t)
	echoPort, stopEcho := startEchoServer(t, "srv")
	defer stopEcho()
	svc.SetForwardRules(map[string][]int{"goodkey": {echoPort}})
	sess := &fakeSession{id: "client"}
	bindFakeServer(t, svc, sess)

	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-open","port":65000,"reqId":"p1"}`)})
	ch := waitFrameType(t, sess, "fwd-challenge")
	nonce, _ := ch["nonce"].(string)
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-auth","hmac":"` + fwdHMAC("goodkey", nonce) + `","reqId":"p1"}`)})
	last := waitFrameType(t, sess, "fwd-err")
	msg, _ := last["msg"].(string)
	assert.Equal(t, "port not authorized", msg)
}

// TestOpenForward_ClientSide 客户端 OpenForward 全链路：fakeSession 扮演服务端。
// 验证：握手（open→challenge→auth→ok）→ 写隧道 → 对端收到 fwd-data；
// 对端注入 fwd-data 块 → 调用方读出。
func TestOpenForward_ClientSide(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.SetForwardRules(map[string][]int{})
	sess := &fakeSession{id: "server"}
	st := bindFakeServer(t, svc, sess)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	connCh := make(chan struct {
		c   net.Conn
		err error
	}, 1)
	go func() {
		c, err := svc.OpenForward(ctx, "server", "clientkey", 8080)
		connCh <- struct {
			c   net.Conn
			err error
		}{c, err}
	}()

	// 服务端（fake）收到 fwd-open → 回 challenge
	deadline := time.Now().Add(2 * time.Second)
	var nonce string
	for time.Now().Before(deadline) {
		for _, fr := range sess.sentFrames() {
			if fr.header["type"] == "fwd-open" {
				nonce = "aabbccddeeff00112233445566778899"
				sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-challenge","nonce":"` + nonce + `","reqId":"` + fr.header["reqId"].(string) + `"}`)})
				goto sent
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
sent:
	require.NotEmpty(t, nonce, "OpenForward 应发出 fwd-open")

	// 客户端应发出 fwd-auth（HMAC 用 clientkey）
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, fr := range sess.sentFrames() {
			if fr.header["type"] == "fwd-auth" {
				expected := fwdHMAC("clientkey", nonce)
				assert.Equal(t, expected, fr.header["hmac"], "HMAC 必须用调用方 key 计算")
				// 服务端回 fwd-ok
				sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-ok","reqId":"` + fr.header["reqId"].(string) + `"}`)})
				goto authed
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
authed:

	select {
	case res := <-connCh:
		require.NoError(t, res.err)
		require.NotNil(t, res.c)
		defer res.c.Close()
		// 写隧道 → 对端应收到 fwd-data 头+块
		go res.c.Write([]byte("tunnel-write"))
		deadline := time.Now().Add(2 * time.Second)
		var got []byte
		for time.Now().Before(deadline) {
			for _, fr := range sess.sentFrames() {
				if fr.header["type"] == "fwd-data" && len(fr.body) > 0 {
					got = fr.body
				}
			}
			if string(got) == "tunnel-write" {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		assert.Equal(t, "tunnel-write", string(got), "写隧道的数据应以 fwd-data 帧到达对端")

		// 对端注入 fwd-data → 调用方应读出
		st.mu.Lock()
		fw := st.fwd
		st.mu.Unlock()
		require.NotNil(t, fw)
		sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-data","reqId":"` + fw.reqID + `"}`)})
		sess.feed(peerjs.Frame{IsText: false, Data: []byte("from-server")})
		buf := make([]byte, 64)
		res.c.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := res.c.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, "from-server", string(buf[:n]))
	case <-time.After(3 * time.Second):
		t.Fatal("OpenForward 未在超时内返回")
	}
}

// TestForward_FwdDataWithoutTunnel 防御：无隧道时 fwd-data 头/块静默丢弃不 panic。
// 发现背景：手写协议测试时想到——恶意对端不发握手直接发 fwd-data，泵内路由
// 必须优雅处理（不 panic、不建状态），否则公共信令上可被一行帧打崩。
func TestForward_FwdDataWithoutTunnel(t *testing.T) {
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "evil"}
	bindFakeServer(t, svc, sess)
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-data","reqId":"x"}`)})
	sess.feed(peerjs.Frame{IsText: false, Data: []byte("garbage")})
	// 不 panic 即通过；随后正常握手仍可用
	svc.SetForwardRules(map[string][]int{"k": {1}})
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-open","port":2,"reqId":"y"}`)})
	waitFrameType(t, sess, "fwd-challenge")
}

// TestForward_CloseStream 主动断开（CloseForwardStream）：对端收到 fwd-close、
// 本端隧道槽清空、out 关闭（调用方读侧 EOF）。
func TestForward_CloseStream(t *testing.T) {
	svc := newTestPeerJSService(t)
	echoPort, stopEcho := startEchoServer(t, "srv")
	defer stopEcho()
	svc.SetForwardRules(map[string][]int{"testkey": {echoPort}})
	sess := &fakeSession{id: "client"}
	_ = bindFakeServer(t, svc, sess)

	// 建一条隧道
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-open","port":` + fmt.Sprint(echoPort) + `,"reqId":"c1"}`)})
	ch := waitFrameType(t, sess, "fwd-challenge")
	nonce, _ := ch["nonce"].(string)
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-auth","hmac":"` + fwdHMAC("testkey", nonce) + `","reqId":"c1"}`)})
	waitFrameType(t, sess, "fwd-ok")

	svc.CloseForwardStream("client")
	waitFrameType(t, sess, "fwd-close")
	st := svc.stateFor(sess)
	st.mu.Lock()
	closed := st.fwd == nil
	st.mu.Unlock()
	assert.True(t, closed, "隧道应已清槽")
}

// TestForward_ListAndInfo 管理面：ListForwardStreams 返回隧道快照。
func TestForward_ListAndInfo(t *testing.T) {
	svc := newTestPeerJSService(t)
	echoPort, stopEcho := startEchoServer(t, "srv")
	defer stopEcho()
	svc.SetForwardRules(map[string][]int{"testkey": {echoPort}})
	sess := &fakeSession{id: "client"}
	bindFakeServer(t, svc, sess)
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-open","port":` + fmt.Sprint(echoPort) + `,"reqId":"l1"}`)})
	ch := waitFrameType(t, sess, "fwd-challenge")
	nonce, _ := ch["nonce"].(string)
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-auth","hmac":"` + fwdHMAC("testkey", nonce) + `","reqId":"l1"}`)})
	waitFrameType(t, sess, "fwd-ok")
	time.Sleep(50 * time.Millisecond) // 等隧道建立
	infos := svc.ListForwardStreams()
	require.Len(t, infos, 1)
	assert.Equal(t, "client", infos[0].PeerID)
	assert.Equal(t, echoPort, infos[0].Port)
}

// TestForward_TimeoutNoChallenge 客户端超时：服务端不回 challenge → 错误返回，
// 单槽释放（可再次握手）。
// 发现背景：防御性测试——对端不回包（伪造/宕机）时 OpenForward 必须超时退出，
// 不能永久挂住调用方或占住 fwdHs 单槽。
func TestForward_TimeoutNoChallenge(t *testing.T) {
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "silent"}
	bindFakeServer(t, svc, sess)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := svc.OpenForward(ctx, "silent", "k", 8080)
	require.Error(t, err)
	st := svc.stateFor(sess)
	st.mu.Lock()
	hs := st.fwdHs
	st.mu.Unlock()
	assert.Nil(t, hs, "超时后握手槽必须释放")
	_ = io.EOF
}
