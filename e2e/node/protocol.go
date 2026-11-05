// Package node 是 m/e2e 的节点侧实现。
//
// 链路形态：
//
//	浏览器 ──PeerJS 数据通道（只搬 JSON 字符串信令）──> 本节点
//	浏览器 <──第二条 WebRTC DataChannel（帧协议）─────> 本节点
//
// 第一条通道由 PeerJS 服务器撮合，第二条的 SDP 完全走第一条通道，不再经过
// PeerJS。第二条通道上跑的帧直接复用 webrtc 模块的 kind/stream 格式。
package node

// 控制动作：跑在 webrtc.KindControl 的 JSON 载荷上。
const (
	ActionCollection = "collection" // 要一份 collection
	ActionFile       = "file"       // 按 sha256 要一段文件内容
	ActionPing       = "ping"
	ActionPong       = "pong"
)

// SignalMessage 是浏览器与节点之间走 PeerJS 字符串通道的信令信封。
type SignalMessage struct {
	Type         string     `json:"type"` // offer | answer | candidate | error
	ConnectionID string     `json:"connectionId,omitempty"`
	SDP          string     `json:"sdp,omitempty"`
	Candidate    *Candidate `json:"candidate,omitempty"`
	Message      string     `json:"message,omitempty"`
}

// Candidate 是 ICE 候选的 JSON 形状。
type Candidate struct {
	Candidate     string  `json:"candidate"`
	SDPMid        *string `json:"sdpMid,omitempty"`
	SDPMLineIndex *uint16 `json:"sdpMLineIndex,omitempty"`
	Username      *string `json:"usernameFragment,omitempty"`
}

// FileRef 是 collection 里的一条。浏览器拿着 SHA256 就能请求内容。
type FileRef struct {
	SHA256   string `json:"sha256"`
	Path     string `json:"path"`
	Filename string `json:"filename"`
	MimeType string `json:"mime"`
	Size     int64  `json:"size"`
}

// Collection 是节点对外声明的集合快照。
type Collection struct {
	Name       string    `json:"name"`
	Root       string    `json:"root"`
	Files      []FileRef `json:"files"`
	Count      int       `json:"count"`
	TotalBytes int64     `json:"total_bytes"`
	At         int64     `json:"at"`
}

// FileRequest 是 KindControl 上 action=file 的载荷。
type FileRequest struct {
	SHA256 string `json:"sha256"`
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
}

// FileHeader 是一段区间请求的应答头（KindMetadata），先发它再发数据。
type FileHeader struct {
	SHA256 string `json:"sha256"`
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
	Size   int64  `json:"size"`
}

// RangeCheck 是一段区间发完后的校验（KindCheck）。
//
// 浏览器逐个区间比对 Digest，整文件再用 collection 里的 SHA256 终验一次：
// 分段校验负责定位坏块（重传最小代价），整文件校验负责兜底。
type RangeCheck struct {
	Stream uint32 `json:"stream"`
	SHA256 string `json:"sha256"`
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
	Digest string `json:"digest"`
}

// ErrorPayload 是 webrtc.KindError 的载荷。
type ErrorPayload struct {
	Message string `json:"message"`
}
