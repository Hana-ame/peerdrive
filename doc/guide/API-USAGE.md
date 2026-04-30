# Peerdrive API 使用手册

> 按功能模块组织，每个模块说明 API 调用顺序、目的和调用条件。

## 目录

1. [认证与身份](#1-认证与身份)
2. [文件管理](#2-文件管理)
3. [合集系统](#3-合集系统)
4. [P2P 网络](#4-p2p-网络)
5. [BT DHT](#5-bt-dht)
6. [IPFS](#6-ipfs)
7. [中继 Relay](#7-中继-relay)
8. [评论系统](#8-评论系统)
9. [WebDAV](#9-webdav)
10. [LLM 助手](#10-llm-助手)

---

## 1. 认证与身份

### 1.1 注册用户

```
POST /auth/register  →  获取 JWT token
```

**调用条件**：未注册用户
**目的**：创建账号，获取身份凭证。JWT 用于后续所有需认证的请求。

```bash
curl -X POST http://localhost:4000/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"secret123"}'
# → {"token":"eyJ...", "username":"alice", "role":"user"}
```

### 1.2 登录

```
POST /auth/login  →  获取 JWT token
```

**调用条件**：已注册用户，token 过期后重新获取
**目的**：获取新的身份凭证（72小时有效）

### 1.3 验证身份

```
GET /auth/whoami
Authorization: Bearer <token>
```

**调用条件**：需要确认当前 token 是否有效
**目的**：返回用户名和角色，Peerdrive Node 用此接口验证用户 token

### 1.4 注册节点身份

```
POST /node/register  →  节点绑定到用户账号
Authorization: Bearer <token>
{"token":"<jwt>"}
```

**调用条件**：用户已有 JWT，想要将此 Peerdrive Node 绑定到自己名下
**目的**：
- 节点调用中心服务器验证 token → 获取用户名 → 向中心服务器登记 node↔user 绑定
- 其他节点可通过中心服务器查询"这个 peer 是谁运营的"

**调用顺序**：
1. 先在中心服务器注册用户（`POST /auth/register`）
2. 拿到 JWT 后，在 Peerdrive Node 上调用 `POST /node/register`
3. Node 联系硬编码的中心服务器完成注册

```bash
# 在 Peerdrive Node 上执行
curl -X POST http://<node>:<port>/node/register \
  -H 'Content-Type: application/json' \
  -d '{"token":"eyJ..."}'
# → {"status":"registered", "username":"alice", "peer_id":"12D3Koo..."}
```

### 1.5 查询节点运营者

```
GET /node/operator
```

**调用条件**：任何节点（无认证）
**目的**：查询此 Peerdrive Node 的运营者。匿名节点返回 `null`。

```bash
curl http://<node>:<port>/node/operator
# 已注册 → {"operator":"alice"}
# 匿名   → {"operator":null, "note":"anonymous node"}
```

### 1.6 查询服务策略

```
GET /auth/service-policy/:username
Authorization: Bearer <token>
```

**调用条件**：已认证用户，需要判断某用户是否有 relay/P2P 权限
**目的**：Peerdrive Node 在向对等节点提供服务前，查询对方是否被允许使用 relay/P2P

### 1.7 设置服务策略

```
POST /auth/service-policy/:username
Authorization: Bearer <token>
{"allow_relay":true, "allow_p2p":false, "notes":"reason"}
```

**调用条件**：管理员或用户本人
**目的**：控制某用户是否能使用 relay/P2P 服务

---

## 2. 文件管理

### 2.1 上传文件

```
POST /files/upload
Content-Type: multipart/form-data
```

**调用条件**：需要将新文件添加到 Peerdrive 存储
**目的**：上传文件，计算 SHA256，存入内容寻址存储。返回 hash 供后续使用。

**上传大小限制**：认证用户 100MB，匿名 10MB

### 2.2 注册本地文件

```
POST /files/register_local
{"path":"/home/user/file.txt", "filename":"file.txt"}
```

**调用条件**：文件已在服务器本地磁盘上，不想重复上传
**目的**：直接将本地文件纳入 Peerdrive 管理，计算 SHA256 并注册元数据

### 2.3 注册文件夹（递归）

```
POST /files/register_folder
{"folder_path":"/home/user/docs"}
```

**调用条件**：需要批量注册整个目录
**目的**：递归扫描目录下所有文件并注册

### 2.4 浏览文件

```
GET /files/browse?path=/home/user
```

**调用条件**：需要查看服务器文件系统
**目的**：浏览目录内容，选择要注册的文件

### 2.5 验证文件

```
GET /files/verify/:hash
```

**调用条件**：需要确认文件完整性
**目的**：检查文件是否存在且内容与 hash 一致

### 2.6 下载文件 (SHA256)

```
GET /sha256sum/:hash
```

**调用条件**：已知文件 SHA256 hash
**目的**：通过内容寻址下载文件，支持 Range 分块下载

### 2.7 删除文件

```
DELETE /files/:hash
```

**调用条件**：需要从 Peerdrive 存储中移除文件
**目的**：删除文件元数据和存储

---

## 3. 合集系统

合集是文件的逻辑分组，支持版本管理和 P2P 共享。

### 3.1 创建匿名合集

```
POST /anon/collections
{"entries":[{"path":"doc.txt","hash":"abc123..."}], "friendly_name":"我的文档"}
```

**调用条件**：有一组文件想要打包成合集
**目的**：创建不可变的匿名合集。每个条目是 `路径→SHA256` 的映射。

### 3.2 查看合集列表

```
GET /anon/collections
```

**调用条件**：浏览本节点上所有匿名合集
**目的**：获取合集列表（含 hash、名称、条目数、标签）

### 3.3 查看合集内容

```
GET /anon/collections/:hash
```

**调用条件**：已知合集 hash
**目的**：查看合集的全部条目（路径、hash、MIME、大小）

### 3.4 下载合集内的文件

```
GET /anon/collections/:hash/path/to/file
```

**调用条件**：需要获取合集内某个文件
**目的**：按路径下载合集内的文件

### 3.5 合集版本管理

```
# 提交新版本
POST /anon/collections/commit
{"source_hash":"abc...", "entries":[...], "commit_message":"更新了文档"}

# 查看版本历史
GET /collections/:username/:coll/log

# 回滚到指定版本
POST /collections/:username/:coll/rollback/:version_id
```

**调用目的**：追踪合集变更历史，支持回滚

### 3.6 Fork 合集

```
POST /anon/collections/fork
{"source_hash":"abc...", "friendly_name":"我的修改版"}
```

**调用条件**：想要基于别人的合集创建自己的版本
**目的**：复制合集并开始独立编辑

### 3.7 Merge 合并

```
POST /actions/merge
{"username":"target", "source_username":"source", "collection_name":"coll", "source_coll_name":"source_coll", "strategy":"ours"}
```

**调用目的**：将两个合集的条目合并

### 3.8 分享合集

```
POST /shares
{"collection_hash":"abc...", "expires_in_hours":24}
```

**调用条件**：想要生成分享链接给其他人
**目的**：创建带 token 的分享链接

### 3.9 完整合集工作流

```
1. POST /files/upload              ← 上传文件，拿到 hash
2. POST /files/register_local      ← 或注册本地文件
3. POST /anon/collections          ← 创建合集（hash + 路径）
4. GET  /anon/collections/:hash    ← 查看合集
5. POST /anon/collections/commit   ← 修改后提交新版本
6. POST /shares                     ← 分享合集链接
```

---

## 4. P2P 网络

### 4.1 查看节点信息

```
GET /p2p/node
GET /p2p/status
```

**调用条件**：P2P 已启用（`PEERDRIVE_P2P_ENABLE=true`）
**目的**：获取本节点 Peer ID、监听地址、连接状态

### 4.2 查看已连接对端

```
GET /p2p/peers          ← 已连接的对端
GET /p2p/discovered     ← mDNS 发现的局域网对端
GET /p2p/peers/detail   ← 对端详细信息（传输量、延迟、传输方式）
```

**调用目的**：了解当前网络状态

### 4.3 连接到对端

```
POST /p2p/connect
{"addr":"/ip4/192.168.1.100/tcp/4001/p2p/12D3Koo..."}
```

**调用条件**：已知对端的 multiaddr
**目的**：建立 P2P 直连

### 4.4 宣告哈希

```
POST /p2p/announce
{"hash":"<sha256>"}
```

**调用条件**：本节点拥有某个文件/合集
**目的**：在 DHT 上宣告"我有这个内容"，让其他节点可以找到

### 4.5 从 P2P 获取合集

```
POST /p2p/fetch
{"hash":"<collection_hash>"}
```

**调用条件**：通过 DHT 发现了需要的合集
**目的**：从 P2P 网络拉取合集数据

### 4.6 P2P 发现流程

```
1. GET  /p2p/node                    ← 获取自己的 Peer ID
2. GET  /p2p/discovered              ← 查看局域网发现的节点
3. POST /p2p/connect                 ← 手动连接到远程节点
4. POST /p2p/announce                ← 宣告自己的文件
5. POST /p2p/fetch                   ← 拉取网络上的合集
6. POST /p2p/sync                    ← 同步文件到本地
```

---

## 5. BT DHT

BT DHT 运行独立的 Mainline Kademlia 网络，与 IPFS DHT 并行工作。

### 5.1 查看 BT DHT 状态

```
GET /bt/status
```

**调用条件**：BT DHT 已启用（`PEERDRIVE_BT_DHT_ENABLE=true`）
**目的**：查看 DHT 节点数量、路由表大小

### 5.2 BT 宣告

```
POST /bt/announce
{"hash":"<infohash>"}
```

**调用条件**：想要在 BT 网络上宣布自己拥有某个 infohash
**目的**：让 BT 网络上的其他节点可以通过 DHT 找到自己

### 5.3 BT 查找

```
POST /bt/find
{"hash":"<infohash>"}
```

**调用条件**：需要找到某个 infohash 的提供者
**目的**：在 BT DHT 中查找提供者列表（ip:port）

### 5.4 BEP44 数据存取

```
# 存入数据
POST /bt/bep44/put
{"target":"<40-char-hex-key>", "value":"<base64-data>"}

# 读取数据
POST /bt/bep44/get
{"target":"<40-char-hex-key>"}
```

**调用条件**：需要在 BT DHT 上存储或读取任意 key-value 数据
**目的**：利用 BT DHT 作为分布式键值存储

### 5.5 BEP51 Infohash 采样

```
GET /bt/bep51/sample
```

**调用条件**：想要发现 BT 网络上有哪些内容
**目的**：从 DHT 路由表中采样 infohash，发现网络上流行的内容

### 5.6 BT 下载器（完整客户端）

```
# 添加种子文件
POST /bt/torrent           ← 上传 .torrent 文件

# 添加磁力链接
POST /bt/magnet            ← 解析并添加磁力链接
{"magnet":"magnet:?xt=urn:btih:..."}

# 管理下载
GET  /bt/downloads          ← 列出所有下载任务
GET  /bt/download/:infohash ← 查看单个下载进度
POST /bt/download/:infohash/pause
POST /bt/download/:infohash/resume
DELETE /bt/download/:infohash  ← 删除下载任务

# 做种
POST /bt/download/:infohash/seed
POST /bt/download/:infohash/unseed
```

**典型 BT 下载流程**：
```
1. POST /bt/magnet              ← 添加磁力链接
2. GET  /bt/download/:infohash  ← 监控下载进度
3. 下载完成后文件自动注册到 Peerdrive 存储
4. GET  /sha256sum/:hash        ← 通过 Peerdrive 下载文件
```

### 5.7 DHT Key-Value 查询

```
POST /bt/dht/get
{"target":"<40-char-hex>"}
```

**调用目的**：直接查询 BT DHT 中某个 key 对应的值（BEP44 简化接口）

---

## 6. IPFS

IPFS 兼容层使用 libp2p Kademlia DHT 和 Bitswap 协议。
通过 `provider/ipfs.go` 实现 ContentProvider 接口，
在 Manager 中注册为 `"ipfsgw"` provider，下载优先级通过 `PEERDRIVE_DOWNLOAD_ORDER` 配置（默认 `local,ipfs,ipfsgw,btdht,http`）。

### 6.1 查看 IPFS 状态

```
GET /ipfs
```

**调用条件**：IPFS compat 已启用
**目的**：查看 IPFS 兼容层状态（是否启用、对等节点数、区块数）

### 6.2 开关 IPFS 兼容层

```
POST /ipfs/toggle
{"enabled":true}
```

**调用条件**：需要按需启用 IPFS 兼容模式
**目的**：开启/关闭 Bitswap 和 IPFS DHT 互操作

### 6.3 CID 下载

```
GET /ipfs/:cid
```

**调用条件**：已知文件的 CIDv1
**目的**：通过 IPFS CID 下载文件。Peerdrive 上传文件时自动计算 CID 并关联到 SHA256。

### 6.4 CID 固定

```
POST /ipfs/pin/:cid        ← 固定 CID
DELETE /ipfs/pin/:cid      ← 取消固定
GET /ipfs/pins             ← 列出所有固定
```

**调用目的**：防止 IPFS 垃圾回收删除指定内容

### 6.5 IPFS 网关状态

```
GET /ipfs/gateways
```

**调用目的**：检查 ipfs.io、cloudflare-ipfs.com、dweb.link 的可用性

### 6.6 IPFS DHT 查询

```
POST /ipfs/dht/get
{"cid":"<cidv1>"}
```

**调用目的**：在 IPFS DHT 中查找谁拥有指定 CID

### 6.7 双栈查询

```
POST /p2p/dual/announce    ← 同时在 IPFS + BT DHT 宣告
POST /p2p/dual/find        ← 同时在两个 DHT 查找
```

**调用目的**：最大化内容可发现性

---

## 7. 中继 Relay

Relay 是网络中继节点，帮助 NAT 后的节点互联。Relay 集成在 Peerdrive Node 中。

### 7.1 Node 注册为 Relay

```
POST /p2p/relay/register
{"peer_id":"12D3Koo...","addrs":["/ip4/.../tcp/4001"],"storage_mb":1024,"version":"v3"}
```

**调用条件**：Node 配置了 `PEERDRIVE_REG_SERVER_URL`，自动执行
**目的**：向中心服务器注册为中继节点

### 7.2 Relay 心跳

```
POST /p2p/relay/heartbeat
{"peer_id":"12D3Koo...","load_pct":0.5}
```

**调用条件**：已注册的 relay，每 60 秒自动发送
**目的**：保持在活跃 relay 列表中

### 7.3 查询活跃 Relay

```
GET /p2p/relay/list
```

**调用条件**：Node 需要发现中继节点来协助 NAT 穿透
**目的**：获取最近 5 分钟内有心跳的 relay 列表

### 7.4 Relay 操作者

```
GET /p2p/relay/:peer_id/operator    ← 查询谁运营这个 relay
POST /p2p/relay/:peer_id/operator   ← 绑定 relay 到用户
```

---

## 8. 评论系统

### 8.1 读取评论

```
GET /comments/:hash
```

**调用条件**：任何用户（匿名可读）
**目的**：查看某个合集下的所有评论

### 8.2 发表评论

```
POST /comments/:hash
Authorization: Bearer <token>
{"content":"这个合集很好！"}
```

**调用条件**：已认证用户
**目的**：在合集下留言

---

## 9. WebDAV

### 9.1 挂载 Peerdrive 为网络驱动器

```
WebDAV 地址: http://<node>:<port>/webdav/
```

**调用条件**：`PEERDRIVE_WEBDAV_ENABLE=true`（默认开启）
**目的**：通过 WebDAV 协议将 Peerdrive 存储挂载为本地磁盘

**各系统挂载方式**：
```bash
# Windows
net use Z: http://<node>:3000/webdav/

# macOS
# Finder → Go → Connect to Server → http://<node>:3000/webdav/

# Linux
mount -t davfs http://<node>:3000/webdav/ /mnt/peerdrive
```

---

## 10. LLM 助手

### 10.1 配置

在 Settings 页面或 localStorage 中配置：
- `peerdrive_llm_endpoint`：LLM API 地址
- `peerdrive_llm_model`：模型名称（默认 `Qwen/Qwen3-8B`）
- `peerdrive_llm_apikey`：API Key（可选）

### 10.2 可用工具（Function Calling）

LLM 助手有 17 个工具可调用：

| 工具 | 调用的 API | 目的 |
|------|-----------|------|
| `get_node_info` | `GET /ping`, `GET /p2p/node` | 节点状态 |
| `list_collections` | `GET /anon/collections` | 查看合集 |
| `create_anon_collection` | `POST /anon/collections` | 创建合集 |
| `register_local_file` | `POST /files/register_local` | 注册文件 |
| `add_file_to_collection` | `POST /collections/.../entries` | 添加文件到合集 |
| `commit_collection` | `POST /collections/.../commit` | 提交合集版本 |
| `get_p2p_status` | `GET /p2p/status` | P2P 网络状态 |

**使用条件**：LLM endpoint 已正确配置，且可以访问 Peerdrive API

---

## 快速参考：认证 Header

所有需认证的请求都带：
```
Authorization: Bearer <jwt_token>
```

匿名请求不带此 header，以匿名身份访问公开接口。
