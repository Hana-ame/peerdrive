// Package signalling implements the PeerJS-compatible signaling transport:
// node registration over HTTP, the WebSocket handshake with the signaling
// server, frame read/write, heartbeat keep-alive, and disconnect notification.
//
// Module positioning — the signaling plane is split three ways:
//
//   - github.com/Hana-ame/go-signalframe: the wire frame itself. Message
//     construction, JSON serialization, and the write-lock + write-deadline
//     sender that wraps a JSON-writing socket. Zero third-party deps.
//   - github.com/Hana-ame/go-peerjs/signalling (this package): the transport.
//     It owns the socket lifecycle — retrieveID over HTTP, the ws/wss
//     handshake, the read loop, the heartbeat loop, the disconnect signal.
//   - github.com/Hana-ame/go-peerjs (peer / connection): the WebRTC data
//     plane. OFFER/ANSWER/CANDIDATE routing and DataChannel setup.
//
// Keeping this package free of pion/webrtc is deliberate: a signaling client
// is useful on its own (a relay, a panel bridge, a presence client) and must
// not drag the WebRTC stack in with it. ICE servers are a WebRTC concern and
// deliberately do not appear in Options.
//
// The PeerJS wire protocol is implemented byte-for-byte as before this split:
// URL shape, query parameters, frame JSON, heartbeat framing, and the exact
// error strings ("peerjs: retrieve id: …", "peerjs: dial …", "peerjs: not
// connected") are pinned by the contract tests in contract_test.go.
package signalling

import (
	"context"

	"github.com/Hana-ame/go-signalframe"
)

// Message is the generic message transported between the signaling server and a peer.
// Alias of signalframe.Message — the single wire-format definition.
type Message = signalframe.Message

// MessageHandler is a signaling message callback.
// Deprecated: users do not need to interact with it directly — message handling is taken over by Peer's internal router
// (OFFER/ANSWER/CANDIDATE/LEAVE/EXPIRE are all handled internally). This type is retained only as internal
// plumbing between the Signaller implementer and the framework.
type MessageHandler func(m Message) error

// Signaller is a signaling channel abstraction: responsible for node registration and message send/receive.
// Extensibility: the current implementation uses the PeerJS public cloud protocol (peerJSSignaller); it can later be replaced with
// self-hosted peerjs-server, MQTT room signaling, etc., without modifying the Peer/Connection layer.
//
// Note: when implementing a custom Signaller, message dispatch is not your concern — OnMessage is injected
// internally by the framework (called when Peer is constructed). The implementer only needs to call the
// injected callback after receiving a signaling message (see peerJSSignaller.readLoop for the pattern).
type Signaller interface {
	// Dial registers the node and establishes the signaling connection; ctx cancellation aborts.
	Dial(ctx context.Context) error
	// ID returns the node ID (valid after Dial succeeds; the server assigns one if ID is not specified).
	ID() string
	// Send sends a message to the signaling server.
	Send(m Message) error
	// OnMessage registers a signaling message callback.
	// Deprecated: for framework internal use only (Peer injects the message router). Users do not need to call it;
	// when implementing a custom Signaller, simply pass received messages to the injected callback.
	OnMessage(h MessageHandler)
	// Done returns the signaling connection disconnect notification (closed when readLoop exits due to network error/peer close).
	// H7 fix: previously readLoop exited silently on error and connected=false, but there was no signal
	// to notify the upper layer — startLoop only selects on ctx/closed (two signals that never fire), so a public WS
	// dropping once would leave the node permanently deaf until restart. The reconnect loop depends on this channel to trigger a full reconnect.
	Done() <-chan struct{}
	// Close closes the signaling connection.
	Close() error
}

// SignallerFactory is the entry point for creating custom signaling (optional parameter to NewPeer).
type SignallerFactory func() Signaller
