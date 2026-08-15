package transport

import (
	peerjs "github.com/Hana-ame/go-peerjs"
)

// rtcSession 把 *peerjs.Connection 适配为 Session 接口。
// 为什么包一层而不是让 Connection 直接实现：Connection.ID 字段是信令路由键
// （connectionId），与会话标识（远端 peer id）语义不同且字段名冲突；
// adapter 在 service 层收敛差异，peerjs 模块保持传输原语职责。
type rtcSession struct {
	c  *peerjs.Connection
	id string // 缓存 PeerID（Connection.PeerID 导出字段，读一次避免歧义）
}

func newRTCSession(c *peerjs.Connection) *rtcSession {
	return &rtcSession{c: c, id: c.PeerID}
}

func (r *rtcSession) ID() string { return r.id }

func (r *rtcSession) SendJSON(v any) error { return r.c.SendJSON(v) }

func (r *rtcSession) SendFrame(header any, body []byte) error { return r.c.SendFrame(header, body) }

func (r *rtcSession) OnMessage(f func(peerjs.Frame)) { r.c.OnMessage(f) }

func (r *rtcSession) OnClose(f func()) {
	r.c.OnClose(func(*peerjs.Connection) { f() })
}

func (r *rtcSession) Close() { r.c.Close() }

// DataChannel 供 serveFile 的写缓冲流控使用（WSSession 无此能力）。
func (r *rtcSession) DataChannel() peerjs.DataChannel { return r.c.DataChannel() }
