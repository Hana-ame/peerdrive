package transport

import (
	peerjs "github.com/Hana-ame/go-peerjs"
)

// rtcSession adapts *peerjs.Connection to the Session interface.
// Why wrap instead of letting Connection directly implement: Connection.ID field is the
// signaling routing key (connectionId), semantically different from the session identifier
// (remote peer id) and the field name conflicts; the adapter converges the difference at
// the service layer, keeping the peerjs module as a transport primitive.
type rtcSession struct {
	c  *peerjs.Connection
	id string // cached PeerID (Connection.PeerID exported field, read once to avoid ambiguity)
}

func newRTCSession(c *peerjs.Connection) *rtcSession {
	return &rtcSession{c: c, id: c.PeerID}
}

func (r *rtcSession) ID() string { return r.id }

// ConnID returns the connection-level UUID (signaling routing key, both ends see the same
// value) — bindConn dedup for same peer uses it for both-end consistent retention decisions
// (see conn.go dedup comments).
func (r *rtcSession) ConnID() string { return r.c.ID }

func (r *rtcSession) SendJSON(v any) error { return r.c.SendJSON(v) }

func (r *rtcSession) SendFrame(header any, body []byte) error { return r.c.SendFrame(header, body) }

func (r *rtcSession) OnMessage(f func(peerjs.Frame)) { r.c.OnMessage(f) }

func (r *rtcSession) OnClose(f func()) {
	r.c.OnClose(func(*peerjs.Connection) { f() })
}

func (r *rtcSession) Close() { r.c.Close() }

// DataChannel provides the DataChannel for serveFile's write buffer flow control (WSSession
// doesn't have this capability).
func (r *rtcSession) DataChannel() peerjs.DataChannel { return r.c.DataChannel() }
