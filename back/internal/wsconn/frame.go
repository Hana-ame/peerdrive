package wsconn

// WebSocket opcodes. RFC 6455 §5.5. Duplicated rather than imported from
// gorilla so the wire-level contract is visible at the codec boundary; the
// golden vector suite (golden_test.go) fails loudly if any value drifts.
const (
	OpcodeContinuation = 0x00
	OpcodeText         = 0x01
	OpcodeBinary       = 0x02
	OpcodeClose        = 0x08
	OpcodePing         = 0x09
	OpcodePong         = 0x0A
)

// Frame is one inbound message as seen by the node: the text/binary split is
// the whole of the transport's contribution to the frame protocol. Text frames
// carry JSON control headers, binary frames carry data chunks; the split is
// decided by the sender and is meaningless to wsconn, which only reports it.
//
// The shape is deliberately identical to peerjs.Frame in back/peerjs
// ({IsText, Data}) so the transport bridge is a field copy with no lossy
// conversion.
type Frame struct {
	IsText bool
	Data   []byte
}

// MessageToFrame turns a ReadMessage result into a Frame. This is the single
// place the opcode is interpreted, so the text/binary convention has exactly
// one definition.
//
// messageType is opaque here: anything that is not OpcodeText is binary. That
// matches gorilla, which only ever returns OpcodeText/OpcodeBinary from
// ReadMessage (control frames are consumed internally by NextReader), but it
// keeps the mapping total for any future implementation of Conn.
func MessageToFrame(messageType int, data []byte) Frame {
	return Frame{IsText: messageType == OpcodeText, Data: data}
}
