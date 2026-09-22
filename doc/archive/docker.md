# Peerdrive Docker Multi-Node Test Environment

Docker-based multi-node test environment for Peerdrive P2P dual-stack testing with NAT isolation simulation.

## Architecture

```
                        public-net
                    ┌──────┼──────┐
                    │      │      │
                 relay  bt-seed reg-server
                 ┃   ┃
         nat-net-a  nat-net-b
           │            │
         peer-a       peer-b
```

- **relay** -- Public libp2p circuit relay server (bridges NAT-isolated peers)
- **peer-a** -- P2P client behind NAT (on `nat-net-a`, cannot reach peer-b directly)
- **peer-b** -- P2P client behind NAT (on `nat-net-b`, cannot reach peer-a directly)
- **bt-seed** -- BitTorrent Mainline DHT seed node (stable DHT peer for testing)
- **reg-server** -- Registration / authentication server

## Files

| File | Description |
|------|-------------|
| `Dockerfile` | Minimal alpine image for peerdrive-server (pre-built binary) |
| `Dockerfile.reg-server` | Minimal alpine image for reg-server (pre-built binary) |
| `docker-compose.yml` | 5-node compose file with 3 isolated bridge networks |
| `docker-entrypoint.sh` | Entrypoint with relay peer discovery and bootstrap |
| `test-in-docker.sh` | Full integration test suite |
| `dual-stack-test.sh` | Per-node dual-stack P2P test (from parent dir) |
| `peerdrive-server` | Pre-built linux/amd64 binary (built from host) |
| `reg-server` | Pre-built linux/amd64 binary (built from host) |

## Quick Start

```bash
cd p2p-dual-stack/docker

# Build images and start all containers
docker compose up -d

# Check node status
curl http://localhost:3000/p2p/status | jq
curl http://localhost:4000/ping

# Run the full integration test suite
bash test-in-docker.sh

# Stop and clean up
docker compose down -v
```

## Network Isolation

Three Docker bridge networks provide NAT simulation:

- **public-net** (`172.28.0.0/24`) -- Relay, bt-seed, and reg-server. Internet accessible.
- **nat-net-a** (`172.28.1.0/24`) -- peer-a only (plus relay as bridge). Can reach internet for BT DHT bootstrap.
- **nat-net-b** (`172.28.2.0/24`) -- peer-b only (plus relay as bridge). Can reach internet for BT DHT bootstrap.

Peers on different NAT networks cannot communicate directly -- all traffic routes through the relay node.

## Host Port Mapping

| Service    | Host Port | Container Port |
|------------|-----------|----------------|
| relay      | 3000      | 3000 (HTTP)    |
| relay      | 4001      | 4001 (libp2p)  |
| peer-a     | 3001      | 3000 (HTTP)    |
| peer-b     | 3002      | 3000 (HTTP)    |
| bt-seed    | 3003      | 3000 (HTTP)    |
| reg-server | 4000      | 4000 (HTTP)    |

## Environment Variables

Each node can be configured via env vars in docker-compose.yml. Key variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `PEERDRIVE_P2P_ENABLE` | `true` | Enable P2P service |
| `PEERDRIVE_RELAY_MODE` | `client` | Relay mode: `server`, `client`, or `off` |
| `PEERDRIVE_PUBLIC_REACHABLE` | `false` | Whether this node has a public IP |
| `PEERDRIVE_BT_DHT_ENABLE` | `true` | Enable BT Mainline DHT |
| `RELAY_HOST` | (none) | Docker entrypoint: relay hostname for bootstrap |

## Building Binaries

Binaries are pre-built from the host to avoid Go toolchain in Docker:

```bash
# From go/ directory
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" \
  -o ../p2p-dual-stack/docker/peerdrive-server ./cmd/server/

# From registration-server/ directory
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" \
  -o ../p2p-dual-stack/docker/reg-server ./cmd/server/
```

## Test Suite

Run the full test suite:

```bash
./test-in-docker.sh
```

The test script:
1. Builds Docker images
2. Starts all 5 containers
3. Waits for all healthchecks
4. Verifies network isolation (peer-a cannot reach peer-b directly)
5. Checks P2P connections (peers connected to relay)
6. Runs dual-stack tests against each node (upload, announce, find, download)
7. Tests cross-node file transfer (upload to peer-a, download from peer-b)
8. Tests registration server (register, login)
9. Checks BT DHT status on all nodes
10. Stops and cleans up containers

## Manual Testing

```bash
# Start services
docker compose up -d

# Check P2P status on each node
curl -s http://localhost:3000/p2p/status | jq .
curl -s http://localhost:3001/p2p/status | jq .
curl -s http://localhost:3002/p2p/status | jq .

# Get node info
curl -s http://localhost:3000/p2p/node | jq .

# Check connected peers
curl -s http://localhost:3000/p2p/peers | jq .

# Upload a file
curl -s -X POST -F "file=@/etc/hostname" http://localhost:3001/files/upload | jq .

# Announce on dual stack
curl -s -X POST -H 'Content-Type: application/json' \
  -d '{"hash":"<sha256-hash>"}' \
  http://localhost:3001/p2p/dual/announce | jq .

# Download by hash from another node
curl -s http://localhost:3002/sha256sum/<sha256-hash> -o /tmp/downloaded

# Stop everything
docker compose down -v
```
