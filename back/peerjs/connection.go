package peerjs

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

// defaultBufferLowThreshold is the send-buffer low-water threshold.
// SendFrame has built-in flow control: when bufferedAmount exceeds the threshold, it waits for the low-water event before sending the next chunk.
// Pitfall: pion's OnBufferedAmountLow is a replacement-style callback — if each concurrent sender registers its own,
// only the last registrant receives the event and the others dead-wait (this once caused concurrent serveFile to hang).
// Therefore the callback must be registered once at attach time (a single global instance), with waiting funneled through the lowWater channel.
const defaultBufferLowThreshold = 512 * 1024

// Connection represents a WebRTC DataConnection (actively initiated or passively received).
// SDP exchange and ICE candidate exchange happen via the signaling server; the data plane is a DataChannel.
//
// Extensibility: the data plane depends on the DataChannel interface (transport.go), not bound to specific pion types;
// the business frame protocol (verb) is defined by the upper layer; this type provides only transport primitives.
type Connection struct {
	ID      string // connectionId (signaling message routing key)
	PeerID  string // remote peer id
	Label   string
	Offered bool // whether this end is the offerer

	// M9: dc is written lock-free during attach (pion OnDataChannel callback / after local CreateDataChannel),
	// while Open/Send/SendFrame/Close read it concurrently from arbitrary goroutines —
	// data race (guaranteed by -race, at extreme reading nil half-initialized). dcMu protects dc reads/writes;
	// attach is called only once, so the lock overhead is negligible.
	dcMu sync.RWMutex
	pc   *webrtc.PeerConnection
	dc   DataChannel
	ice  []webrtc.ICEServer

	sendMu sync.Mutex // ensures data header and binary chunk are sent consecutively (SendFrame)

	lowWater chan struct{} // low-water event broadcast (registered once at attach, capacity 1 to prevent accumulation)

	peer *Peer

	// handlerMu protects onOpen/onMessage/onClose: the registrant (business goroutine calling
	// OnOpen/OnMessage/OnClose) and the trigger (pion callback closure registered during attach,
	// read on the PC goroutine) are concurrent — a data race guaranteed by -race (discovery context:
	// self-hosted integration tests under -race always fail; real-world network timing is slower and
	// harder to expose). Callback bodies are invoked directly; closures only take a snapshot.
	handlerMu sync.Mutex
	onOpen    func(*Connection)
	onMessage func(Frame)
	onClose   func(*Connection)

	closeOnce sync.Once
	done      chan struct{}
}

// Done returns the connection-close notification (triggered on Close or peer disconnect).
func (c *Connection) Done() <-chan struct{} { return c.done }

// OnOpen registers the connection-ready (DataChannel open) callback.
func (c *Connection) OnOpen(f func(*Connection)) {
	c.handlerMu.Lock()
	c.onOpen = f
	c.handlerMu.Unlock()
}

// OnMessage registers the data message callback (Frame: text/binary frame).
func (c *Connection) OnMessage(f func(Frame)) {
	c.handlerMu.Lock()
	c.onMessage = f
	c.handlerMu.Unlock()
}

// OnClose registers the connection-close callback.
func (c *Connection) OnClose(f func(*Connection)) {
	c.handlerMu.Lock()
	c.onClose = f
	c.handlerMu.Unlock()
}

// Open returns whether the DataChannel is ready.
func (c *Connection) Open() bool {
	c.dcMu.RLock()
	defer c.dcMu.RUnlock()
	return c.dc != nil && c.dc.Open()
}

// Send sends binary data.
// Note: pion's dc.Send([]byte) sends a binary frame (SCTP PPID 53),
// distinguishable from a text frame (PPID 51) on the receiver side — the protocol relies on this distinction to route "data chunks vs. control headers."
func (c *Connection) Send(data []byte) error {
	c.dcMu.RLock()
	dc := c.dc
	c.dcMu.RUnlock()
	if dc == nil || !dc.Open() {
		return fmt.Errorf("peerjs: connection not open")
	}
	return dc.Send(data)
}

// SendText sends a text frame (for JSON control headers).
// Pitfall: must use a text frame. If a JSON header is sent via Send([]byte), the peer (peerjs browser side
// / this library's peer) will misidentify the header as a binary data chunk and discard/mismatch it.
func (c *Connection) SendText(s string) error {
	c.dcMu.RLock()
	dc := c.dc
	c.dcMu.RUnlock()
	if dc == nil || !dc.Open() {
		return fmt.Errorf("peerjs: connection not open")
	}
	return dc.SendText(s)
}

// SendJSON sends a JSON text message (equivalent to SendText(json(v))).
func (c *Connection) SendJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.SendText(string(b))
}

// SendFrame atomically sends a "JSON header + binary body" frame (header and body sent consecutively), with built-in write-buffer flow control.
// Why: the receiver routes data using a state machine of "data header → immediately following binary chunk";
// if multiple goroutines send concurrently and headers/chunks interleave, chunks attach to the wrong request.
// sendMu guarantees atomic on-wire delivery of one frame's header+body, safe for concurrent multi-request use; it also acts as a backpressure gate —
// when the peer consumes slowly, all senders queue here (bounded wait; immediate exit on connection close).
// Pitfall: flow-control events rely on the global callback registered at attach time (lowWater); registering here
// (replacement-style callbacks would be overwritten by concurrent senders → dead-wait) is not allowed.
func (c *Connection) SendFrame(header any, body []byte) error {
	hb, err := json.Marshal(header)
	if err != nil {
		return err
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	// M9: hold sendMu and read a dc snapshot once; subsequent code uses the local variable (avoid locking each time)
	c.dcMu.RLock()
	dc := c.dc
	c.dcMu.RUnlock()
	if dc == nil || !dc.Open() {
		return fmt.Errorf("peerjs: connection not open")
	}
	if err := dc.SendText(string(hb)); err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	// Low-severity 1 fix: add an overall timeout ceiling (30s) to the flow-control wait — previously only done/lowWater were waited on,
	// relying on ICE disconnected (~30s) as a backstop for slow consumers. Bounded but not fast; now explicitly capped.
	flowWait := time.NewTimer(30 * time.Second)
	defer flowWait.Stop()
	for dc.BufferedAmount() > defaultBufferLowThreshold {
		select {
		case <-c.lowWater:
		case <-c.done: // connection closed: exit immediately, don't hang the caller
			return fmt.Errorf("peerjs: connection closed during flow control")
		case <-flowWait.C: // slow consumer: return timeout error (upper layer will disconnect/retry)
			return fmt.Errorf("peerjs: flow control timeout (slow consumer)")
		}
	}
	return dc.Send(body)
}

// DataChannel returns the underlying data channel (advanced usage: flow control, close, etc.).
func (c *Connection) DataChannel() DataChannel {
	c.dcMu.RLock()
	defer c.dcMu.RUnlock()
	return c.dc
}

// Close closes the connection and deregisters from the signaling layer.
// M9: Close may be triggered by pion internal callbacks (OnClose in OnDataChannel), concurrent with attach
// writing dc — reading dc requires holding dcMu.
func (c *Connection) Close() {
	c.closeOnce.Do(func() {
		if c.pc != nil {
			_ = c.pc.Close()
		}
		c.dcMu.RLock()
		dc := c.dc
		c.dcMu.RUnlock()
		if dc != nil {
			dc.Close()
		}
		c.peer.forgetConnection(c.ID)
		close(c.done)
		c.handlerMu.Lock()
		h := c.onClose
		c.handlerMu.Unlock()
		if h != nil {
			h(c)
		}
	})
}

// handleMessage handles signaling messages related to this connection (ANSWER/CANDIDATE).
// SDP/candidate parsing errors are silently ignored: the peer may send out-of-order or stale candidates;
// a failure simply means this round of negotiation failed; ICE state callbacks handle final cleanup.
func (c *Connection) handleMessage(m Message) {
	switch m.Type {
	case MsgAnswer:
		var payload AnswerPayload
		if err := json.Unmarshal(m.Payload, &payload); err != nil || payload.SDP == nil {
			return
		}
		_ = c.pc.SetRemoteDescription(*payload.SDP)
	case MsgCandidate:
		var payload CandidatePayload
		if err := json.Unmarshal(m.Payload, &payload); err != nil {
			return
		}
		_ = c.pc.AddICECandidate(payload.Candidate)
	}
}

// newConnection creates a connection (when offered, establishes PC and DataChannel immediately).
// If connID is empty, a new ID is generated; the answerer must reuse the connectionId provided by the offerer.
// Pitfall: previously the answerer generated a new ID, causing ANSWER messages to fail signaling routing (by connectionId),
// leaving ICE stuck in checking forever and DataChannel never opening —
// the peerjs protocol specifies that connectionId is defined by the offerer and shared by both sides.
func (p *Peer) newConnection(dst, label string, offered bool, iceServers []webrtc.ICEServer, connID string) (*Connection, error) {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{ICEServers: iceServers})
	if err != nil {
		return nil, fmt.Errorf("peerjs: new pc: %w", err)
	}
	if connID == "" {
		connID = randHex(16)
	}
	conn := &Connection{
		ID:       connID,
		PeerID:   dst,
		Label:    label,
		Offered:  offered,
		pc:       pc,
		peer:     p,
		done:     make(chan struct{}),
		lowWater: make(chan struct{}, 1),
	}
	// On ICE failure/disconnect, clean up the connection immediately to avoid leaks (same behavior as peerjs-client negotiator)
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		switch state {
		case webrtc.ICEConnectionStateClosed,
			webrtc.ICEConnectionStateFailed,
			webrtc.ICEConnectionStateDisconnected:
			conn.Close()
		}
	})
	// Collect local ICE candidates and forward them via signaling (equivalent to peerjs negotiator.onicecandidate).
	// Pitfall: pion does not send candidates automatically; manual OnICECandidate + signaling CANDIDATE is required,
	// otherwise both sides stay in checking and never connect.
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		payload := CandidatePayload{
			Candidate:    c.ToJSON(),
			Type:         ConnData,
			ConnectionID: conn.ID,
		}
		_ = p.Send(NewMessage(MsgCandidate, dst, payload))
	})
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		conn.attach(newPionChannel(dc))
	})

	p.registerConnection(conn)

	if offered {
		ordered := true
		dc, err := pc.CreateDataChannel(label, &webrtc.DataChannelInit{Ordered: &ordered})
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("peerjs: create dc: %w", err)
		}
		conn.attach(newPionChannel(dc))
		if err := conn.makeOffer(); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return conn, nil
}

// attach binds the data channel and sets open/message/flow-control callbacks.
// The offerer side calls this immediately after CreateDataChannel (state is connecting);
// the answerer side calls it when the peer's offer triggers pc.OnDataChannel.
func (c *Connection) attach(dc DataChannel) {
	// M9: dc write holds the lock (Close/Send/Open read concurrently; pion's OnDataChannel callback
	// runs on the PC goroutine and may race with the first Send).
	c.dcMu.Lock()
	c.dc = dc
	c.dcMu.Unlock()
	// Flow-control callback registered globally once (replacement-style callback; multiple senders each registering would overwrite each other → dead-wait)
	dc.SetBufferedAmountLowThreshold(defaultBufferLowThreshold)
	dc.OnBufferedAmountLow(func() {
		select {
		case c.lowWater <- struct{}{}:
		default:
		}
	})
	dc.OnOpen(func() {
		// Snapshot then callback: onOpen may be registered later than the open event (still nil at attach time);
		// reading a snapshot only avoids concurrent write with OnOpen registration (handlerMu protects).
		c.handlerMu.Lock()
		h := c.onOpen
		c.handlerMu.Unlock()
		if h != nil {
			h(c)
		}
	})
	dc.OnMessage(func(f Frame) {
		c.handlerMu.Lock()
		h := c.onMessage
		c.handlerMu.Unlock()
		if h != nil {
			h(f)
		}
	})
	// Clean up this end when the remote actively closes the dc (Close is idempotent): otherwise this end's connection hangs,
	// relying on ICE disconnected as a backstop (seconds-to-minutes, too slow).
	dc.OnClose(func() {
		c.Close()
	})
}

// makeOffer creates and sends an OFFER (equivalent to peerjs negotiator._makeOffer).
func (c *Connection) makeOffer() error {
	offer, err := c.pc.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("peerjs: create offer: %w", err)
	}
	if err := c.pc.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("peerjs: set local offer: %w", err)
	}
	payload := OfferPayload{
		SDP:           &offer,
		Type:          ConnData,
		ConnectionID:  c.ID,
		Label:         c.Label,
		Reliable:      true,
		Serialization: "raw",
	}
	return c.peer.Send(NewMessage(MsgOffer, c.PeerID, payload))
}

// handleOffer handles a remote-initiated OFFER: sets the remote SDP and replies with an ANSWER.
func (c *Connection) handleOffer(sdp *webrtc.SessionDescription) error {
	if err := c.pc.SetRemoteDescription(*sdp); err != nil {
		return fmt.Errorf("peerjs: set remote offer: %w", err)
	}
	answer, err := c.pc.CreateAnswer(nil)
	if err != nil {
		return fmt.Errorf("peerjs: create answer: %w", err)
	}
	if err := c.pc.SetLocalDescription(answer); err != nil {
		return fmt.Errorf("peerjs: set local answer: %w", err)
	}
	payload := AnswerPayload{SDP: &answer, Type: ConnData, ConnectionID: c.ID}
	return c.peer.Send(NewMessage(MsgAnswer, c.PeerID, payload))
}
