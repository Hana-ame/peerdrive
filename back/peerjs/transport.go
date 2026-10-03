package peerjs

import "github.com/pion/webrtc/v4"

// Frame is a message frame on the data channel (library-defined type, transport-implementation agnostic).
// IsText=true is a text frame (control header/JSON); false is a binary frame (data chunk).
type Frame struct {
	IsText bool
	Data   []byte
}

// DataChannel is a data-plane transport abstraction.
// Extensibility: the current implementation uses WebRTC DataChannel (pion); it can later be replaced with
// WebSocket / TCP direct connection, etc. — Connection depends only on this interface.
type DataChannel interface {
	SendText(string) error
	Send([]byte) error
	OnOpen(func())
	OnMessage(func(Frame))
	OnClose(func())
	Open() bool
	BufferedAmount() uint64
	SetBufferedAmountLowThreshold(uint64)
	OnBufferedAmountLow(func())
	Close()
}

// pionChannel adapts pion/webrtc.DataChannel to the DataChannel interface.
type pionChannel struct {
	dc *webrtc.DataChannel
}

func newPionChannel(dc *webrtc.DataChannel) DataChannel {
	return &pionChannel{dc: dc}
}

func (p *pionChannel) SendText(s string) error                { return p.dc.SendText(s) }
func (p *pionChannel) Send(b []byte) error                    { return p.dc.Send(b) }
func (p *pionChannel) Open() bool                             { return p.dc.ReadyState() == webrtc.DataChannelStateOpen }
func (p *pionChannel) BufferedAmount() uint64                 { return p.dc.BufferedAmount() }
func (p *pionChannel) SetBufferedAmountLowThreshold(n uint64) { p.dc.SetBufferedAmountLowThreshold(n) }
func (p *pionChannel) OnBufferedAmountLow(f func())           { p.dc.OnBufferedAmountLow(f) }
func (p *pionChannel) Close()                                 { _ = p.dc.Close() }

func (p *pionChannel) OnOpen(f func()) {
	p.dc.OnOpen(func() { f() })
}

func (p *pionChannel) OnMessage(f func(Frame)) {
	p.dc.OnMessage(func(m webrtc.DataChannelMessage) {
		f(Frame{IsText: m.IsString, Data: m.Data})
	})
}

func (p *pionChannel) OnClose(f func()) {
	p.dc.OnClose(func() { f() })
}
