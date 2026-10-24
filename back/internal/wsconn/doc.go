// Package wsconn is peerdrive's WebSocket transport core: everything between
// the TCP stream and the node's frame protocol.
//
// # Module placement: an internal package, not a separate go.mod
//
// The repo has two precedents for where split-out code goes:
//
//   - internal/hashmap stayed internal (back/internal/hashmap): it has exactly
//     one consumer set, the main module.
//   - signalframe got its own go.mod (back/signalframe, module
//     github.com/Hana-ame/go-signalframe): it was needed by *two independent
//     modules* at once — back/peerjs and back/signalserver — so an internal
//     package could not be imported by both.
//
// wsconn follows the hashmap precedent: its only consumers are
// back/internal/transport (the Session bridge) and back/internal/router (the
// /ws/peer upgrade), both in this module. Making it a separate module would
// add a fourth mirror-sync burden (AGENTS.md: independent modules must be
// mirrored to their own repo and re-tagged on every change) with no consumer
// to justify it. If back/signalserver ever wants the same session/upgrade
// semantics, that is the point to promote it — and the seam here is already
// drawn so the move is a directory move plus one import-path rewrite.
//
// # Scope
//
// Four responsibilities, each its own file:
//
//   - connection establishment — Upgrader + OriginPolicy (upgrader.go)
//   - frame codec — Frame, text/binary opcodes, ReadMessage→Frame (frame.go)
//   - read/write scheduling — Session readLoop, heartbeat, write mutex,
//     read/write deadlines (session.go)
//   - lifecycle — Close / OnClose / read-loop-exit-implies-close (session.go)
//
// # Deliberately out of scope
//
//   - back/peerjs's PeerJS signaling client and back/signalserver's relay
//     server are each already independent modules and each speak the PeerJS
//     wire protocol (OPEN/LEAVE/OFFER/ANSWER/CANDIDATE) over WS. They are a
//     different protocol with a different purpose — node-to-node NAT
//     traversal vs. this node's own admin/data channel — and neither imports
//     wsconn. They do share one piece of code, the message struct + locked
//     JSON writer, which is exactly why signalframe exists.
//   - The frame verbs (req/meta/data/done/err, admin/admin-resp/admin-bin,
//     share/share-resp, fwd-*): the node decides what a Frame means. wsconn
//     only moves bytes and does not parse them.
//   - The HTTP surface: which route is mounted where, which JSON error body
//     a failed upgrade gets, and binding the session into the connection map
//     all stay in back/internal/router and back/internal/transport.
//   - Backpressure: there is none. DataChannel sessions expose
//     BufferedAmount/SetBufferedAmountLowThreshold for serveFile's flow
//     control (see transport.rtcSession); a WSSession has no equivalent
//     because gorilla has no application-level write queue. The only bound on
//     a stalled peer is the write deadline. Do not assume this package can be
//     taught flow control later without changing the Session contract.
//
// # Dependency stance
//
// No third-party dependency beyond gorilla/websocket, which only upgrader.go
// imports (for the handshake). The opcode constants are deliberately
// duplicated in frame.go rather than imported, so the wire-level contract is
// visible at this boundary. Every call the Session makes on the socket goes
// through the Conn interface (conn.go), which *websocket.Conn satisfies — the
// same minimal-interface stance signalframe takes with JSONWriter, and what
// makes the scheduler testable without a socket.
package wsconn
