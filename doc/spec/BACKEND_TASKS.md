# 后端功能文档

> 最后更新: 2026-04-27

---

## 架构概览

```
cmd/server/main.go          — 入口：加载配置 → 初始化组件 → 启动 HTTP
  │
  ├── config/config.go      — 环境变量配置
  ├── router/router.go      — 路由注册（22+ 路由）
  ├── controller/           — HTTP 层：参数校验、调用 service、返回 JSON
  │   ├── file.go, collection.go, p2p.go, anon.go, sync.go, fork.go, merge.go, auth.go
  ├── service/              — 业务逻辑层
  │   ├── file_service.go   — 文件上传/注册/验证/删除
  │   ├── p2p.go            — libp2p 节点、Exchange/Announce/Request 协议
  │   ├── p2p_connection.go — 连接管理（心跳/重连）
  │   ├── p2p_transfer.go   — 分片传输（256KB chunk/8并发）
  │   ├── p2p_ws.go         — WebSocket 传输
  │   ├── downloader.go     — 内容寻址下载（local → P2P 降级）
  │   ├── anon_service.go   — 匿名集合 CRUD
  │   ├── sync_service.go   — 本地同步
  │   └── auth_service.go   — 用户认证
  ├── provider/             — 存储后端抽象
  │   ├── local.go          — 本地文件
  │   └── http.go           — 远程 HTTP
  └── repository/           — SQLite 数据访问
      ├── file_repo.go, collection_repo.go, anon_repo.go, sync_repo.go, task_repo.go, user_repo.go
```

---

## 🔴 P1 - 用户重复抱怨

### 🔴 P1.1 文件浏览 `/files/browse` 301 重定向问题

**抱怨来源**: 游览文件系统.txt

```
[GIN-debug] redirecting request 301: /files/browse/ → /files/browse/?path=%2F
```

路由: `GET /files/browse?path=<dir>`
Handler: `controller.BrowseDir` → `FileService.BrowseDir(dirPath)`

**问题**: 某些请求携带尾部 `/` 导致 Gin 301 重定向。已在 `router.go` 设置:
```go
r.RedirectTrailingSlash = false
r.RedirectFixedPath = false
```
**需验证是否生效**。如果仍有问题，需要额外添加 `/files/browse/` 路由。

**测试**:
```bash
curl -v http://127.0.0.1:3000/files/browse/?path=/
# 应返回 200 + JSON，不是 301
```

### 🔴 P1.2 Windows 路径兼容

**抱怨来源**: 游览文件系统.txt（"不一定运行在linux下面"）

- `BrowseDir` 中使用 `filepath.Join` 处理路径（跨平台安全）
- `DefaultRootPath()` 已在 Windows 下返回 `C:\`
- `storageDir` 也应使用 `filepath` 处理

**当前状态**: 已使用 `filepath.IsAbs`、`filepath.Join`。**需 Windows 环境验证**。

---

## 各接口详情

### 系统

| 方法 | 路径 | Handler | 说明 | 测试点 |
|------|------|---------|------|--------|
| GET | `/ping` | `controller.Ping` | 健康检查 → `"pong"` | `curl /ping` → 200 |

### 文件管理

#### GET `/files?sort=time`

列出所有已注册文件。sort 参数: `time`, `name`, `size`, `type`, `path`。

**返回**: `[{hash, filename, size, mime_type, created_at, provider_type, provider_path}]`

**测试**: `curl /files` → 200 + JSON 数组

#### POST `/files/upload`

Multipart 文件上传。

**请求**: `multipart/form-data`, field `file`
**返回**: `{hash, filename, size}`
**流程**: 接收文件流 → 计算 SHA256 → 写入 storage → 注册到 DB

**测试**:
```bash
echo "test" > /tmp/t.txt
curl -X POST http://127.0.0.1:3000/files/upload -F "file=@/tmp/t.txt"
# → {"hash":"...","filename":"t.txt","size":5}
```

#### POST `/files/register_local`

注册本地文件路径。

**请求**: `{path: "/abs/path/to/file", filename: "可选"}`
**流程**: 读取本地文件 → 计算 SHA256 → 注册到 DB
**测试**: 指向存在的文件 → 200 + `{hash, filename}`

#### POST `/files/register_folder`

递归注册文件夹。

**请求**: `{folder_path: "/abs/path/"}`
**返回**: `{registered: [{hash, filename, path}], count: N}`
**安全**: 需要路径穿越检测 (`../` 拒绝)

**测试**:
```bash
curl -X POST /files/register_folder -H 'Content-Type: application/json' \
  -d '{"folder_path":"/tmp/testdir"}'
# → 200 + registered 数组
```

#### GET `/files/verify/:hash`

验证文件完整性。

**返回**: `{hash, filename, size, mime_type, exists: true/false, consistent: true/false}`
**测试**: 用已知 hash → 验证返回 exists:true, consistent:true

#### DELETE `/files/:hash`

删除文件元数据和 provider 记录。**不删除物理文件**。

**测试**: `curl -X DELETE /files/<hash>` → 200

#### GET `/files/browse?path=/`

浏览服务器文件系统（不限于已注册文件）。

**返回**: `[{name, path, is_dir, size, mod_time}]`
**安全**: 仅返回目录内容，不遍历符号链接
**默认路径**: Linux `/`, Windows `C:\`

---

### 匿名合集

#### POST `/anon/collections`

创建匿名合集（内容寻址，不可变）。

**请求**: `{entries: [{path, hash}], friendly_name: "名称", tags: ["tag1"]}`
**流程**: 验证条目 → JSON 序列化 → SHA256 哈希 → 写入 storage → 返回 hash
**安全**: 
- 拒绝 path 为空、含 `../`、绝对路径
- hash 必须 64 位 hex

**测试**:
```bash
curl -X POST /anon/collections -H 'Content-Type: application/json' -d '{
  "entries":[{"path":"f.txt","hash":"<real_hash>"}],
  "friendly_name":"测试合集"
}'
# → {"hash":"<64-hex>"}
```

#### GET `/anon/collections`

列出所有匿名合集。

**返回**: `[{hash, friendly_name, name_preview, version, entry_count, tags, created_at}]`

#### GET `/anon/collections/:hash`

获取单个匿名合集的全部内容。

**返回**: `{hash, friendly_name, name_preview, entries: [{path, hash}], tags, created_at}`

#### GET `/anon/collections/:hash/*filepath`

下载合集内的单个文件。

**流程**: 根据合集 hash 加载 JSON → 找到 entry → 通过 hash 获取文件内容 → 流式返回

#### POST `/anon/collections/fork`

基于现有合集创建分支。

**请求**: `{source_hash, add_entries: [], remove_paths: [], friendly_name: ""}`
**流程**: 加载源合集 → 添加新条目 → 移除指定路径 → 序列化为新合集

#### POST `/anon/collections/commit`

提交新版本的合集。

**请求**: `{source_hash, entries: [], commit_message: ""}`
**流程**: 创建新的 AnonCollection → 新 hash

---

### 用户合集

#### POST `/collections`

创建用户合集。

**请求**: `{username, collection_name, visibility: "public"|"unlisted"|"private", tags: []}`
**返回**: `{id, username, collection_name, visibility}`

#### GET `/collections/:username`

列出用户的所有合集。

#### GET `/collections/:username/:collection_name`

获取合集详情（含条目列表）。

**返回**: `{id, username, collection_name, entries: [{path, file_hash}], current_hash, visibility, tags}`

#### POST `/collections/:username/:collection_name/entries`

添加条目到合集。

**请求**: `{path: "dir/file.txt", hash: "<sha256>"}`
**注意**: 允许重复 path（不同 hash），去重是 SHA256 文件层的事

#### DELETE `/collections/:username/:collection_name/entries/*path`

删除合集条目。

#### POST `/collections/:username/:collection_name/commit`

提交新版本。

**请求**: `{commit_message: ""}`
**流程**: 冻结当前工作区为快照 → 生成版本记录

#### GET `/collections/:username/:collection_name/log`

版本历史。

#### POST `/collections/:username/:collection_name/rollback/:version_id`

回滚到指定版本。**覆盖当前工作区**。

#### GET `/collections/public` & `/collections/search`

列出公开合集 / 搜索合集。

---

### P2P

#### GET `/p2p/status`

P2P 状态摘要。

**返回**:
```json
{
  "enabled": true,
  "peer_id": "12D3...",
  "addrs": ["/ip4/..."],
  "connected_count": 3,
  "discovered_count": 5,
  "relay_mode": "client",
  "hole_punch": true,
  "ws_connections": 1,
  "conn_stats": {"known_peers": 5, "successful_conns": 3, "failed_conns": 1},
  "active_transfers": [{"hash": "...", "progress": 45.2, "done": false}]
}
```

#### GET `/p2p/node`

本节点 Peer ID 和地址。

#### GET `/p2p/peers` / `/p2p/discovered`

已连接/已发现节点列表。

#### POST `/p2p/connect`

手动连接节点。

**请求**: `{addr: "/ip4/1.2.3.4/tcp/4001/p2p/12D3..."}`

#### POST `/p2p/announce`

向 DHT 宣告拥有某文件。

**请求**: `{hash: "<sha256>"}`

#### POST `/p2p/fetch`

从 P2P 网络获取合集。

**请求**: `{hash: "<sha256>"}`
**流程**: DHT 查找 provider → exchange 协议获取数据 → 解析 JSON → 返回合集

#### POST `/p2p/sync`

从指定节点同步文件到本地。

**请求**: `{peer_id, hash, target_dir: "/path/"}`
**流程**: 获取合集 → 下载所有文件 → 写入本地存储

#### POST `/p2p/push` / POST `/p2p/request-file`

推送合集 / 广播文件请求。

#### GET `/ws/transfer`

WebSocket 升级端点（用于浏览器节点文件传输）。

**协议**: JSON 消息 + 二进制帧
- Client → Server: `{"type":"request","hash":"..."}`
- Server → Client: `{"type":"response","hash":"...","size":N}` + 二进制数据

---

### 协作操作

#### POST `/actions/fork`

Fork 合集到本地。

**请求**: `{username, source_username, collection_name, source_coll_name}`

#### POST `/actions/merge`

合并两个合集。

**请求**: `{username, source_username, collection_name, source_coll_name, strategy: "ours"|"theirs"|"manual"}`

**策略**:
- `ours`: 冲突时保留本地 → 乐观合并
- `theirs`: 冲突时采用远端 → 覆盖合并
- `manual`: 冲突时报错 → 强制手动处理

#### POST `/actions/pull`

从上游拉取更新。

---

### 本地同步

#### POST `/local/save`

保存合集文件到本地磁盘。

**请求**: `{collection_hash, local_path, include: ["*.go"], exclude: ["node_modules"]}`

#### GET `/local/status/:hash`

查询同步状态。

---

### 任务

#### GET `/tasks` / GET `/tasks/:id`

查询异步传输任务（Fork/Pull）状态。

---

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORT` | 3000 | HTTP 端口 |
| `PEERDRIVE_STORAGE` | ./storage | 存储目录 |
| `PEERDRIVE_STORAGE_ENABLE` | true | 是否启用存储 |
| `PEERDRIVE_ALLOWED_ORIGINS` | localhost:5173,... | CORS 白名单 |
| `PEERDRIVE_PUBLIC_DOMAIN` | "" | 公网域名 |
| `PEERDRIVE_P2P_ENABLE` | true | P2P 开关 |
| `PEERDRIVE_P2P_LISTEN` | /ip4/0.0.0.0/tcp/0 | P2P 监听 |
| `PEERDRIVE_BOOTSTRAP_PEER` | "" | 引导节点 |
| `PEERDRIVE_MDNS_ENABLE` | true | mDNS |
| `PEERDRIVE_RELAY_ENABLE` | false | 中继 |
| `PEERDRIVE_RELAY_MODE` | client | 中继模式 |
| `PEERDRIVE_STATIC_RELAYS` | "" | 静态中继 |
| `PEERDRIVE_HOLE_PUNCH` | true | NAT 打洞 |
| `PEERDRIVE_AUTO_NAT` | true | AutoNAT |
| `PEERDRIVE_NAT_PORTMAP` | false | NAT-PMP |

---

## 数据库表

| 表 | 关键字段 | 用途 |
|----|---------|------|
| file_meta | hash(PK), size, filename, mime_type | 文件元数据 |
| file_providers | hash(FK), provider_type, path, available | 文件存储位置 |
| collections | username, collection_name, current_hash | 用户合集 |
| collection_entries | collection_id(FK), path, file_hash | 合集工作区 |
| collection_versions | id, collection_id(FK), snapshot_data, message | 版本快照 |
| version_entries | version_id(FK), path, file_hash | 版本条目 |
| transfer_tasks | id, type, status, params | 异步任务 |
| users | username, password_hash, authkey | 用户认证 |
| local_collection_sync | collection_hash, local_path | 本地同步 |
| local_sync_files | collection_hash, file_path, is_saved | 同步文件状态 |
