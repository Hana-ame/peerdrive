# AGENTS.md

> peerdrive 是 monorepo：`h2f/` 与 `peerjs/` 各自一个 Go module，根上 `go.work` 把它们串起来。
> 两条 CI 用 `paths` 过滤，谁变了跑谁，互不阻塞。

## 模块

| 目录 | module | 一句话 | 契约 |
| --- | --- | --- | --- |
| `h2f/` | `github.com/Hana-ame/peerdrive/h2f` | 按 hash 找文件：目录记位置，文件记内容 | `h2f/AGENTS.md` |
| `peerjs/` | `github.com/Hana-ame/peerdrive/peerjs` | 本机进程当 PeerJS peer，浏览器用 log 里的 id 连过来互传字符串 | `peerjs/AGENTS.md` |

两个模块**不互相 import**：`h2f` 只依赖 `modernc.org/sqlite`，`peerjs` 只依赖 `pion/webrtc` + `gorilla/websocket`。以后拆成独立仓，把目录搬走即可。

## 构建与测试

```bash
go work sync                  # 根目录，同步两个 module 的依赖
cd h2f   && go build ./... && go test ./...
cd peerjs && go build ./... && go test ./...
```

CI 里不依赖 `go.work`：每条流水线 `cd` 进自己的模块跑，`go-version-file` 指向该模块的 `go.mod`。`go.work` 只服务本地开发和 IDE。

## CI

| workflow | 触发路径 | jobs |
| --- | --- | --- |
| `.github/workflows/h2f.yml` | `h2f/**`、`go.work` | test (ubuntu/macos/windows) / cross-build / win7 |
| `.github/workflows/peerjs.yml` | `peerjs/**`、`go.work` | test (ubuntu/windows) / cross-build ×4 / e2e |

改 `go.work` 会同时触发两条；只改 `h2f/` 或 `peerjs/` 只触发一条。

## 目录约定

- `h2f/interface/` — 纯契约：`FileMetadata` + 四个错误哨兵，零依赖
- `h2f/metadata/` — 实现：SQLite、扫描、清单编码、四平台时间戳
- `peerjs/` 根包 — PeerJS 客户端实现（信令 + WebRTC 应答 + DataChannel）
- `peerjs/cmd/peerjs-node/` — 入口，启动后把 `PEER ID` 打进日志
- `peerjs/index.html` — 浏览器侧 clean demo
- `peerjs/e2e/` — CI 用的 Playwright 脚本

各模块的具体契约见自己的 `AGENTS.md`，这里只做索引。
