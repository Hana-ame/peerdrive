# Peerdrive P2P Chaos Testing Suite

Tools for simulating adverse network conditions to validate Peerdrive P2P dual-stack (IPFS/libp2p + BitTorrent DHT) behaviour under real-world network degradation.

## Files

| File | Description |
|------|-------------|
| `chaos-net.sh` | Host-level network chaos using `tc` and `iptables` |
| `test-under-chaos.sh` | Run P2P tests under various network conditions |
| `docker/docker-compose-chaos.yml` | Docker compose override for container-level chaos |
| `docker/chaos-agent/Dockerfile` | Docker image for the chaos agent |
| `docker/chaos-agent/entrypoint.sh` | Entrypoint that applies tc/iptables from within Docker |

## Requirements

### Kernel Modules

The following Linux kernel modules are required:

```
sch_netem    # Network emulator (packet loss, delay, jitter)
sch_tbf      # Token Bucket Filter (bandwidth limiting)
sch_htb      # Hierarchical Token Bucket (complex shaping)
cls_u32      # U32 classifier (for tc filtering)
xt_statistic # Statistic match (random packet drop via iptables)
xt_comment   # Comment match (rule labelling)
```

Load them with:

```bash
sudo modprobe sch_netem sch_tbf sch_htb cls_u32
```

On Ubuntu/Debian, install extra kernel modules if needed:

```bash
sudo apt-get install linux-modules-extra-$(uname -r)
```

To make modules persistent across reboots:

```bash
echo "sch_netem" | sudo tee -a /etc/modules
echo "sch_tbf"   | sudo tee -a /etc/modules
echo "sch_htb"   | sudo tee -a /etc/modules
echo "cls_u32"   | sudo tee -a /etc/modules
```

### Required Tools

- `tc` (from `iproute2`)
- `iptables`
- `ip` (from `iproute2`)
- `sudo` (for host-level chaos)

Install on Ubuntu/Debian:

```bash
sudo apt-get install iproute2 iptables
```

## Quick Start

### 1. Simulate Bad Network

```bash
# 30% packet loss, 500ms latency, limited to 1Mbps
sudo ./chaos-net.sh start --loss 30% --latency 500ms --bandwidth 1Mbps
```

### 2. Run a Test Under Chaos

```bash
# Test IPFS file transfer with 30% packet loss
./test-under-chaos.sh --loss 30% --test ipfs-test.sh

# Test BT DHT under high latency
./test-under-chaos.sh --latency 2000ms --test bt-dht-test.sh

# Test with combined conditions
./test-under-chaos.sh --loss 10% --latency 100ms --bandwidth 1Mbps --test dual-stack-test.sh
```

### 3. Run the Full Chaos Matrix

```bash
# Runs all test scripts under every combination of:
#   Loss:   0%, 10%, 30%, 50%
#   Latency: 0ms, 100ms, 500ms
#   Bandwidth: unlimited, 1Mbps, 100Kbps
#   Combined: loss=30% latency=500ms bw=100Kbps
./test-under-chaos.sh --matrix

# Matrix for a specific test only
./test-under-chaos.sh --matrix --test ipfs-test.sh
```

### 4. Simulate Flaky Connectivity

```bash
# Network drops for 5 seconds every 30 seconds
sudo ./chaos-net.sh flaky --interval 30s --duration 5s

# Then run a test while flaky mode is active
./test-under-chaos.sh --test ipfs-test.sh
```

### 5. Stop All Chaos

```bash
sudo ./chaos-net.sh stop
```

## Host Chaos Script (`chaos-net.sh`)

### Commands

```
start   Apply chaos rules to the network interface
stop    Remove all chaos rules and restore networking
flaky   Cycle between connected/disconnected states
status  Show current chaos state and applied rules
```

### Start Options

| Option | Default | Description |
|--------|---------|-------------|
| `--loss <X%>` | 0% | Packet loss percentage (e.g., 10%, 30%, 50%, 90%) |
| `--latency <Xms>` | 0ms | Additional network latency (e.g., 100ms, 500ms, 2000ms) |
| `--bandwidth <X>` | unlimited | Bandwidth limit (e.g., 1Mbps, 100Kbps, 10Kbps) |
| `--jitter <Xms>` | 0ms | Random latency variation |

### Flaky Options

| Option | Default | Description |
|--------|---------|-------------|
| `--interval <Xs>` | 15s | Time between disconnect cycles |
| `--duration <Xs>` | 5s | How long each disconnect lasts |

### Environment Variables

| Variable | Description |
|----------|-------------|
| `CHAOS_IFACE` | Network interface to apply rules to (default: auto-detect) |
| `CHAOS_IGNORE_MODULES` | Set to 1 to skip kernel module check |
| `CHAOS_IPTABLES_DROP` | Additional iptables-based random drop probability |

### Examples

```bash
# Severe packet loss
sudo ./chaos-net.sh start --loss 50%

# High latency satellite-like connection
sudo ./chaos-net.sh start --latency 2000ms

# Slow mobile connection
sudo ./chaos-net.sh start --latency 100ms --bandwidth 1Mbps --jitter 50ms

# Combined: terrible connection
sudo ./chaos-net.sh start --loss 30% --latency 500ms --bandwidth 100Kbps --jitter 100ms

# Check status
sudo ./chaos-net.sh status
```

## Docker Chaos (`docker-compose-chaos.yml`)

The Docker chaos override adds a `chaos-agent` container that runs alongside the Peerdrive services and applies network degradation at the host level.

### Usage

```bash
cd docker

# Start with default chaos (no degradation, agent idle)
docker compose -f docker-compose.yml -f docker-compose-chaos.yml up -d

# Start with 30% packet loss
CHAOS_LOSS=30% \
  docker compose -f docker-compose.yml -f docker-compose-chaos.yml up -d

# Start with high latency + bandwidth limit
CHAOS_LATENCY=500ms CHAOS_BANDWIDTH=1Mbps \
  docker compose -f docker-compose.yml -f docker-compose-chaos.yml up -d

# Start flaky mode
CHAOS_MODE=flaky CHAOS_INTERVAL=30s CHAOS_DURATION=5s \
  docker compose -f docker-compose.yml -f docker-compose-chaos.yml up -d

# Run tests with Docker chaos
../test-under-chaos.sh --docker --loss 30% --test ../ipfs-test.sh

# Stop everything
docker compose -f docker-compose.yml -f docker-compose-chaos.yml down
```

### Chaos Agent Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CHAOS_MODE` | `start` | `start`, `flaky`, `stop`, or `status` |
| `CHAOS_IFACE` | auto | Target network interface |
| `CHAOS_PACKET_LOSS` (or `CHAOS_LOSS`) | 0% | Packet loss percentage |
| `CHAOS_LATENCY` | 0ms | Added latency |
| `CHAOS_JITTER` | 0ms | Random latency variation |
| `CHAOS_BANDWIDTH` | unlimited | Bandwidth limit |
| `CHAOS_INTERVAL` | 15s | Flaky cycle interval |
| `CHAOS_DURATION` | 5s | Flaky disconnect duration |
| `CHAOS_AUTO_DETECT` | false | Auto-detect container veth interfaces |
| `CHAOS_TARGETS` | (none) | Comma-separated container names to target |

## Test Harness (`test-under-chaos.sh`)

### Single Test Mode

```bash
# Basic usage
./test-under-chaos.sh --loss 30% --latency 100ms --bandwidth 1Mbps --test ipfs-test.sh

# The test script path is resolved relative to:
#   1. The current directory
#   2. The p2p-dual-stack/ root directory
#   3. The docker/ directory
```

### Matrix Mode

In `--matrix` mode, the harness runs:

1. **Baseline**: No chaos (to confirm tests pass normally)
2. **Loss tests**: 10%, 30%, 50% packet loss (0% skipped as baseline)
3. **Latency tests**: 100ms, 500ms latency (0ms skipped as baseline)
4. **Bandwidth tests**: 1Mbps, 100Kbps (unlimited skipped as baseline)
5. **Combined**: 30% loss + 500ms latency + 100Kbps bandwidth

Results are saved to a temporary directory (`/tmp/peerdrive-chaos-matrix.XXXXXX/`) with individual per-test logs and a summary file.

```bash
# Full matrix against all test scripts
./test-under-chaos.sh --matrix

# Matrix for a specific test
./test-under-chaos.sh --matrix --test ipfs-test.sh

# Matrix without the baseline run
./test-under-chaos.sh --matrix --no-baseline

# Matrix using Docker chaos
./test-under-chaos.sh --matrix --docker
```

## Architecture

### How `tc` Works

The script uses Linux Traffic Control (`tc`) with the `netem` (Network Emulator) qdisc:

```
Normal:  [应用程序] --> [eth0]
With tc: [应用程序] --> [netem loss/delay/jitter] --> [tbf bandwidth] --> [eth0]
```

For bandwidth limiting combined with netem:

```
[应用程序] --> [HTB root (1:)] --> [class 1:1 @ 1Mbps] --> [netem loss/delay] --> [eth0]
```

### How `iptables` Works for Flaky Mode

Flaky mode uses a custom iptables chain (`PEERDRIVE_CHAOS` in the `mangle` table) that is toggled between `ACCEPT` and `DROP`:

- Rule 1: `-j ACCEPT` (connected phase)
- Rule 1 replaced: `-j DROP` (disconnected phase)

This creates a clean on/off switch without adding/removing rules.

### How Docker Chaos Works

The `chaos-agent` container runs with:
- `network_mode: host` -- sees all host network interfaces
- `privileged: true` -- required for `tc` and `iptables` operations
- Mounted Docker socket (`/var/run/docker.sock`) -- for container introspection

It applies the same `tc`/`iptables` rules as `chaos-net.sh`, but from within a container environment.

## Test Scenarios

### Scenario 1: IPFS File Transfer Under Packet Loss

```bash
# Setup: Run Peerdrive nodes
cd docker && docker compose up -d

# In another terminal: apply 30% packet loss
sudo ./chaos-net.sh start --loss 30%

# Run the IPFS test
./test-under-chaos.sh --loss 30% --test ipfs-test.sh

# Cleanup
sudo ./chaos-net.sh stop
cd docker && docker compose down
```

Expected: IPFS should handle up to 30% loss gracefully (built-in retries and
redundancy). At 50%+ loss, connections may time out.

### Scenario 2: BT DHT Under High Latency

```bash
# Setup: Run Peerdrive nodes
cd docker && docker compose up -d

# Simulate satellite latency
sudo ./chaos-net.sh start --latency 2000ms

# Run BT DHT test
./test-under-chaos.sh --latency 2000ms --test bt-dht-test.sh

# Cleanup
sudo ./chaos-net.sh stop
cd docker && docker compose down
```

Expected: BT DHT operations complete but take longer. DHT lookup timeouts may
increase.

### Scenario 3: Slow Mobile Connection

```bash
# Setup
cd docker && docker compose up -d

# Simulate 3G-like conditions
sudo ./chaos-net.sh start --latency 100ms --jitter 50ms --bandwidth 1Mbps --loss 5%

# Run all tests
./test-under-chaos.sh --loss 5% --latency 100ms --bandwidth 1Mbps --test ipfs-test.sh

# Cleanup
sudo ./chaos-net.sh stop
cd docker && docker compose down
```

## Troubleshooting

### "RTNETLINK answers: No such file or directory"

The `sch_netem` kernel module is not loaded:

```bash
sudo modprobe sch_netem
```

### "tc qdisc add ... Cannot find device"

The network interface was not auto-detected. Specify it explicitly:

```bash
sudo CHAOS_IFACE=eth0 ./chaos-net.sh start --loss 30%
```

### "iptables: No chain/target/match by that name"

The `xt_statistic` module is missing. Load it:

```bash
sudo modprobe xt_statistic
```

### Chaos continues after stopping the test

Run `sudo ./chaos-net.sh stop` to explicitly remove all rules. The script
cleans up both `tc` qdiscs and `iptables` chains.
