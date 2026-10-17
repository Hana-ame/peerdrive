# Peerdrive API 路由参考

> 2026-05-03T23:50:00 · 整理自 `back/internal/router/router.go`

---

## 1. 统一 Collection 路由（新，推荐使用）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/collections` | 列出所有匿名合集 |
| POST | `/collections` | 创建匿名合集 |
| GET | `/collections/:hash` | 获取合集详情 |
| GET | `/collections/:hash/*filepath` | 下载合集内文件 |
| POST | `/collections/fork` | Fork 合集 |
| POST | `/collections/merge` | 合并合集 |
| POST | `/collections/pull` | 拉取合集更新 |
| POST | `/collections/upload` | 上传文件 |
| POST | `/collections/register-local` | 注册本地文件 |
| POST | `/collections/register-url` | 注册 URL |
| POST | `/collections/register-folder` | 注册文件夹 |

## 2. 继承 Collection 路由（遗留）

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/collections` | 创建命名合集（user/coll） |
| GET | `/collections/public` | 公开合集列表 |
| GET | `/collections/search` | 搜索公开合集 |
| GET | `/collections/:username` | 用户合集列表 |
| GET | `/collections/:username/:coll` | 获取命名合集 |
| POST | `/collections/:username/:coll/entries` | 添加条目 |
| DELETE | `/collections/:username/:coll/entries/*path` | 移除条目 |
| POST | `/collections/:username/:coll/commit` | 提交版本 |
| GET | `/collections/:username/:coll/log` | 版本历史 |
| POST | `/collections/:username/:coll/rollback/:vid` | 回滚 |

⚠️ **注意**：`/collections` 有两个路由组（统一 + 命名合集）。`POST /collections` 会匹配到创建匿名合集，`POST /collections/:username` 会匹配到用户合集列表。Gin 按注册顺序匹配，统一路由优先。

## 3. 下载

| 方法 | 路径 | 行为 |
|------|------|------|
| GET | `/sha256sum/:sha256` | **仅本地存储**，无 P2P/BT/IPFS 回退 |
| GET | `/sha256sum/:sha256/:filename` | 同上，带文件名 |
| GET | `/download/:hash` | **多协议回退**（P2P → BT → IPFS → 本地） |
| GET | `/download/:hash/sources` | 检查各协议可用性 |
| POST | `/download/:hash/refresh` | 强制刷新下载源 |
| GET | `/ipfs/:cid` | 通过 CID 从 IPFS 下载 |

## 4. P2P

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/p2p/status` | P2P 状态 |
| GET | `/p2p/node` | 节点信息 |
| GET | `/p2p/peers` | 对等列表 |
| GET | `/p2p/peers/detail` | peers 详情 |
| GET | `/p2p/peers/detail/:peer_id` | 单 peer 详情 |
| GET | `/p2p/discovered` | 发现节点 |
| GET | `/p2p/connections` | 连接列表 |
| GET | `/p2p/stats` | 统计 |
| GET | `/p2p/topology` | 拓扑 |
| GET | `/p2p/quality` | 连接质量 |
| GET | `/p2p/ping/:peer_id` | Ping peer |
| POST | `/p2p/connect` | 连接 peer |
| POST | `/p2p/announce` | IPFS DHT 宣告 |
| POST | `/p2p/fetch` | P2P 拉取合集 |
| POST | `/p2p/sync` | 从 peer 同步 |
| POST | `/p2p/push` | 推送合集 |
| POST | `/p2p/request-file` | 请求文件传输 |
| GET | `/p2p/ws/info` | WebSocket 信息 |
| GET | `/p2p/webrtc/info` | WebRTC 配置 |
| GET | `/p2p/auth/status` | 认证状态 |

### 双栈 (IPFS + BT DHT)

| 方法 | 路径 |
|------|------|
| POST | `/p2p/dual/announce` |
| POST | `/p2p/dual/find` |

### 端口转发

| 方法 | 路径 |
|------|------|
| POST | `/p2p/forward/create\|connect\|close` |
| GET | `/p2p/forward/list` |

### P2P 下载（可续传/多源）

| 方法 | 路径 |
|------|------|
| POST | `/p2p/download/resume\|multipeer` |
| GET | `/p2p/download/progress/:hash\|sources/:hash\|multipeer/progress/:hash` |
| POST | `/p2p/download/cancel/:hash` |

## 5. BitTorrent

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/bt/status\|stats` | DHT 状态/统计 |
| POST | `/bt/announce\|find` | BT DHT 宣告/查找 |
| POST | `/bt/bep44/put\|get` | BEP 44 存储 |
| GET | `/bt/bep51/sample` | BEP 51 采样 |
| POST | `/bt/torrent\|magnet` | 种子/磁力链解析 |
| GET/POST/DELETE | `/bt/download/:infohash*` | BT 下载管理 CRUD |
| GET | `/bt/downloads` | 下载列表 |
| POST | `/bt/seed-collection` | 合集做种 |

## 6. IPFS

| 方法 | 路径 |
|------|------|
| GET | `/ipfs` |
| POST | `/ipfs/toggle` |
| POST/DELETE | `/ipfs/pin/:cid` |
| GET | `/ipfs/pins\|gateways` |

## 7. 文件管理（遗留）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/files` | 列出已注册文件 |
| POST | `/files/upload` | 上传文件 |
| POST | `/files/register_local\|_url\|_folder` | 注册文件/URL/文件夹 |
| GET | `/files/verify/:hash` | 验证 hash |
| GET | `/files/browse` | 浏览本地目录 |
| DELETE | `/files/:hash` | 删除文件 |
| POST | `/files/copy\|diff` | 复制/差异 |

## 8. 系统

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/ping` | 健康检查 |
| GET | `/tasks` | 任务列表 |
| GET | `/tasks/:id` | 任务状态 |
| GET | `/swagger/*any` | API 文档 |

## 9. 协作

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/actions/merge\|fork\|pull` | 遗留，301 → `/collections/` |

## 10. 其他

| 方法 | 路径 | 说明 |
|------|------|------|
| ALL | `/webdav/*path` | WebDAV 网络驱动器 |
| GET | `/ws/transfer\|signal` | WebSocket 传输/信令 |
| GET | `/relay/proxy` | P2P Relay 代理 |
| POST/GET | `/shares` | 分享链接 |
| GET | `/s/:token` | 访问分享 |
| POST/GET | `/local/save\|:hash` | 本地同步 |
| GET | `/:username/:coll/*filepath` | 命名合集文件下载 |
