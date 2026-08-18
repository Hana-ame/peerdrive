# 分层文档索引（doc/layers/）

> 按 AOP 切面组织的模块文档树（2026-08-18 建立）。
> 归属判断规则见 `doc/LAYERS.md`；架构决策/坑见 `doc/REFACTOR.md`；旧业务模块文档
> （auth/bt/ipfs/p2p/storage）仍在 `doc/modules/`，其中标注过时的以本树为准。

---

## ① 信令/传输原语层 — `back/peerjs/`

**一句话**：PeerJS 信令 + WebRTC DataChannel 传输原语，零业务知识。

| 模块 | 文件 | 文档 |
|---|---|---|
| Connection | `connection.go` | [connection.md](L1-peerjs/connection.md) |
| Peer（信令生命周期） | `peer.go` + `signaller.go` | [peer.md](L1-peerjs/peer.md) |
| DataChannel 抽象 | `transport.go` | [transport.md](L1-peerjs/transport.md) |
| 消息/流控 | `message.go` + `flowcontrol_test.go` | [connection.md](L1-peerjs/connection.md)（并入） |

## ② 帧协议层 — `back/internal/transport/`

**一句话**：会话状态机 + verb 分派（req 拉取/上传/索引/转发），知道帧不知道业务。

| 模块 | 文件 | 文档 |
|---|---|---|
| 连接共享核心（分派） | `conn.go` | [conn.md](L2-transport/conn.md) |
| 入站角色（应答） | `inbound.go` | [inbound.md](L2-transport/inbound.md) |
| 出站角色（发起） | `outbound.go` | [outbound.md](L2-transport/outbound.md) |
| 文件索引持久化 | `file_index.go` | [file-index.md](L2-transport/file-index.md) |
| 端口转发 v2 | `forward.go` | [forward.md](L2-transport/forward.md) |
| 会话抽象 | `ws_session.go` + `rtc_session.go` | [sessions.md](L2-transport/sessions.md) |

## ③ 管理面切面 — `back/internal/transport/admin.go` + router 装配

**一句话**：仅本地 WS 会话的管理 verb，内部转发 gin engine 复用全部 controller。

| 模块 | 文件 | 文档 |
|---|---|---|
| admin verb（协议+上传+响应分类） | `admin.go` + `admin_test.go` | [README.md](L3-admin/README.md) |
| router 装配 | `back/internal/router/router.go`（SetAdminHandler）+ `peerjs_routes.go` | 同上 |

## ④ 业务核心 — `back/internal/controller|service|source|downloader`

**一句话**：HTTP 语义业务，不感知自己在被 WS 帧转发还是 HTTP 直接调。

| 模块 | 文件 | 文档 |
|---|---|---|
| controller（17 端点组） | `controller/*.go` | [controllers.md](L4-core/controllers.md) |
| service（业务服务） | `service/*.go` | [services.md](L4-core/services.md) |
| source（统一文件获取） | `source/*.go` | [source.md](L4-core/source.md) |
| downloader（多协议流水线） | `downloader/*.go` | [downloader.md](L4-core/downloader.md) |

## ⑤ 数据切面 — `back/internal/repository/`

**一句话**：SQLite 持久化（file_index 表 sha256→路径 + seq 游标增量同步）。

| 模块 | 文件 | 文档 |
|---|---|---|
| repository（11 个 repo） | `repository/*.go` + `db.go` | [README.md](L5-data/README.md) |

## ⑥ 发现切面 — `back/internal/signalserver/` + `transport/*_discovery.go`

**一句话**：节点怎么互相找到（自托管信令/HTTP 发现/MQTT 房间），只交换 peerId。

| 模块 | 文件 | 文档 |
|---|---|---|
| 自托管信令 + 发现 API | `signalserver/*.go`（cmd/peerserver） | [signalserver.md](L6-discovery/signalserver.md) |
| 发现客户端 | `transport/http_discovery.go` + `transport/mqtt_discovery.go` | [discovery.md](L6-discovery/discovery.md) |

## ⑦ 外部能力切面 — `back/p2p_bt/` + `back/internal/provider/`

**一句话**：独立生态位能力（BT DHT / IPFS 网关），独立 go.mod 或可独立演进。

| 模块 | 文件 | 文档 |
|---|---|---|
| BT DHT 桥 | `p2p_bt/*.go`（独立 go.mod） | [p2p-bt.md](L7-external/p2p-bt.md) |
| IPFS 网关提供者 | `internal/provider/ipfs.go` | [provider.md](L7-external/provider.md) |

## ⑧ 前端切面 — `front/src/`

**一句话**：浏览器端，只与 ② 的 WS 会话 + ③ admin verb 通信。

| 模块 | 文件 | 文档 |
|---|---|---|
| WS 客户端 | `ws.js` | [ws-client.md](L8-frontend/ws-client.md) |
| api 封装 | `api.js` | [api-layer.md](L8-frontend/api-layer.md) |
| 页面/路由/导航 | `pages/` + `App.jsx` + `components/Navbar.jsx` | [pages.md](L8-frontend/pages.md) |

---

## 分层测试（scripts/test-layers.sh）

每层一段独立跑、全跑汇总（任一层失败非零退出）：

```bash
bash scripts/test-layers.sh           # L1-L8（2026-08-18 实测 8/8 全绿）
bash scripts/test-layers.sh --integration  # 追加真实信令集成段（-p 1 串行）
```

| 层 | 测试命令（脚本内） | 数 |
|----|--------------------|----|
| L1 | `cd back/peerjs && go test ./... -count=1 -race` | 21 |
| L2 | `go test -tags nosqlite ./internal/transport/ -count=1 -skip "^TestAdmin"` | 32 |
| L3 | `go test -tags nosqlite ./internal/transport/ -count=1 -run "^TestAdmin"` | 9 |
| L4 | `go test -tags nosqlite ./internal/controller/... ./internal/service/... ./internal/source/... ./internal/downloader/...` | 94 |
| L5 | `go test -tags nosqlite ./internal/repository/...` | 11 |
| L6 | `go test -tags nosqlite ./internal/signalserver/...` | 6 |
| L7 | `cd back/p2p_bt && go test ./... -count=1` | 7 |
| L8 | `cd front && npm test`（vitest） | 32 |
| INT | `go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1` | — |

细节与独立包（peerdrive-media）验证链见 [doc/testing/README.md](../testing/README.md)。

## 维护约定

- 每份文档保持「职责 → 关键机制 → 坑 → 测试 → 文件清单」结构
- 帧协议/verb 定义以 `doc/REFACTOR.md` §4 为准，本文档树引用不重复定义
- 改代码后若行为变化，同步更新对应模块文档（硬性要求，同 AGENTS.md 编码规范）