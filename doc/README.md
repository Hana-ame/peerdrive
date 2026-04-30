# Peerdrive — P2P 文件分享系统

> 基于内容寻址 + Git 风格版本管理的 P2P 文件分享 · v3.0

Peerdrive 通过 SHA256 哈希标识文件实现自动去重，支持合集（Collection）的版本管理（Commit/Rollback/Fork/Pull/Merge），以及跨节点 P2P 同步。存储后端支持本地文件和 HTTP 源两种 Provider 模式。

## 架构

```
HTTP API (Gin) → Service → Provider / Repository (SQLite)
                    ↕
        libp2p / BT DHT / IPFS / WebRTC
```

| 层 | 说明 |
|---|---|
| **Controller** | HTTP 请求处理、参数校验 |
| **Service** | 业务逻辑（文件注册/下载、合集管理、P2P 传输/信令） |
| **Provider** | 数据源抽象（本地文件 / 远程 URL） |
| **Repository** | SQLite 持久化 |

## 核心概念

| 概念 | 类比 |
|---|---|
| **Collection** | Git Repository — 文件合集 |
| **CollectionEntry** | Git Tree — `path → [{type, value, mime_type}]` Provider 列表 |
| **CollectionVersion** | Git Commit — 版本快照（SHA256 标识） |
| **匿名合集** | 无需注册的临时合集，通过 SHA256 哈希直接访问 |
| **Provider** | 文件来源：`sha256`（内容寻址）或 `url`（HTTP 源） |

## 模块

| 模块 | 说明 |
|------|------|
| **文件管理** | 上传、注册本地/文件夹/URL、SHA256 下载、MIME 嗅探 |
| **合集** | 匿名合集 + 命名合集、Commit/Rollback/Log、Fork/Pull/Merge |
| **P2P** | libp2p 节点发现、连接管理、Ping 延迟、双栈支持 |
| **BT DHT** | Mainline DHT 接入、BEP44 put/get、torrent/magnet 下载 |
| **IPFS** | IPFS 兼容层、CID pin/取消、网关发现 |
| **WebRTC** | 信令服务、P2P 直连传输 |
| **断点续传** | 多对等点分片下载、进度追踪、暂停/恢复 |
| **WebSocket** | 实时传输通道、信令交换 |
| **转发代理** | NAT 穿透转发、Relay 代理下载 |

## API 概览

完整 API 参考见 **[spec/API-REFERENCE.md](spec/API-REFERENCE.md)**（105 端点，Swagger 生成）。

| 路径 | 说明 |
|------|------|
| `GET /ping` | 健康检查 |
| `POST /files/upload` | 上传文件 |
| `POST /files/register_local` | 注册本地文件路径 |
| `POST /files/register_url` | 注册 URL 文件源 |
| `POST /files/register_folder` | 递归注册文件夹 |
| `GET /sha256sum/:sha256` | 通过 SHA256 下载文件 |
| `GET /download/:hash` | Universal Downloader（多源） |
| `GET /ipfs/:cid` | IPFS CID 下载 |
| `POST /anon/collections` | 创建匿名合集 |
| `GET /anon/collections/:hash` | 读取匿名合集 |
| `GET /anon/collections/:hash/*filepath` | 下载匿名合集文件 |
| `POST /api/v1/collections/:user/:coll/entries` | 添加合集条目 |
| `POST /api/v1/collections/:user/:coll/commit` | 提交版本 |
| `GET /api/v1/collections/:user/:coll/log` | 版本历史 |
| `POST /api/v1/actions/fork` | Fork 远程合集 |
| `POST /api/v1/actions/merge` | 三路合并 |
| `GET /p2p/node` | 本节点信息 |
| `GET /p2p/peers` | 已连接节点 |
| `POST /p2p/bt/bep44/get` | BT DHT BEP44 查询 |
| `GET /p2p/ws/info` | WebSocket 传输信息 |
| `GET /p2p/webrtc/info` | WebRTC 信令信息 |
| `GET /swagger/*any` | Swagger UI |

## 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `PORT` | 服务端口 | `3000` |
| `PEERDRIVE_P2P_ENABLE` | 启用 P2P | `true` |
| `PEERDRIVE_BT_DHT_ENABLE` | 启用 BT DHT | `true` |
| `CORS_MODE` | CORS 模式（all/localhost） | 白名单 |
| `VITE_API_BASE` | 前端 API 地址 | `http://localhost:3000` |

## 快速开始

```bash
# 启动后端
cd go && go run ./cmd/server/
# 服务启动在 :3000

# 启动前端
cd react && npm run dev
# Vite 开发服务器在 :5173

# 运行测试
cd go && go test ./... -count=1    # 单元测试
bash test/e2e-all.sh               # E2E 全端点测试
bash test/p2p.sh                   # P2P 双节点测试
```

## 文档导航

| 要做什么 | 去这里 |
|----------|--------|
| 查找 API 接口 | [spec/API-REFERENCE.md](spec/API-REFERENCE.md) — 105 端点完整参考 |
| 了解合集逻辑 | [spec/COLLECTION-LOGIC.md](spec/COLLECTION-LOGIC.md) — 合集完整追踪 |
| 阅读需求总表 | [spec/REQUIREMENTS.md](spec/REQUIREMENTS.md) — 130+ 需求 |
| 找代码对应的文档 | [CODE-DOC-MAPPING.md](CODE-DOC-MAPPING.md) — 代码↔文档映射 |
| 运行测试 | [testing/index.md](testing/index.md) — 测试文档门户 |
| 部署到 VPS | [guide/VPS_DEPLOY.md](guide/VPS_DEPLOY.md) — 生产环境部署 |
| 配置 LLM 助手 | [guide/siliconflow-setup.md](guide/siliconflow-setup.md) — SiliconFlow 配置 |
| 查看全部文档 | [INDEX.md](INDEX.md) — 完整文档索引 |
| 查看报告 | [report/index.md](report/index.md) — 报告目录 |
| 了解当前状态 | [DASHBOARD.md](DASHBOARD.md) — 项目仪表盘 |
