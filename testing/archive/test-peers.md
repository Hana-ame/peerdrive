# Peerdrive P2P Dual-Stack Test Peers

Simulated IPFS/libp2p and BitTorrent DHT peers for integration testing of the Peerdrive dual-stack protocol.

## Files

| File | Description |
|------|-------------|
| `ipfs-peer.py` | Simulated IPFS/libp2p peer (HTTP) |
| `bt-peer.py` | Simulated BitTorrent DHT peer (UDP/bencode) |
| `dual-stack-test.py` | Integration test orchestrator |

## Quick Start

```bash
# Start test peers
python3 ipfs-peer.py --port 9001 &
python3 bt-peer.py --port 6882 &

# Run integration test (with external Peerdrive server)
python3 dual-stack-test.py --peerdrive-api http://localhost:3000
```

## Start Peers Individually

### IPFS Peer

```bash
python3 ipfs-peer.py --port 9001
```

Listens on TCP for HTTP requests:

| Endpoint | Description |
|----------|-------------|
| `GET /ping` | Health check |
| `GET /p2p/node` | Peer info (Peer ID, multiaddrs) |
| `POST /p2p/connect` | Accept connection from remote peer |
| `POST /files/upload` | Upload file (returns SHA256 hash) |
| `GET /files/<hash>` | Download file by hash |

### BT DHT Peer

```bash
python3 bt-peer.py --port 6882 --bootstrap 127.0.0.1:6881
```

Listens on UDP for bencoded KRPC DHT messages:

| Query | Description |
|-------|-------------|
| `ping` | Node liveness check |
| `find_node` | Returns simulated close nodes |
| `get_peers` | Returns peers for known infohashes, or close nodes |
| `announce_peer` | Registers a peer for an infohash |

## Integration Test

```bash
# Automatic (starts test peers, connects to existing Peerdrive)
python3 dual-stack-test.py --peerdrive-api http://localhost:3000

# Auto-start Peerdrive binary if found
python3 dual-stack-test.py --start-peerdrive

# Specify ports
python3 dual-stack-test.py --peerdrive-api http://localhost:3000 --ipfs-port 9001 --bt-port 6882
```

The test runs three scenarios:

1. **Scenario A**: Upload file to Peerdrive, announce on both IPFS and BT DHT, verify both peers can find it.
2. **Scenario B**: Create file on test IPFS peer, Peerdrive discovers via DHT, downloads.
3. **Scenario C**: Announce on BT DHT, test BT peer responds to `get_peers`, Peerdrive connects.
