# Peerdrive 系统设计文档

## 1. 概述
Peerdrive 是一个基于内容寻址（Content-Addressable Storage, CAS）的 P2P 文件共享与分发系统。它允许用户通过文件的 SHA256 哈希值唯一标识内容，并提供一个将这些内容组织为"合集（Collection）"的机制。系统支持多副本冗余、P2P 自动回退下载以及基于快照的版本控制。

## 2. 核心架构

### 2.1 内容寻址存储 (CAS)
- **唯一标识**：所有文件使用其内容的 SHA256 哈希值作为唯一 ID。
- **解耦存储**：通过元数据层将"内容标识（Hash）"与"物理位置（Path/URL）"解耦。
- **详细分析** → [sha256-download.md](./sha256-download.md)

### 2.2 多副本元数据模型
采用分层存储模型：
- **`file_meta` 表**：存储内容的固有属性（大小、MIME 类型、是否 gzip 压缩、文件类型）。每种唯一内容仅有一条记录。
- **`file_providers` 表**：存储该内容的多个物理副本位置。记录包括提供者类型（`local` 或 `http`）和可用性状态（`available`）。
- **副本策略**：下载时循环尝试所有可用副本，若某副本失效则标记为不可用。
- **数据库定义** → [database.md](./database.md)

### 2.3 P2P 下载回退机制
系统实现分级下载策略：
1. **本地存储** → 2. **远程 HTTP 副本** → 3. **P2P Bitswap 网络**
- 当所有已知副本均失效时，系统通过 libp2p 协议在网络中广播请求。
- 成功拉取的数据将自动缓存至本地，并更新元数据记录。
- **实现细节** → [sha256-download.md](./sha256-download.md)

## 3. 合集系统 (Collection System)

### 3.1 匿名合集 (Anonymous Collection)
- **本质**：一个包含 `path` → `hash` 映射关系的 JSON 文件。
- **特性**：不可变（Immutable）。一旦生成，其内容决定了其 SHA256 哈希值。
- **存储**：合集 JSON 存储在 `storage/{hash[:2]}/{hash}`，`file_meta` 中 `type` 为 `anon_collection`。
- **校验**：路径必须相对且不含 `..`，hash 必须为 64 位十六进制。
- **详细分析** → [anon-collection.md](./anon-collection.md)
- **指针机制**：用户合集在数据库中是一个可变记录，通过 `current_hash` 字段指向匿名合集快照。
- **版本控制**：
  - **Commit**：将当前工作区状态序列化为匿名合集 → 存储 → 更新 `current_hash` → 记录版本历史。
  - **Rollback**：将 `current_hash` 指向历史版本快照。
  - **Fork/Merge**：基于匿名快照的不可变性，实现合集的分支与合并。
- **实现逻辑** → [anon-collection.md](./anon-collection.md) | **数据结构** → [database.md](./database.md)

## 4. 文件导入与上传

### 4.1 文件上传 (Upload)
- 通过 HTTP Multipart 上传文件流。
- 使用临时文件计算 SHA256 哈希、检测 MIME 类型、记录文件大小。
- 文件存储到 `storage/{hash[:2]}/{hash}`。
- 重复文件检测：返回 `already_exists` 标识（HTTP 200），不重复写入磁盘。
- 可通过 `PEERDRIVE_STORAGE_ENABLE` 环境变量禁用写操作（返回 403）。
- **详细分析** → [upload.md](./upload.md)

### 4.2 文件注册 (Register)
- 直接引用本地已存在的文件路径，无需复制。
- 支持绝对路径（任意文件系统位置）和相对路径。
- 自动计算并存储 `Size`、`MimeType`、SHA256 Hash。
- 幂等性保证：重复注册相同文件返回相同 hash。
- **注册分析** → [register.md](./register.md)

## 5. 配置系统

通过环境变量进行配置：
| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORT` | `3000` | HTTP 监听端口 |
| `PEERDRIVE_STORAGE` | `./storage` | 文件存储根目录 |
| `PEERDRIVE_STORAGE_ENABLE` | `true` | 写操作开关（设为 false 时返回 403） |
| `PEERDRIVE_P2P_ENABLE` | `true` | 启用/禁用 P2P 网络 |
| `PEERDRIVE_P2P_LISTEN` | `/ip4/0.0.0.0/tcp/0` | libp2p 监听地址 |
| `PEERDRIVE_BOOTSTRAP_PEER` | (空) | DHT bootstrap 节点 multiaddr |
| `PEERDRIVE_MDNS_ENABLE` | `true` | LAN mDNS 节点发现 |
| `PEERDRIVE_RELAY_MODE` | `client` | 中继模式: off / client / server |
| `PEERDRIVE_STATIC_RELAYS` | (空) | 逗号分隔的静态中继地址 |
| `PEERDRIVE_HOLE_PUNCH` | `true` | 启用 STUN 打洞 |
| `PEERDRIVE_AUTO_NAT` | `true` | 自动 NAT 类型检测 |
| `PEERDRIVE_NAT_PORTMAP` | `false` | UPnP/NATPMP 端口映射 |
| `PEERDRIVE_PUBLIC_REACHABLE` | `false` | 标记节点为公网可达 |
- **配置分析** → `internal/config/config.go`

## 6. 技术栈
- **后端**: Go, libp2p, SQLite3, Gin
- **前端**: React (JSX), Tailwind CSS, Vite

## 7. 架构分层
```
cmd/server/main.go     — 入口点
internal/router/        — 路由注册
internal/controller/    — HTTP 请求/响应处理
internal/service/       — 业务逻辑层
internal/repository/    — 数据访问层
internal/model/         — 数据结构定义
internal/provider/      — 内容寻址读取（local/http）
internal/config/        — 配置管理
```

## 8. API 逻辑概览
| 路径 | 说明 |
|------|------|
| `GET /ping` | 健康检查 |
| `GET /sha256sum/:hash` | 基于 CAS 的文件下载（含 P2P 回退） |
| `POST /files/upload` | 上传文件 |
| `POST /files/register_local` | 注册本地文件 |
| `POST /files/register_folder` | 注册文件夹 |
| `GET /files/verify/:hash` | 验证文件元数据 |
| `DELETE /files/:hash` | 删除文件 |
| `POST /anon/collections` | 创建匿名合集 |
| `GET /anon/collections/:hash` | 获取匿名合集 JSON |
| `GET /anon/collections/:hash/entries/*path` | 从匿名合集下载文件 |
| `POST /anon/collections/fork` | 复刻匿名合集 |
| `POST /collections` | 创建用户合集 |
| `GET /collections/:username` | 列出用户合集 |
| `POST /collections/:username/:coll/entries` | 添加条目 |
| `POST /collections/:username/:coll/commit` | 提交版本 |
| `POST /collections/:username/:coll/rollback/:vid` | 回滚版本 |
| `POST /actions/fork` | 复刻合集 |
| `POST /actions/merge` | 合并合集 |
| `POST /actions/pull` | 拉取更新 |
| `GET /tasks` | 任务列表 |
| `GET /p2p/status` | P2P 状态（含 relay/hole_punch/ws） |
| `GET /p2p/node` | P2P 节点信息 |
| `GET /p2p/peers` | 已连接对等点 |
| `GET /p2p/discovered` | mDNS/DHT 发现的节点 |
| `GET /p2p/ping/:peer_id` | Ping 对等点 |
| `POST /p2p/connect` | 手动连接对等点 |
| `POST /p2p/announce` | 声明拥有文件 hash |
| `POST /p2p/fetch` | 从 P2P 拉取匿名合集 |
| `POST /p2p/sync` | 从对等点全量同步合集 |
| `POST /p2p/push` | 向对等点推送合集 |
| `POST /p2p/request-file` | 广播文件请求（P2P） |
| `GET /p2p/ws/info` | WebSocket 连接信息 |
| `GET /ws/transfer` | WebSocket 文件传输端点 |

## 9. 测试
- **注册/下载测试**：`bash test/register.sh`
- **上传测试**：`bash test/upload.sh`
- **匿名合集测试**：`bash test/anon-collection.sh`
- **P2P Stage 2 测试**：`bash test/p2p.sh`
- **P2P Stage 3 中继测试**：`bash test/relay.sh`
- **测试说明** → `docs/testing/index.md`
