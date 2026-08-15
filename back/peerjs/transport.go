package peerjs

import "github.com/pion/webrtc/v4"

// Frame 数据通道上的一个消息帧（库自定类型，传输实现无关）。
// IsText=true 为文本帧（控制头/JSON），false 为二进制帧（数据块）。
type Frame struct {
	IsText bool
	Data   []byte
}

// DataChannel 数据面传输抽象。
// 扩展性：当前实现为 WebRTC DataChannel（pion），后续可替换为
// WebSocket / TCP 直连等传输——Connection 只依赖本接口。
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

// pionChannel 把 pion/webrtc.DataChannel 适配为 DataChannel 接口。
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
