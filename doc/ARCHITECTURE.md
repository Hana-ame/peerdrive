# Peerdrive 架构全景

> 基于 `refactor` 79556e3 实读产出。本文档描述当前代码的模块结构、数据流、
> 访问形态与部署分发方式，供新贡献者与协调者快速建立全局认知。
>
> **与其他文档的关系**：`ROADMAP.md` 定义开发顺序；`REFACTOR.md` 记录架构决策
> 与踩坑；`NETDISK.md` 定义网盘链路；本文档描述**现状**——代码里实际有什么。
> 决策原因见对应的设计文档，本文档不重复。

## 目录

- [1. 模块分层](#1-模块分层)
- [2. 主模块 internal 包](#2-主模块-internal-包)
- [3. 数据模型与内容寻址](#3-数据模型与内容寻址)
- [4. 取数链路](#4-取数链路)
- [5. 访问形态](#5-访问形态)
- [6. 数据流](#6-数据流)
- [7. 前端](#7-前端)
- [8. 部署分发](#8-部署分发)

---

## 1. 模块分层

Peerdrive 是一个 monorepo，包含一个 Go 主模块、四个独立子模块、一个前端 SPA
和两个 npm 包。

```
peerdrive/                         ← monorepo 根
├── back/                          ← Go 主模块 (module peerdrive)
│   ├── cmd/peerdrive/             ← 单二进制入口（子命令: serve/signal/reg/all/version）
│   ├── cmd/peersignal/            ← 独立信令二进制（自托管替代 0.peerjs.com）
│   ├── cmd/echclient/             ← ECH 验证工具
│   ├── cmd/media-node/            ← 独立媒体节点
│   ├── internal/                  ← 业务逻辑包（详见 §2）
│   ├── docs/                      ← Swagger 自动生成文档
│   └── test/                      ← 集成测试
├── back/peerjs/                   ← 独立子模块 github.com/Hana-ame/go-peerjs
├── back/signalserver/             ← 独立子模块 github.com/Hana-ame/go-peerserver
├── back/signalframe/              ← 独立子模块 github.com/Hana-ame/go-signalframe
├── back/p2p_bt/                   ← 独立子模块 github.com/Hana-ame/go-peerdrive-bt
├── front/                         ← React SPA (Vite + TailwindCSS + Vitest)
├── packages/
│   ├── peerdrive-client/          ← 零依赖浏览器消费端 (npm)
│   └── peerdrive-media/           ← 媒体加载库 (npm)
└── doc/                           ← 架构决策记录（本文档在此）
```

**子模块关系**：主模块 `go.mod` 的 `replace` 指令将 `github.com/Hana-ame/go-peerjs`
指向本地 `./peerjs`，`github.com/Hana-ame/go-signalframe` 指向 `./signalframe`。
`signalserver` 和 `p2p_bt` 各自独立 `go.mod`，不参与主模块的 `replace`。

每个子模块对应一个独立的 GitHub 仓库（`github.com/Hana-ame/go-peerjs`、
`go-peerserver`、`go-signalframe`、`go-peerdrive-bt`），tag 同步。

## 2. 主模块 internal 包

`back/internal/` 包含 29 个包，按领域分为 7 组：

### 配置与身份

| 包 | 职责 |
|---|---|
| `config` | 全量环境变量读取（`PEERDRIVE_*` 前缀，旧名回退）。默认信令 `0.peerjs.com:9000` |
| `log` | 分级日志（DEBUG/INFO/WARN/ERROR），`LogDuration` 自动记录函数耗时 |
| `nodestate` | 节点运行时身份（peer ID、能力标记） |
| `version` | 版本号 |

### 存储与索引

| 包 | 职责 |
|---|---|
| `hashmap` | 内容寻址核心：`sha256 → 路径/文件信息` 映射。`Sum()` 计算流式 sha256 |
| `repository` | SQLite 持久化：`anon_repo`（合集）、`file_index`（文件索引）、`search`、`sync` |
| `pathutil` | 路径安全：`Within`/`SafeCopy`/`SafeOpenFile`/`SafeRemove`，含 Windows 8.3 短名展开 |
| `model` | 数据模型：`AnonCollection`、`Provider`（sha256/url）、`Level`（public/unlisted/private） |

### 合集与取数

| 包 | 职责 |
|---|---|
| `collection` | 内容寻集合：JSON 文档 → CAS。Entry 支持 `sha`/`ech-url`/`url`/`private.url` 多备选 |
| `source` | 多源取数管理器。`Source` 接口 + `ShaSource`/`URLSource`/`EchSource`/`BTSource` |
| `egress` | 统一消费出口抽象：`ContentProvider` 接口解耦数据源与传输层 |
| `extractor` | 压缩包自动解压（tar/zip/rar/7z） |
| `provider` | 数据源提供者：`ipfs.go`（IPFS 网关提供者：并发试网关取首个成功） |

### 下载与转发

| 包 | 职责 |
|---|---|
| `downloader` | 通用多协议下载管线：sha256 校验、重试、限速 |
| `echcore` | ECH 客户端：域前置，从 ech-proxy 移植 |
| `echproxy` | ECH 隧道代理：twimg/exhentai 域前置配置 |
| `ratelimit` | 令牌桶限流 + 登录退避 |

### 互联与传输

| 包 | 职责 |
|---|---|
| `transport` | 帧协议实现：`req`/`meta`/`data`/`done`（文件）+ `share`/`share-resp`（共享）+ `admin`（管理面）+ `forward`（端口转发）+ `pull`（跨节点拉取） |
| `wsconn` | WebSocket 连接封装 |
| `mcp` | MCP 服务端（stdio，LLM 工具调用） |
| `twitterpic` | Twitter 图片构建器 |

### 服务层

| 包 | 职责 |
|---|---|
| `service` | 业务逻辑：`anon_service`（合集 CRUD）、`collection_service`、`file_service`、`peerpull`（跨节点拉取）、`node_directory`（节点市场）、`nodeshare`（共享范围解析）、`pin_service`、`sync_service`、`share_service` |

### HTTP 与路由

| 包 | 职责 |
|---|---|
| `controller` | HTTP 控制器：`anon`/`coll`/`file`/`peerjs`/`forward`/`admin` |
| `httpd` | 嵌入式 HTTP 服务器 |
| `router` | Gin 路由 + 认证中间件（`AuthOptional`/`AuthRequired`） |
| `panel` | 内嵌公共面板单文件（`go:embed`，二进制自带 UI） |
| `serverapp` | 服务入口：`InitDB → PeerJS → source 注册 → Router` |
| `services` | 单二进制多组件组合（主服务 + 信令 + 注册） |
| `regserver` | 注册认证服务：Bearer token、`/auth/whoami` |

## 3. 数据模型与内容寻址

Peerdrive 的核心是**内容寻址存储（CAS）**：

```
storage/
└── <sha[:2]>/        ← 前 2 位 hex 分桶
    └── <sha>         ← 完整 64 位小写 sha256 作为文件名
```

任何文件（包括合集 JSON）都按此布局存储。合集自身也是一个 CAS 文件——它的
sha256 就是它的地址。

### Collection Entry 格式

每个合集 entry 携带可选的 `source` 对象，定义多备选取数路径：

```json
{
  "path": "photos/2024/sunset.jpg",
  "sha": "abc123...",                    // CAS 地址
  "source": {
    "sha": "abc123...",                  // CAS（主路径）
    "ech-url": "https://ech.example.com/...",  // ECH 域前置
    "url": "https://cdn.example.com/...",       // 普通 HTTP
    "private.url": "https://private.example.com/..."  // 私有
  },
  "metadata": { "name": "sunset.jpg", "mime": "image/jpeg" }
}
```

## 4. 取数链路

`Entry.Fetch` 按优先级尝试备选 source，返回第一个成功的流：

```
优先级：sha → ech-url → url → private.url
```

| Source | 解析方式 | 校验 |
|---|---|---|
| `sha` | CAS 读取（`storage/<sha[:2]>/<sha>`）或经 PeerJS `req` 帧向对端请求 | sha256 |
| `ech-url` | `EchSource`：resolver（hash → CDN URL）+ echcore 隧道取字节 | 可选 |
| `url` | `URLSource`：`fmt.Sprintf` 模板拼 URL | sha256 |
| `private.url` | 同 `url`，不进共享清单 | 可选 |

每次取数记录在 `FetchMonitor`，暴露备选轨迹和来源成功分布。

## 5. 访问形态

| 形态 | 协议 | 典型场景 | 入口 |
|---|---|---|---|
| **本地 HTTP** | HTTP REST | 管理台、文件上传下载 | `GET /anon/collections`、`POST /files/upload` |
| **PeerJS** | WebRTC DataChannel | 跨节点取数、共享清单 | `share`/`share-resp` 帧 + `req` 帧 |
| **WS 管理面** | WebSocket | 管理 verb（admin/admin-resp/admin-bin） | 仅本地 WS 会话，WebRTC 不实现 |
| **外部 HTTP** | HTTP REST | 外部消费、文件预览 | `GET /anon/c/:hash/f/:hash` |

## 6. 数据流

```
摄取：文件 → sha256 计算 → CAS 写入（storage/<sha[:2]>/<sha>）→ file_index 登记
合集：entries[] → collection JSON → 自身 sha256 → CAS 写入（同普通文件）
取数：collection → Entry.Fetch → source.Manager.OpenRange → egress.ContentProvider → 响应流
共享：运营者声明 scope → NodeShare.ShareSnapshot → share 帧 → 对端看到清单 → PeerPuller 拉取
```

## 7. 前端

### 技术栈

React 19 + react-router-dom v7（HashRouter）+ Vite + TailwindCSS + Vitest

### Features 模块

`front/src/features/` 按领域分组，共 8 个 feature：

| Feature | 页面 | 说明 |
|---|---|---|
| `node` | `Connect`, `NodeControl` | 节点连接与面板 |
| `drive` | `Drive` | 网盘主界面（多源文件、预览、上传） |
| `collection` | `Collections`, `CollectionView` | 合集浏览（多备选 source、标签外置索引） |
| `bt` | `BT` | BT 种子管理 |
| `ipfs` | `IPFS` | IPFS 网关 |
| `iwara` | `Iwara` | Iwara 视频 |
| `transfers` | `Transfers` | 传输管理 |
| `settings` | `Settings` | 节点设置 |

### 路由

`HashRouter`（`#/drive/:hash`、`#/collection/:hash` 等），无 SPA fallback
依赖，支持 `file://` 协议和任何静态空间部署。

### 消费端包

- `peerdrive-client`：零运行时依赖，纯浏览器从节点拉文件。单文件面板
  `dist/panel.html` 可直接 `file://` 打开
- `peerdrive-media`：经 PeerJS 信令 + WebRTC DataChannel 从节点加载 URL 资源
  渲染 `<img>`/`<video>`。三入口：React / Vanilla (IIFE+CDN) / Node

## 8. 部署分发

### 二进制

单二进制 `peerdrive`，5 个子命令：

| 子命令 | 说明 |
|---|---|
| `serve` | 起主服务（HTTP + PeerJS + SQLite） |
| `signal` | 只起信令 + 节点发现 |
| `reg` | 只起注册/认证/中继登记 |
| `all` | 三者合并到一个进程、一个端口 |
| `version` | 打印版本号 |

### 信令默认

默认 `0.peerjs.com:9000`（key `"peerjs"`，公共云）。可通过
`PEERDRIVE_PEERJS_HOST/PORT/KEY` 指向自托管 peersignal。

### 前端部署

- Cloudflare Pages（`x.moonchan.xyz`）
- 面板单文件：`packages/peerdrive-client/dist/panel.html`（file:// 可开）

### npm 包

- `peerdrive-client`：零依赖浏览器消费端
- `peerdrive-media`：媒体加载库（npm 依赖 `github:Hana-ame/peerdrive#v0.1.0`）

### 独立仓库镜像

| 仓库 | 模块 | 用途 |
|---|---|---|
| `github.com/Hana-ame/go-peerjs` | `back/peerjs/` | 信令客户端 + WebRTC |
| `github.com/Hana-ame/go-peerserver` | `back/signalserver/` | 自托管信令 + 发现 |
| `github.com/Hana-ame/go-signalframe` | `back/signalframe/` | 帧格式 |
| `github.com/Hana-ame/go-peerdrive-bt` | `back/p2p_bt/` | BT DHT 桥接 |
