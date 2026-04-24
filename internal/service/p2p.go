// Package service provides business logic layer for download and P2P operations.
// Uses provider.Manager for content retrieval and libp2p for networking.

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
