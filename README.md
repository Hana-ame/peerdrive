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

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORT` | 3000 | HTTP 端口 |
| `PEERDRIVE_P2P_ENABLE` | true | P2P 网络 |
| `PEERDRIVE_P2P_READ_ONLY` | false | 只读模式（不提供数据） |
| `PEERDRIVE_BT_DHT_ENABLE` | true | BT DHT 网络 |
| `PEERDRIVE_IPFS_COMPAT` | false | IPFS 兼容模式 |
| `PEERDRIVE_RELAY_MODE` | client | 中继模式 |
| `PEERDRIVE_WEBDAV_ENABLE` | true | WebDAV 挂载 |
| `PEERDRIVE_STORAGE` | ./storage | 存储目录 |

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
