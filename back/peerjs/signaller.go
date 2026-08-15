package peerjs

import "context"

// Signaller 信令通道抽象：负责节点注册、消息收发。
// 扩展性：当前实现为 PeerJS 公共云协议（peerjsSignaller），后续可替换为
// 自托管 peerjs-server、MQTT 房间信令等，无需改动 Peer/Connection 层。
//
// 注意：实现自定义 Signaller 时无需关心消息分发——OnMessage 由框架
// 内部注入（Peer 构造时调用），实现者只需在收到信令消息后调用
// 注入的回调（见 peerJSSignaller.readLoop 的做法）。
type Signaller interface {
	// Dial 注册节点并建立信令连接；ctx 取消时中止。
	Dial(ctx context.Context) error
	// ID 返回节点 ID（Dial 成功后有效；未指定 ID 时服务端分配）。
	ID() string
	// Send 发送消息到信令服务器。
	Send(m Message) error
	// OnMessage 注册信令消息回调。
	// Deprecated: 仅框架内部使用（Peer 注入消息路由）。使用者无需调用；
	// 实现自定义 Signaller 时把收到的消息交给注入的回调即可。
	OnMessage(h MessageHandler)
	// Done 返回信令连接断开通知（readLoop 因网络错误/对端关闭退出时关闭）。
	// H7 修复：之前 readLoop 出错静默退出、connected=false，但没有任何信号
	// 通知上层——startLoop 只 select ctx/closed 两个永不触发的信号，公网 WS
	// 掉一次后节点永久失聪直到重启。重连循环依赖本通道触发整轮重连。
	Done() <-chan struct{}
	// Close 关闭信令连接。
	Close() error
}

// MessageHandler 信令消息回调。
// Deprecated: 使用者无需直接接触——消息处理由 Peer 内部路由接管
// （OFFER/ANSWER/CANDIDATE/LEAVE/EXPIRE 均内部处理）。该类型仅作为
// Signaller 实现者与框架之间的内部接线保留。
type MessageHandler func(m Message) error

// SignallerFactory 创建自定义信令的入口（NewPeer 的可选参数）。
type SignallerFactory func() Signaller
