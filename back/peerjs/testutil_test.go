package peerjs

import (
	"context"
	"sync"
)

// fakeSignaller 内存版信令：无网络，可注入消息驱动 Peer.route。
// 用途：协议路由/连接生命周期测试不依赖公共云信令。
// 注意：Send 可能被 pion ICE gather 回调 goroutine 并发调用，sent 需加锁。
type fakeSignaller struct {
	mu      sync.Mutex
	id      string
	sent    []Message
	handler MessageHandler
	done    chan struct{}
	once    sync.Once
}

func (f *fakeSignaller) Dial(context.Context) error { return nil }
func (f *fakeSignaller) ID() string                 { return f.id }
func (f *fakeSignaller) Send(m Message) error {
	f.mu.Lock()
	f.sent = append(f.sent, m)
	f.mu.Unlock()
	return nil
}
func (f *fakeSignaller) OnMessage(h MessageHandler) {
	f.mu.Lock()
	f.handler = h
	f.mu.Unlock()
}
// Done 返回断线通知（测试可手动触发模拟信令掉线）。
func (f *fakeSignaller) Done() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.done == nil {
		f.done = make(chan struct{})
	}
	return f.done
}
func (f *fakeSignaller) Close() error { return nil }

// disconnect 模拟信令网络断开（H7 重连回归测试用）。
func (f *fakeSignaller) disconnect() {
	f.mu.Lock()
	if f.done == nil {
		f.done = make(chan struct{})
	}
	f.once.Do(func() { close(f.done) })
	f.mu.Unlock()
}

// inject 模拟信令服务器转发消息给本 peer。
func (f *fakeSignaller) inject(m Message) {
	f.mu.Lock()
	h := f.handler
	f.mu.Unlock()
	if h != nil {
		_ = h(m)
	}
}

// sentCount 返回发送消息中指定类型的数量（测试断言用）。
func (f *fakeSignaller) sentCount(t MessageType) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.sent {
		if m.Type == t {
			n++
		}
	}
	return n
}

// sentSnapshot 返回已发送消息的锁内快照（遍历需用本方法——pion ICE
// gather 回调 goroutine 可能在测试结束后仍在写 sent）。
func (f *fakeSignaller) sentSnapshot() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Message, len(f.sent))
	copy(out, f.sent)
	return out
}

// fakeDC 内存版 DataChannel：记录发送序列，可手动触发 open/message/close。
// 用途：帧类型/原子性/生命周期/流控测试。
type fakeDC struct {
	mu       sync.Mutex
	events   []string // "text:xxx" / "bin:xxx"
	onOpen   func()
	onMsg    func(Frame)
	onCls    func()
	onLow    func()
	opened   bool
	buffered uint64
}

func newFakeDC() *fakeDC { return &fakeDC{} }

func (f *fakeDC) SendText(s string) error {
	f.events = append(f.events, "text:"+s)
	return nil
}
func (f *fakeDC) Send(b []byte) error {
	f.events = append(f.events, "bin:"+string(b))
	return nil
}
func (f *fakeDC) OnOpen(h func())         { f.onOpen = h }
func (f *fakeDC) OnMessage(h func(Frame)) { f.onMsg = h }
func (f *fakeDC) OnClose(h func())        { f.onCls = h }
func (f *fakeDC) Open() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opened
}
func (f *fakeDC) BufferedAmount() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buffered
}

// setBuffered 模拟对端消费/积压（测试辅助，带锁）。
func (f *fakeDC) setBuffered(n uint64) {
	f.mu.Lock()
	f.buffered = n
	f.mu.Unlock()
}
func (f *fakeDC) SetBufferedAmountLowThreshold(uint64) {}
func (f *fakeDC) OnBufferedAmountLow(h func())         { f.onLow = h }
func (f *fakeDC) Close() {
	f.mu.Lock()
	f.opened = false
	f.mu.Unlock()
}

// emitLow 手动触发低水位事件（模拟对端消费）。
func (f *fakeDC) emitLow() {
	if f.onLow != nil {
		f.onLow()
	}
}

// newTestPeer 构造使用 fakeSignaller 的 Peer。
func newTestPeer() (*Peer, *fakeSignaller) {
	f := &fakeSignaller{id: "test-node"}
	p := NewPeerWithSignaller(f)
	return p, f
}

// newTestConn 构造绑定 fakeDC 的 Connection（不经 newConnection，避免真实 pion）。
// 注意：必须与 newConnection 一样初始化 lowWater（nil channel 会让流控永久阻塞）。
func newTestConn(p *Peer, id string) (*Connection, *fakeDC) {
	f := newFakeDC()
	c := &Connection{
		ID:       id,
		PeerID:   "remote-" + id,
		peer:     p,
		done:     make(chan struct{}),
		lowWater: make(chan struct{}, 1),
	}
	c.attach(f)
	p.registerConnection(c)
	return c, f
}

// openFake 模拟 DataChannel open。
func openFake(f *fakeDC) {
	f.opened = true
	if f.onOpen != nil {
		f.onOpen()
	}
}
