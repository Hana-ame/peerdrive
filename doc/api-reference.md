# PeerDrive 后端 API 参考

> ⚠️ **2026-08-17 起前端已全面迁移到 `/ws/peer` 的 admin 帧**（见 REFACTOR §3.10、
> NODE-API §2.4）：`front/src/api.js` 的 `request()` 全部走 WS，HTTP 路由保留但标注
> legacy（router.go LEGACY 区），仅兼容旧前端/curl/集成测试。下表仍是路由与请求/响应
> 结构的权威参考（admin 帧内部转发到同一 controller，行为一致）。
>
> 面向前端开发，按 `front/src/api.js` 整理。API Base 可配置，默认 `https://wsl-3000.moonchan.xyz`。
> Auth: `Authorization: Bearer <token>`，token 来源于 URL fragment (`#token`) 或设置页手动输入。

---

## 1. 文件管理

| 端点 | 方法 | 说明 |
|------|------|------|
| `/files?sort=time` | GET | 列出所有已注册文件 |
| `/files/register_local` | POST | 注册本地文件 `{ path, filename }` |
| `/files/register_url` | POST | 注册 URL 下载 `{ url, filename }` |
| `/files/register_folder` | POST | 注册整个文件夹 `{ folder_path }` |
| `/files/upload` | POST | 上传文件 (multipart, field: `file`) |
| `/files/browse?path=` | GET | 浏览文件系统目录 |
| `/files/verify/:hash` | GET | 验证文件是否存在 |
| `/files/:hash` | DELETE | 删除已注册文件 |
| `/sha256sum/:hash` | GET | 直接下载文件（按 hash），返回文件流 |

## 2. 匿名合集 (Anon Collections)

| 端点 | 方法 | 说明 |
|------|------|------|
| `/anon/collections` | GET | 列出所有匿名合集 |
| `/anon/collections` | POST | 创建合集 `{ entries, friendly_name, tags, visibility, access_list_hash }` |
| `/anon/collections/:hash` | GET | 获取合集详情 |
| `/anon/collections/:hash/:path` | GET | 下载合集中指定路径的文件 |
| `/anon/collections/commit` | POST | 提交合集版本 `{ source_hash, entries, commit_message }` |
| `/anon/collections/fork` | POST | Fork 合集 `{ source_hash, add_entries, remove_paths, friendly_name }` |

Entry 格式:

```json
// SHA256 provider（已注册到存储的文件）
{"path":"readme.txt","providers":[{"type":"sha256","value":"<64-hex>"}]}
// URL provider（外部链接，无需下载到本地存储）
{"path":"readme.txt","providers":[{"type":"url","value":"https://example.com/readme.txt"}]}
// 目录条目（不需要 providers）
{"path":"images/"}
```

## 3. 用户合集 (User Collections)

| 端点 | 方法 | 说明 |
|------|------|------|
| `/collections/:username` | GET | 列出用户合集 |
| `/collections` | POST | 创建合集 `{ username, collection_name, visibility, tags }` |
| `/collections/:username/:coll` | GET | 获取合集详情 |
| `/collections/:username/:coll/tags` | POST | 更新合集标签 `{ tags }` |
| `/collections/:username/:coll/entries` | POST | 添加条目 `{ path, hash }` |
| `/collections/:username/:coll/entries/:path` | DELETE | 删除条目 |
| `/collections/:username/:coll/commit` | POST | 提交版本 `{ commit_message }` |
| `/collections/:username/:coll/log` | GET | 版本历史 |
| `/collections/:username/:coll/rollback/:vid` | POST | 回滚到指定版本 |
| `/collections/:username/:coll/visibility` | POST | 设置可见性 `{ visibility }` |
| `/collections/search?q=` | GET | 搜索公开合集 |
| `/collections/public?q=` | GET | 列出公开合集 |
| `/:username/:coll/:filepath` | GET | 直接下载合集文件 |

## 4. 合集协作 (Actions)

| 端点 | 方法 | 说明 |
|------|------|------|
| `/actions/fork` | POST | Fork 用户合集 `{ username, source_username, collection_name, source_coll_name }` |
| `/actions/merge` | POST | 合并合集 `{ ..., strategy: "ours"|"theirs" }` |
| `/actions/pull` | POST | 拉取合集更新 `{ username, collection_name }` |

## 5. P2P 网络

### 状态与查询

| 端点 | 方法 | 说明 |
|------|------|------|
| `/p2p/status` | GET | P2P 总状态（含 signal_peers） |
| `/p2p/node` | GET | 本机节点信息 |
| `/p2p/peers` | GET | 已连接 peers 列表 |
| `/p2p/peers/detail` | GET | peers 详细信息 |
| `/p2p/peers/detail/:peerId` | GET | 单个 peer 详情 |
| `/p2p/discovered` | GET | 发现节点列表 |
| `/p2p/connections` | GET | 连接列表 |
| `/p2p/stats` | GET | P2P 统计 |
| `/p2p/topology` | GET | 网络拓扑数据 |
| `/p2p/quality` | GET | 连接质量信息 |
| `/p2p/ws/info` | GET | WebSocket 传输信息 |
| `/p2p/auth/status` | GET | JWT 认证状态 |

### 操作

| 端点 | 方法 | 说明 |
|------|------|------|
| `/p2p/ping/:peerId` | GET | Ping 指定 peer |
| `/p2p/connect` | POST | 连接 peer `{ peer_id, addrs }` |
| `/p2p/announce` | POST | 向网络宣告 hash `{ hash }` |
| `/p2p/fetch` | POST | 从 peer 抓取数据 `{ peer_id, hash }` |
| `/p2p/sync` | POST | 从 peer 同步合集 `{ peer_id, collection_name }` |
| `/p2p/push` | POST | 推送合集给 peer `{ peer_id, collection_name }` |
| `/p2p/request-file` | POST | 请求文件 `{ hash }` |
| `/p2p/dual/announce` | POST | 双栈宣告 `{ hash }` |
| `/p2p/dual/find` | POST | 双栈查找 `{ hash }` |

### WebSocket

| 端点 | 说明 |
|------|------|
| `/ws/transfer` | 文件传输 WebSocket，地址由 HTTP API Base 的 scheme 换为 `ws` |

## 6. BT (BitTorrent)

| 端点 | 方法 | 说明 |
|------|------|------|
| `/bt/status` | GET | BT 模块状态 |
| `/bt/stats` | GET | BT 统计信息 |
| `/bt/announce` | POST | BT DHT 宣告 `{ hash }` |
| `/bt/find` | POST | BT DHT 查找 `{ hash }` |
| `/bt/downloads` | GET | 列出所有下载任务 |
| `/bt/download/:infohash` | GET | 单个下载任务详情 |
| `/bt/magnet` | POST | 解析 Magnet URI `{ uri }` |
| `/bt/torrent` | POST | 上传 .torrent 文件 (multipart, field: `torrent`) |
| `/bt/download/:infohash` | DELETE | 删除下载任务 |
| `/bt/download/:infohash/pause` | POST | 暂停下载 |
| `/bt/download/:infohash/resume` | POST | 恢复下载 |
| `/bt/download/:infohash/seed` | POST | 开始做种 |
| `/bt/download/:infohash/unseed` | POST | 停止做种 |
| `/bt/bep51/sample` | GET | BEP 51 采样数据（天线功能用） |

## 7. IPFS

| 端点 | 方法 | 说明 |
|------|------|------|
| `/ipfs` | GET | IPFS 兼容层状态 |
| `/ipfs/toggle` | POST | 启用/禁用 IPFS 兼容模式 `{ enabled }` |
| `/ipfs/pin/:cid` | POST | 固定 CID |
| `/ipfs/pin/:cid` | DELETE | 取消固定 CID |
| `/ipfs/pins` | GET | 列出已固定 CID |
| `/ipfs/gateways` | GET | IPFS 网关健康检查 |

## 8. 本地同步 & 访问控制

| 端点 | 方法 | 说明 |
|------|------|------|
| `/local/save` | POST | 合集本地保存 |
| `/local/status/:hash` | GET | 本地保存状态查询 |
| `/access/list` | POST | 创建 ACL `{ users, groups }` |
| `/access/list/:hash` | GET | 获取 ACL 详情 |

## 9. 系统 & 任务

| 端点 | 方法 | 说明 |
|------|------|------|
| `/ping` | GET | 健康检查 |
| `/tasks` | GET | 后台任务列表 |
| `/tasks/:id` | GET | 单个任务状态/进度 |

## 10. 注册服务器 (外部服务)

以下端点调用外部注册服务器，URL 由 `peerdrive_reg_server_url` 配置。

| 端点 | 说明 |
|------|------|
| `/reg/users` | 注册用户列表 |
| `/reg/groups` | 群组列表 |
| `/reg/groups/:name/members` | 群组成员 |
| `/comments/:hash` (GET) | 获取合集评论 |
| `/comments/:hash` (POST) | 发表评论 `{ content }` |
| `/stats` | 服务器统计 |
| `/auth/whoami` | JWT 身份验证 |
| `/auth/group/:username` (GET/POST) | 用户群组管理 |

## 前端页面 → API 对照

| 页面 (路由) | 组件 | 主要调用的 API |
|-----------|------|---------------|
| `/` | Plaza | `listAnonCollections`, `searchCollections`, `listPublicCollections`, `getBEP51Sample` |
| `/files` | FileManager | `listFiles`, `registerLocalFile`, `registerFolder`, `deleteFile`, `browseDir`, `uploadFile` |
| `/create` | AnonCreator | `createAnonCollection`, `commitAnonCollection`, `listFiles`, `registerLocalFile`, `registerURL`, `browseDir`, `saveLocal` |
| `/anon/collections/:hash` | AnonExplorer | `getAnonCollection`, `forkAnonCollection` |
| `/:username/:collName` | Explorer | `getUserCollection`, `getVersionLog`, `commitCollection`, `rollbackVersion` |
| `/p2p` | P2PPanel | `getP2PStatus`, `getConnections`, `getP2PPeers`, `getWSInfo` |
| `/p2p/topology` | P2PTopology | `getP2PTopology`, `getP2PQuality` |
| `/p2p/dht` | DHTExplorer | `p2pAnnounce`, `p2pFetch` |
| `/p2p/dashboard` | P2PDashboard | `getP2PStats`, `getP2PDiscovered` |
| `/ipfs` | IPFSPanel | `getIPFSCompatStatus`, `getIPFSGatewayStatus`, `listPins`, `pinCID`, `unpinCID` |
| `/bt` | BTPanel | `getBTStatus`, `btFind`, `btAnnounce`, `getBEP51Sample` |
| `/bt/controller` | BTController | `btGetDownloads`, `btMagnetResolve`, `btTorrentUpload`, `btPauseDownload`, `btResumeDownload`, `btRemoveDownload` |
| `/settings` | Settings | `ping`, `getAuthStatus`, `getApiBase`/`setApiBase`, LLM 配置 |

## 认证配置 (localStorage keys)

| Key | 说明 |
|-----|------|
| `peerdrive_api_base` | API Base URL |
| `peerdrive_auth_token` | URL fragment 传入的 token |
| `peerdrive_auth_key` | 设置页手动输入的 token |
| `peerdrive_auth_header_enabled` | 是否启用 auth header |
| `peerdrive_reg_server_url` | 注册服务器地址 |

## LLM 配置 (localStorage keys)

| Key | 说明 |
|-----|------|
| `peerdrive_llm_endpoint` | LLM API 端点 |
| `peerdrive_llm_model` | 模型名称 |
| `peerdrive_llm_apikey` | API Key |
| `peerdrive_llm_body_template` | 请求体模板 (JSON) |
| `peerdrive_data_consent` | 数据许可 |

---

## 补充路由

### P2P 可续传 / 多源下载

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/p2p/download/resume` | 可续传下载 |
| GET | `/p2p/download/progress/:hash` | 下载进度 |
| POST | `/p2p/download/cancel/:hash` | 取消下载 |
| POST | `/p2p/download/multipeer` | 多 peer 并发下载 |
| GET | `/p2p/download/sources/:hash` | 下载源列表 |
| GET | `/p2p/download/multipeer/progress/:hash` | 多源下载进度 |

### 端口转发

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/p2p/forward/create` | 创建转发会话 |
| POST | `/p2p/forward/connect` | 连接转发会话 |
| GET | `/p2p/forward/list` | 列出活跃转发 |
| POST | `/p2p/forward/close` | 关闭转发会话 |

### BT 额外端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/bt/stats` | BT 全局统计 |
| GET | `/bt/download/:infohash/torrent` | 下载 .torrent 文件 |
| GET | `/bt/download/:infohash/magnet` | 获取 magnet URI |
| POST | `/bt/seed-collection` | 将匿名合集作为 BT 做种 |

### IPFS CID 下载

| 方法 | 路径 |
|------|------|
| GET | `/ipfs/:cid` |

### 分享

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/shares` | 创建分享 |
| GET | `/shares` | 列表 |
| GET | `/s/:token` | 访问 |

### WebDAV

| 方法 | 路径 |
|------|------|
| ANY | `/webdav/*path` |

### Relay 代理

| 方法 | 路径 |
|------|------|
| GET | `/relay/proxy` |

### Swagger UI

| 方法 | 路径 |
|------|------|
| GET | `/swagger/*any` |
