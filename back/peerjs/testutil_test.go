package peerjs

import (
	"context"
	"sync"
)

// fakeSignaller in-memory signaling: no network, messages can be injected to drive Peer.route.
// Purpose: protocol routing/connection lifecycle tests do not depend on public cloud signaling.
// Note: Send may be called concurrently by pion ICE gather callback goroutines; sent needs a lock.
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
// Done returns the disconnect notification (tests can manually trigger to simulate signaling disconnection).
func (f *fakeSignaller) Done() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.done == nil {
		f.done = make(chan struct{})
	}
	return f.done
}
func (f *fakeSignaller) Close() error { return nil }

// disconnect simulates signaling network disconnection (used by H7 reconnect regression tests).
func (f *fakeSignaller) disconnect() {
	f.mu.Lock()
	if f.done == nil {
		f.done = make(chan struct{})
	}
	f.once.Do(func() { close(f.done) })
	f.mu.Unlock()
}

// inject simulates the signaling server forwarding a message to this peer.
func (f *fakeSignaller) inject(m Message) {
	f.mu.Lock()
	h := f.handler
	f.mu.Unlock()
	if h != nil {
		_ = h(m)
	}
}

// sentCount returns the count of sent messages of a specific type (for test assertions).
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

// sentSnapshot returns a locked snapshot of sent messages (iteration must use this method --
// pion ICE gather callback goroutines may still be writing to sent after the test ends).
func (f *fakeSignaller) sentSnapshot() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Message, len(f.sent))
	copy(out, f.sent)
	return out
}

// fakeDC in-memory DataChannel: records send sequences, can manually trigger open/message/close.
// Purpose: frame type/atomicity/lifecycle/flow control tests.
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

// setBuffered simulates peer consumption/backlog (test helper, with lock).
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

// emitLow manually triggers a low-water event (simulating peer consumption).
func (f *fakeDC) emitLow() {
	if f.onLow != nil {
		f.onLow()
	}
}

// newTestPeer constructs a Peer using fakeSignaller.
func newTestPeer() (*Peer, *fakeSignaller) {
	f := &fakeSignaller{id: "test-node"}
	p := NewPeerWithSignaller(f)
	return p, f
}

// newTestConn constructs a Connection bound to a fakeDC (not through newConnection, avoiding real pion).
// Note: must initialize lowWater just like newConnection (a nil channel causes flow control to block permanently).
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

// openFake simulates DataChannel open.
func openFake(f *fakeDC) {
	f.opened = true
	if f.onOpen != nil {
		f.onOpen()
	}
}
