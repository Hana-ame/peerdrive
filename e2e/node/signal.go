package node

import (
	"encoding/json"
	"log"

	"github.com/Hana-ame/peerdrive/peerjs"
	"github.com/Hana-ame/peerdrive/webrtc"
	pion "github.com/pion/webrtc/v4"
)

// Bridge 把 PeerJS 字符串通道上的信令，转进 webrtc.Server 的 PeerConnection 状态机。
//
// 这是本方案「第二条通道」的全部信令：第一条 PeerJS 通道只做搬运，节点侧
// 不向 PeerJS 服务器发任何 OFFER/ANSWER。
type Bridge struct {
	Client *peerjs.Client
	Server *webrtc.Server
	Log    *log.Logger
}

// Handle 是 peerjs.Client.OnMessage 的实现：一条字符串 = 一条信令。
//
// Accept 会做 SetRemoteDescription / CreateAnswer（几十到几百毫秒），而
// OnMessage 是在 PeerJS 数据通道的接收回调里同步执行的——在这里阻塞会堵住
// 后续 candidate 的读取，ICE 就只能等，所以 offer 分派到 goroutine。
func (b *Bridge) Handle(remote, text string) {
	var msg SignalMessage
	if err := json.Unmarshal([]byte(text), &msg); err != nil {
		b.Log.Printf("信令解析失败 ← %s: %v", remote, err)
		return
	}

	switch msg.Type {
	case "offer":
		if msg.ConnectionID == "" || msg.SDP == "" {
			b.Log.Printf("offer 缺 connectionId/sdp ← %s", remote)
			return
		}
		offer := peerjs.Offer{
			Remote:        remote,
			ConnectionID:  msg.ConnectionID,
			Label:         "peerdrive",
			Serialization: "raw",
			SDP:           msg.SDP,
		}
		sig := &channelSignal{client: b.Client, log: b.Log}
		go b.Server.Accept(offer, sig)

	case "candidate":
		if msg.ConnectionID == "" || msg.Candidate == nil {
			b.Log.Printf("candidate 缺 connectionId/candidate ← %s", remote)
			return
		}
		b.Server.Offer(msg.ConnectionID, pion.ICECandidateInit{
			Candidate:        msg.Candidate.Candidate,
			SDPMid:           msg.Candidate.SDPMid,
			SDPMLineIndex:    msg.Candidate.SDPMLineIndex,
			UsernameFragment: msg.Candidate.Username,
		})

	default:
		b.Log.Printf("未知信令类型 %q ← %s", msg.Type, remote)
	}
}

// channelSignal 把 answer / candidate 写成 JSON 字符串，走 PeerJS 通道回到浏览器。
type channelSignal struct {
	client *peerjs.Client
	log    *log.Logger
}

func (s *channelSignal) SendAnswer(remote, connectionID, sdp string) error {
	return s.send(remote, SignalMessage{Type: "answer", ConnectionID: connectionID, SDP: sdp})
}

func (s *channelSignal) SendCandidate(remote, connectionID string, cand pion.ICECandidateInit) error {
	return s.send(remote, SignalMessage{
		Type:         "candidate",
		ConnectionID: connectionID,
		Candidate: &Candidate{
			Candidate:     cand.Candidate,
			SDPMid:        cand.SDPMid,
			SDPMLineIndex: cand.SDPMLineIndex,
			Username:      cand.UsernameFragment,
		},
	})
}

// Release 只清理信令侧记账；本实现不维护额外路由表，故为空。
func (s *channelSignal) Release(connectionID string) {}

func (s *channelSignal) send(remote string, msg SignalMessage) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if err := s.client.Send(remote, string(b)); err != nil {
		s.log.Printf("回信令失败 → %s: %v", remote, err)
		return err
	}
	return nil
}
