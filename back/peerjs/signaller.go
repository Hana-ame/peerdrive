package peerjs

import "context"

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

// MessageHandler is a signaling message callback.
// Deprecated: users do not need to interact with it directly — message handling is taken over by Peer's internal router
// (OFFER/ANSWER/CANDIDATE/LEAVE/EXPIRE are all handled internally). This type is retained only as internal
// plumbing between the Signaller implementer and the framework.
type MessageHandler func(m Message) error

// SignallerFactory is the entry point for creating custom signaling (optional parameter to NewPeer).
type SignallerFactory func() Signaller
