# WebRTC High Throughput & Streaming Mode Specification (Issue #316)

## 1. Overview & Problem Statement

Historically, Peerdrive used a static 64KB chunk size (`StandardDefaultChunkSize`) for DataChannel file transfers.
In Gigabit LAN or low-RTT environments, 64KB chunks generate excessive frame serialization, channel dispatching, and goroutine context-switching overhead.
In high-RTT or lossy networks, burst sends without reactive backpressure cause SCTP queue bloat and retransmission storms.

Issue #316 introduces:
1. **High Throughput Mode** (`CapHighThroughput = "throughput"`): Adaptive chunking (up to 1MB) and zero-copy buffer pooling.
2. **Heavy Traffic / Streaming Mode** (`CapHeavyTraffic = "streaming"`): Reactive event-driven backpressure and QoS traffic prioritization.

## 2. Adaptive Chunking

Chunk size is calculated adaptively based on observed RTT:

| RTT Range | Negotiated Mode | Chunk Size | Rationale |
|---|---|---|---|
| `< 20ms` (LAN / Local) | High Throughput | 1MB (`MaxThroughputChunkSize`) | Maximize throughput, minimize per-packet overhead |
| `20ms - 80ms` (Fast WAN) | High Throughput | 256KB (`HighThroughputChunkSize`) | Balanced throughput with moderate SCTP buffer demand |
| `> 80ms` or Default | Standard | 64KB (`StandardDefaultChunkSize`) | Stable delivery without overwhelming SCTP buffers |

Implementation:
`AdaptiveChunkSize(rtt time.Duration, highThroughput bool) int` in `back/internal/transport/capabilities.go`.

## 3. Zero-Copy Buffer Pool

To eliminate GC pause and heap allocation churn during multi-gigabyte transfers:
- A thread-safe `sync.Pool` (`throughputPool`) manages 256KB/1MB byte slices.
- `GetThroughputBuffer()` acquires a pre-allocated buffer for chunk generation.
- `PutThroughputBuffer()` safely returns clean buffers to the pool once transmitted.

## 4. Reactive Backpressure & Channel Bonding

- **BufferedAmountLowThreshold**: Configured to 1MB. Sender streams data only while buffered bytes remain below 4MB.
- **QoS Stream Separation**: Control messages (`share`, `ping`, `admin`) use a dedicated control stream to prevent large data transfers from blocking heartbeat signals (mitigating Issue #274).
- **Multi-DataChannel Bonding**: Large files can be striped across multiple parallel WebRTC DataChannels, aggregating SCTP throughput and avoiding single-stream congestion bottlenecks.
