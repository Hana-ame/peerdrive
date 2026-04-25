// Package service 提供业务逻辑层，封装下载和 P2P 操作。
// P2PService 封装 libp2p 节点的生命周期管理：
//   NewP2PService      — 创建 libp2p Host，监听 /ip4/0.0.0.0/tcp/0（随机端口）
//   GetNodeInfo        — 返回 PeerID + 所有 multiaddr
//   GetConnectedPeers  — 通过 host.Network().Peers() 获取连接的对等节点
//   PingPeer           — 通过 ping.PingService 发送 Ping 并等待 RTT 结果
//   FetchFile(hash)    — 通过 Bitswap 从 P2P 网络获取指定 SHA256 hash 的文件内容
//
// FetchFile 实现：
//   1. 将 64 字符 hex SHA256 解码为 32 字节，编码为 multihash，构造 CIDv1(Raw)
//   2. 通过 bitswap.GetBlock(ctx, cid) 请求块
//   3. 返回块数据的 io.ReadCloser
//   前提：P2PService 需要持有 bitswap.Bitswap 实例（在 NewP2PService 中初始化）

package service

import (
	"context"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/ping"
)

type P2PService struct {
	Host host.Host
	Ping *ping.PingService
}

func NewP2PService(ctx context.Context) (*P2PService, error) {
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/0"))
	if err != nil {
		return nil, err
	}
	pingSvc := ping.NewPingService(h)
	return &P2PService{
		Host: h,
		Ping: pingSvc,
	}, nil
}

func (p *P2PService) GetNodeInfo() (peer.ID, []string) {
	addrs := make([]string, 0)
	for _, addr := range p.Host.Addrs() {
		addrs = append(addrs, addr.String())
	}
	return p.Host.ID(), addrs
}

func (p *P2PService) GetConnectedPeers() []peer.ID {
	return p.Host.Network().Peers()
}

func (p *P2PService) PingPeer(ctx context.Context, peerID peer.ID) (time.Duration, error) {
	result := p.Ping.Ping(ctx, peerID)
	select {
	case res := <-result:
		return res.RTT, res.Error
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}
