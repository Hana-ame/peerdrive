# Peerdrive

> **2026-05-04 · 项目封印**

[![Peerdrive CI](https://github.com/Hana-ame/peerdrive/actions/workflows/ci.yml/badge.svg)](https://github.com/Hana-ame/peerdrive/actions/workflows/ci.yml)

Peerdrive 是一个多协议文件集合管理器，支持 SHA256 内容寻址存储、URL 引用、P2P 传输和 BitTorrent 下载。通过 **Collection + Provider** 的统一抽象，将本地文件、HTTP 资源、IPFS DHT 和 BT DHT 整合到一个系统中。

**项目已封印。** 以下记录设计思路、架构决策和教训。

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
| 存储 | SQLite + 内容寻址文件系统 |
| 前端 | React 19 + Vite 8 + TailwindCSS 3 |

---

## 快速开始

```bash
# 后端
cd back && go run -tags nosqlite ./cmd/server/main.go

# 前端
cd front && npm run dev

# 测试
cd back && go test -tags nosqlite ./... -count=1
cd front && npx vitest run
```

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

---

## 为什么封印

### 1. 经济模型缺失

P2P 网络的核心是激励。BT 靠"下载完自动做种"的互惠，Filecoin 靠合约。Peerdrive 没有激励机制——节点运行只有支出（带宽 + 电费 + 存储），没有回报。

### 2. 运营商 QoS

国内运营商对 P2P 上传有明确的限速和连接数限制。内容寻址 + P2P 传输在有 QoS 的环境里无法落地。

### 3. 与 IPFS 高度重叠

Peerdrive 的 Collection + Provider 抽象与 IPFS 的 CID + Pin 本质上同构。区别只是多了一个 `url` provider 类型——相当于承认了"P2P 不通就走 HTTP"。

### 4. BT 生态式微

DHT 节点从千万级降到百万级。流媒体时代种子分享本身在萎缩。

---

## 留下的东西

> 注意：本段描述的是 2026-05 封印时的历史模块。**libp2p 栈已于 2026-08-16
> 全部删除**（`back/internal/service/p2p.go`、`p2p_dual.go` 等已不存在），当前
> 互联层为 PeerJS/WebRTC，见 `doc/REFACTOR.md`。

| 模块 | 价值 |
|------|------|
| `back/p2p_bt/` | BT DHT 能力（独立库 `github.com/Hana-ame/go-peerdrive-bt`；**README 旧断言「可独立使用」是错的**——依赖桥接层，以 REFACTOR.md 第 6 节为准） |
| `back/internal/provider/` | 多协议文件获取抽象（下载管线） |
| `back/internal/model/anon.go` | Content-addressed collection JSON 格式 |
| `back/internal/transport/peerjs_service.go` | 当前互联层：PeerJS 信令 + WebRTC DataChannel |
| `doc/` | 完整的架构决策和测试记录 |
