# AGENTS.md

## e2e

把三个模块串成一条可验证链路：h2f 的 collection → PeerJS 通道搬信令 → 自建 WebRTC
数据通道 → 按 sha256 分块并行下载（断点续传）→ sha256 校验。

**两条通道，分工明确**：

1. **第 1 条：PeerJS 数据通道**（`0.peerjs.com` 撮合）——只搬 JSON 信令字符串
   （`offer` / `answer` / `candidate`），不跑任何业务字节；
2. **第 2 条：自建 RTCPeerConnection + DataChannel**——SDP 全部走第 1 条通道交换，
   **不经过 PeerJS 服务器的 OFFER 广播**；通道上跑 `webrtc` 帧协议。

- `node/` — 节点侧：`service.go`（collection 编码 + 区间文件服务）、`signal.go`
  （PeerJS 字符串 ↔ `webrtc.Server` 状态机）、`serve.go`（帧请求分发）、`web.go`（页面静态服务）
- `cmd/peerdrive/` — 入口；构建出的 `./peerdrive` 即节点进程
- `web/` — 浏览器侧：`signaling.js`（双通道）、`frames.js`（帧通道）、
  `downloader.js`（分块并行/续传/校验）、`app.js`（状态分发）
- `e2e/run.mjs` — CI 用 Playwright：连上 → 取 collection → 下载 → 暂停 → 重建通道 → 续传 → 校验

### 跑起来

```bash
go build -o ./peerdrive ./e2e/cmd/peerdrive
./peerdrive -share ./shared -db /tmp/pd/meta.db -http 127.0.0.1:8789 -repo .
```

- `-share`：被 h2f `Index` 扫描，随后用 `metadata.Collection(paths)` 按路径回查成
  collection（两步都过才对外声明，保证“列表里有的 sha256 一定取得到文件”）；
- 启动日志里打 `PEER ID`；浏览器打开 `http://127.0.0.1:8789/` 填该 id；
- `-repo` 指向仓库根：页面所需 `/assets/{frame.js,sha256.js,peerjs.min.js}` 直接读
  `webrtc/frame.js`、`packages/peerdrive-core/src/sha256.js`、`peerjs/peerjs.min.js`，
  **不复制副本**（帧协议与 SHA-256 都只有一份实现）。

### 帧协议（第 2 条通道，kind/stream 复用 `webrtc`）

| 方向 | kind | 载荷 |
| --- | --- | --- |
| 浏览器 → 节点 | `control` | `{action:"collection"}` |
| 节点 → 浏览器 | `metadata` | `Collection` JSON |
| 浏览器 → 节点 | `control` | `{action:"file", sha256, offset, length}` |
| 节点 → 浏览器 | `metadata` | `FileHeader{SHA256,Offset,Length,Size}` |
| 节点 → 浏览器 | `data` | 区间字节，≤64 KiB/帧，同 stream 内有序 |
| 节点 → 浏览器 | `check` | `RangeCheck{stream,offset,length,digest}`，digest = 区间 sha256 |
| 节点 → 浏览器 | `error` | `{message}` |

`length ≤ 0` 或越过文件尾时裁到文件尾（`FileHeader.Length` 回真实长度），让浏览器
最后一块自然收口。

### 浏览器侧行为

- **分块**：1 MiB/块，N 条 stream 并行（UI 可调 1–8）；
- **断点续传**：暂停只停“发新请求”，在飞块照收；通道失效时在飞块退回 pending，
  建新通道后只补未完成块；
- **校验**：每块收完对 `check.digest`，全部到齐后再对 collection 里的整文件 sha256
  终验（分段负责定位坏块，整文件负责兜底）；
- **E2E 钩子**：`window.__PEERDRIVE_E2E`（阶段/collection/下载快照 + `pause/resume/rebuild`），
  仅给 Playwright 用，不参与协议。

### 测试

```bash
cd e2e && go test ./...        # 区间裁剪、区间摘要、error 帧、collection 编码（内存 sender）
cd e2e/e2e && npm install && npx playwright install chromium
node run.mjs                    # 需节点已启动（PEER_LOG 指向节点日志）
```

## 链接

- 上层索引：`../AGENTS.md`
- 模块契约：`../h2f/AGENTS.md`、`../peerjs/AGENTS.md`、`../webrtc/AGENTS.md`