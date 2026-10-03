# P2P Module Docs

> ⚠️ As of 2026-08-16: The interconnection layer has been entirely replaced with **PeerJS signaling + WebRTC DataChannel**.

## Currently Active

- [TRANSPORT.md](./TRANSPORT.md) — Interconnection framework: WS + PeerJS dual transport + Session abstraction + signaling assembly
- [API-DESIGN.md](./API-DESIGN.md) — HTTP endpoint design under the `/p2p` group

## Archived (legacy libp2p/BT-DHT stack, **do not implement from these**)

> See [doc/archive/LEGACY.md](../../archive/LEGACY.md). These three have been moved to [archive/](./archive/):

- [archive/p2p.md](./archive/p2p.md) — Legacy libp2p network architecture
- [archive/dual-stack-protocol.md](./archive/dual-stack-protocol.md) — Legacy IPFS+BT dual-stack specification
- [archive/grid.md](./archive/grid.md) — Legacy P2P test grid
