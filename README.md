# Peerdrive
[![Peerdrive CI](https://github.com/Hana-ame/peerdrive/actions/workflows/ci.yml/badge.svg)](https://github.com/Hana-ame/peerdrive/actions/workflows/ci.yml)

Peerdrive 是一个多协议文件集合管理器，支持 SHA256 内容寻址存储、URL 引用、P2P 传输和 BitTorrent 下载。通过 **Collection + Provider** 的统一抽象，将本地文件、HTTP 资源、PeerJS/WebRTC 互联整合到一个系统中。

---

## 核心理念

```
Collection = 名称 + 条目[]
Entry     = 路径 + Provider[]
Provider  = { type: "sha256" | "url", value: hash | url }
```

剥离独立的"文件"概念——一切皆合集。文件 = 单 entry 的 collection + sha256 provider。

### 双 DHT 架构

SHA256 hash 转为 CIDv1 在 IPFS DHT 上 announce，同时作为 infohash 在 BT DHT 上 announce。双栈查询合并两个网络的结果。

### 关键技术栈

| 层 | 技术 |
|----|------|
| HTTP | Gin |
| P2P | PeerJS 信令 + WebRTC DataChannel（`back/peerjs/` go-peerjs；发现：MQTT / 自托管 HTTP） |
| BT | `github.com/Hana-ame/go-peerdrive-bt`（back/p2p_bt，独立库） |
| 管理面 | 本地 WS admin verb（前端全走 `front/src/ws.js`） |
| 消费端 | `packages/peerdrive-client`（零依赖纯浏览器消费端，走 `share`/`req` 帧；`packages/peerdrive-media` 面向 img/video 的 URL 代理） |
| 存储 | SQLite + 内容寻址文件系统 |
| 前端 | React 19 + Vite 8 + TailwindCSS 3 |

---

## 网盘链路（2026-09，`doc/NETDISK.md`）

把「互联 + 文件」串成一条用户能看懂的链路，对应开发顺序里 ROADMAP 的阶段 5/6：

```
我的节点 ──加入──▶ 节点市场 ──直连──▶ 对方共享的"文件链接" ──选中保存──▶ 我的网盘
                                                        （传输任务：进度 / 取消）
```

| 环节 | 实现 |
|---|---|
| 节点市场与加入 | `service.NodeDirectory` + `GET /peerjs/nodes*`；已加入清单持久化（`joined_nodes.json`）并成为常驻对端 |
| 共享范围 | `share` 帧 + `PEERDRIVE_SHARE_ENABLE/COLLECTIONS/DIRS`（**默认全部关闭**，不声明就不对外暴露任何清单） |
| 跨节点保存 | `service.PeerPuller` + `GET/POST /p2p/pull*`：流式拉取 → sha256 校验 → 落盘 → 登记文件索引，带进度/取消/去重跳过 |
| 网盘界面 | `front/src/pages/{Drive,Market,Peers,PeerDetail,Transfers}`（信息架构参考 Nextcloud Files / Cloudreve / Alist） |
| 无节点消费端 | `packages/peerdrive-client`：一个静态页面直连节点拉文件，不需要本地后端 |

进度、模块拆分、**计划与实际偏差**、验证结果都在 `doc/NETDISK.md`；开发顺序见 `doc/ROADMAP.md`。

---

## 快速开始

```bash
# 后端
cd back && go run -tags nosqlite ./cmd/server/main.go

# 前端
cd front && npm run dev

# 消费端 demo（无后端）
cd packages/peerdrive-client && npm run demo   # http://127.0.0.1:8123/demo/consumer.html

# 一键跑整套网盘链路（自托管信令 + 两个节点，自动验证市场/加入/清单/拉取/校验）
./scripts/netdisk-local-demo.sh                # --stop 停止

# 测试
cd back && go test -tags nosqlite ./... -count=1                                     # 308
cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1    # 21（脱外网，自托管信令；必须 -p 1）
cd back/signalserver && go test ./...            # 21（独立 go.mod，无 CI job）
cd back/p2p_bt && go test ./...                  # 7（独立 go.mod，无 CI job）
cd front && npx vitest run                       # 88
cd packages/peerdrive-client && npm test         # node --test（零依赖）60
bash scripts/test-layers.sh                      # 或按 AOP 分层（L1-L8 + LB）逐层跑

# 全部 14 个测试组件的清单、选型与盲区：doc/testing/README.md
```

## 信令服务器实现方式

> 信令服务器可以用**多种方式实现**，只要兼容 PeerJS 协议即可：
> 公共 PeerJS 云、自托管 Go 信令（wintools / `back/signalserver`）、
> Node.js `peerjs-server` 等。当前线上使用 wintools 维护的 Go 自托管信令，
> peerdrive 通过 `PEERDRIVE_PEERJS_HOST/PORT/KEY` 和 `PEERDRIVE_DISCOVER_URL`
> 连接信令；如果需要在 peerdrive 内嵌信令，`back/signalserver` 也是可用的 Go 实现。

```
wintools 或任何 PeerJS 兼容信令（独立部署）
│  /peerjs            ← PeerJS 兼容 WS 信令
│  /discover/announce ← Go/Web 节点上线自报
│  /discover/nodes    ← 节点发现
└───────────────┬────────────────────────────
                │ 仅转发 SDP/ICE，不碰数据面
┌───────────────▼────────────────────────────
peerdrive
│  back/peerjs  (Go PeerJS 客户端 + WebRTC DataChannel)
│  back/internal/transport/peerjs_service.go (文件服务/节点互联)
│  front        (浏览器 peerjs 消费者)
```

- Go 节点：用 `back/peerjs` 连接信令，常驻在线，提供本地文件 `list/read`。
- Web 端：浏览器 `peerjs` 连接同一信令，按需连接 Go 节点，消费文件。
- 信令实现选择：
  1. 使用线上/本地 wintools Go 信令（当前默认）
  2. 使用 peerdrive `back/signalserver` 内嵌或独立运行
  3. 使用公共 PeerJS 云（`0.peerjs.com`）
  4. 使用 Node.js 或其他 PeerJS 兼容信令

## 环境变量

> 完整配置见 `back/internal/config/config.go`（`PEERDRIVE_*` 前缀，未设置用默认值）。

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORT` | 3000 | HTTP 端口 |
| `PEERDRIVE_STORAGE` | ./storage | 存储目录（内容寻址文件） |
| `PEERDRIVE_STORAGE_ENABLE` | true | 存储启用 |
| `PEERDRIVE_AUTH_TOKEN` | - | 节点认证 token（HTTP 管理面） |
| `PEERDRIVE_MAX_UPLOAD_BYTES` | 100MB | 单文件上传上限 |
| `PEERDRIVE_MAX_UPLOAD_ANON_BYTES` | 10MB | 匿名上传上限 |
| `PEERDRIVE_BT_DHT_ENABLE` / `PEERDRIVE_BT_DHT_LISTEN` | true / :6881 | BT DHT（独立库 go-peerdrive-bt） |
| `PEERDRIVE_IPFS_GATEWAY_ENABLE` / `PEERDRIVE_IPFS_GATEWAYS` | true / 三网关 | IPFS 网关兜底 |
| `PEERDRIVE_WEBRTC_STUN` / `PEERDRIVE_WEBRTC_TURN` | stun.l.google.com / - | ICE 服务器 |
| `PEERDRIVE_PEERJS_ENABLE` | true | PeerJS 信令（互联层） |
| `PEERDRIVE_PEERJS_HOST/PORT/KEY` | 0.peerjs.com/443/peerjs | 信令服务器（可指向自托管 peerserver） |
| `PEERDRIVE_PEERJS_ID` | 随机生成 | 节点 peer id |
| `PEERDRIVE_PEERJS_SECURE` | true | 信令 wss |
| `PEERDRIVE_PEERJS_PEERS` | - | 逗号分隔对端自动互联 |
| `PEERDRIVE_MQTT_ENABLE` / `PEERDRIVE_MQTT_BROKER` | false / tcp://broker.emqx.io:1883 | MQTT 分片房间发现 |
| `PEERDRIVE_MQTT_TOPIC_PREFIX` / `PEERDRIVE_MQTT_COLLECTIONS` | peerdrive/v1 / - | MQTT topic 前缀 / 关注集合 |
| `PEERDRIVE_DISCOVER_URL` | - | 自托管发现 API（优先于 MQTT） |
| `PEERDRIVE_URL_SOURCE_TEMPLATE` | - | URL 源模板（%s=hash，多源兜底） |
| `PEERDRIVE_DOWNLOAD_DIR` | ./downloads | 下载/登记目录（file_index 根） |
| `PEERDRIVE_MAX_PEERS` | 8 | 互联对端上限 |
| `PEERDRIVE_DOWNLOAD_ORDER` / `PEERDRIVE_DOWNLOAD_TIMEOUT` | local,ipfs,ipfsgw,btdht,http / 30s | 下载器路由顺序 / 超时 |
| `PEERDRIVE_FORWARD_RULES` | - | 端口转发规则（`key:port,...`，chmod 600） |
| `PEERDRIVE_DISCOVER_PRESENCE` | true | 节点级「存在房间」发现（零共享 collection 的节点也能互联） |
| `PEERDRIVE_SHARE_ENABLE` | **false** | 对外共享总开关。默认关——不显式开启就不对外暴露任何清单 |
| `PEERDRIVE_SHARE_COLLECTIONS` | - | 共享合集：逗号分隔 hash 或 `all`（受限/私有一律跳过） |
| `PEERDRIVE_SHARE_DIRS` | - | 共享目录：逗号分隔。空 = 不共享文件（文件只回 basename，不回绝对路径） |


## 留下的东西

> 注意：以下列为早期遗留模块。**libp2p 栈已于 2026-08-16
> 全部删除**（`back/internal/service/p2p.go`、`p2p_dual.go` 等已不存在），当前
> 互联层为 PeerJS/WebRTC，见 `doc/REFACTOR.md`。

| 模块 | 价值 |
|------|------|
| `back/p2p_bt/` | BT DHT 能力（独立库 `github.com/Hana-ame/go-peerdrive-bt`；**README 旧断言「可独立使用」是错的**——依赖桥接层，以 REFACTOR.md 第 6 节为准） |
| `back/internal/provider/` | 多协议文件获取抽象（下载管线） |
| `back/internal/model/anon.go` | Content-addressed collection JSON 格式 |
| `back/internal/transport/peerjs_service.go` | 当前互联层：PeerJS 信令 + WebRTC DataChannel |
| `doc/` | 完整的架构决策和测试记录 |
