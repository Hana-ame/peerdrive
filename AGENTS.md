# AGENTS.md

> peerdrive 是 monorepo：`h2f/`、`peerjs/`、`webrtc/` 各自一个 Go module，根上 `go.work` 把它们串起来。
> 三条 CI 用 `paths` 过滤，谁变了跑谁，互不阻塞。

## 模块

| 目录 | module | 一句话 | 契约 |
| --- | --- | --- | --- |
| `h2f/` | `github.com/Hana-ame/peerdrive/h2f` | 按 hash 找文件：目录记位置，文件记内容 | `h2f/AGENTS.md` |
| `peerjs/` | `github.com/Hana-ame/peerdrive/peerjs` | 本机进程当 PeerJS peer，浏览器用 log 里的 id 连过来互传字符串 | `peerjs/AGENTS.md` |
| `webrtc/` | `github.com/Hana-ame/peerdrive/webrtc` | 借 PeerJS 信令自建 DataChannel，传文件/metadata/控制/校验 | `webrtc/AGENTS.md` |

依赖方向是**单向**的：`webrtc` → `peerjs`（只用它的信令层，通过 `RegisterTransport` 接管 OFFER）。`h2f` 不依赖任何一方。
`peerjs` 只依赖 `gorilla/websocket` + `pion/webrtc`，`h2f` 只依赖 `modernc.org/sqlite`。

## 构建与测试

```bash
go work sync                        # 根目录，同步各 module 的依赖
cd h2f    && go build ./... && go test ./...
cd peerjs && go build ./... && go test ./...
cd webrtc && go build ./... && go test ./...
```

CI 里不依赖 `go.work`：每条流水线 `cd` 进自己的模块跑，`go-version-file` 指向该模块的 `go.mod`。`webrtc/go.mod` 用 `replace` 指向 `../peerjs`，所以 checkout 整个仓库即可解析。

## CI

| workflow | 触发路径 | jobs |
| --- | --- | --- |
| `.github/workflows/h2f.yml` | `h2f/**`、`go.work` | test (ubuntu/macos/windows) / cross-build / win7 |
| `.github/workflows/peerjs.yml` | `peerjs/**`、`go.work` | test (ubuntu/windows) / cross-build ×4 / e2e |
| `.github/workflows/webrtc.yml` | `webrtc/**`、`peerjs/**`、`go.work` | test (ubuntu/windows) / cross-build ×4 / e2e |

`webrtc` 那条也监听 `peerjs/**`，因为它依赖对方；反过来改 `webrtc/` 不会触发 `peerjs` 那条。

## 目录约定

- `h2f/interface/` — 纯契约：`FileMetadata` + 四个错误哨兵，零依赖
- `h2f/metadata/` — 实现：SQLite、扫描、清单编码、四平台时间戳
- `peerjs/` 根包 — PeerJS 客户端（信令 + WebRTC 应答 + DataChannel），`signal.go` 是「信令 vs WebRTC」的切面
- `peerjs/cmd/peerjs-node/` — 入口，字符串 echo
- `peerjs/index.html` — 浏览器侧字符串 demo
- `webrtc/conn.go` — `Listen` + `Conn`，PeerConnection 与 DataChannel
- `webrtc/frame.go` — 帧格式、分片、重组
- `webrtc/cmd/webrtc-node/` — 入口，收帧落盘
- `webrtc/index.html` — 浏览器 demo，原生 `RTCPeerConnection`
- `peerjs/e2e/`、`webrtc/e2e/` — CI 用的 Playwright 脚本

各模块的具体契约见自己的 `AGENTS.md`，这里只做索引。
