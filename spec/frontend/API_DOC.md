# Peerdrive API 行为文档 (v2)

> 后端 Go + Gin，SQLite 存储。所有请求若无特殊说明，Content-Type 为 `application/json`。
> 认证请求需在 Header 中携带：`Authorization: Bearer <authkey>`。
> 基础地址示例：`http://wsl-3000.moonchan.xyz`

---

## 认证接口 (Auth)

### POST /auth/register
用户注册。
- **Body**: `{ "username": "...", "password": "..." }`
- **Success**: `{ "authkey": "...", "username": "..." }`
- **Error**: 409 用户已存在。

### POST /auth/login
用户登录。
- **Body**: `{ "username": "...", "password": "..." }`
- **Success**: `{ "authkey": "...", "username": "..." }`
- **Error**: 401 凭据错误。

### POST /auth/logout
注销登录。
- **Header**: `Authorization: Bearer <authkey>`
- **Success**: `{ "message": "logged out successfully" }`

### GET /auth/me
获取当前登录用户信息。
- **Header**: `Authorization: Bearer <authkey>`
- **Success**: `{ "id": ..., "username": "...", "created_at": "...", ... }`

---

## 文件与内容寻址 (CAS)

### GET /sha256sum/:sha256
按 SHA256 哈希下载文件。
- **Logic**:
    1. 循环尝试 `file_providers` 表中标记为 `available=1` 的副本（优先本地）。
    2. 若所有副本失效，触发 **P2P Bitswap 回退**：尝试从网络对等节点拉取。
    3. 成功拉取后自动缓存至本地并更新 DB。
- **Headers**: 
    - `Content-Disposition: attachment; filename="..."`
    - 若元数据标记 `is_gzip: true` $\rightarrow$ `Content-Encoding: gzip`
- **Error**: 404 文件未找到。

### POST /files/upload (认证)
上传文件。Content-Type: `multipart/form-data`。
- **Logic**: 计算 SHA256 $\rightarrow$ 存入 `storage/{h[:2]}/{h}` $\rightarrow$ 写入 `file_meta` 和 `file_providers`。
- **Success**: `{ "hash": "...", "filename": "..." }`

### POST /files/register_local (认证)
注册本地已存在的文件。
- **Body**: `{ "path": "relative/path", "filename": "display_name" }`
- **Logic**: 计算 SHA256 $\rightarrow$ 写入 `file_meta` 和 `file_providers` (provider_type='local') $\rightarrow$ 不复制文件。

### POST /files/register_folder (认证)
批量注册文件夹。
- **Body**: `{ "folder_path": "subdir" }`
- **Success**: `{ "registered": [{ "filename": ..., "hash": ... }, ...] }`

### GET /files/verify/:hash (认证)
查询文件元数据。
- **Success**: 返回文件完整元数据及副本信息。

### DELETE /files/:hash (认证)
删除文件记录及其本地物理文件。

---

## 合集管理 (Collections)

### GET /collections/search (公开)
搜索合集。
- **Query**: `?q=关键词` (匹配用户名或合集名)
- **Success**: `{ "data": [ { "id", "username", "collection_name", "current_hash", ... }, ... ] }`

### POST /collections (认证)
创建新合集。
- **Body**: `{ "username": "...", "collection_name": "..." }`
- **Success**: `{ "id": ..., "username": "...", "collection_name": "..." }`

### GET /collections/:username (认证)
列出该用户的所有合集。

### GET /collections/:username/:collection_name (认证)
获取合集详情及当前条目。
- **Logic**: 若 `current_hash` 存在，优先下载并解析对应的**匿名合集 JSON**；否则 fallback 到 `collection_entries` 表。

### POST /collections/:username/:collection_name/entries (认证)
添加/更新条目 (upsert)。
- **Body**: `{ "path": "...", "hash": "..." }`

### DELETE /collections/:username/:collection_name/entries/*path (认证)
移除条目。

### POST /collections/:username/:collection_name/commit (认证)
提交版本。
- **Logic**: 
    1. 将当前条目序列化为 **匿名合集 JSON** $\rightarrow$ 存储并获取 `hash`。
    2. 更新 `collections.current_hash` 为该 `hash`。
    3. 在 `collection_versions` 中记录版本快照。
- **Success**: `{ "message": "committed", "version_number": ..., "snapshot_hash": "..." }`

### GET /collections/:username/:collection_name/log (认证)
获取版本历史。

### POST /collections/:username/:collection_name/rollback/:version_id (认证)
回滚到指定版本。
- **Logic**: 恢复 `collection_entries` $\rightarrow$ 重新生成匿名快照 $\rightarrow$ 更新 `current_hash`。

---

## 协作与 P2P

### POST /actions/fork (认证)
复制远程合集到本地。

### POST /actions/merge (认证)
合并远程合集到本地。支持 `ours`, `theirs`, `manual` 策略。

### GET /p2p/node (公开)
返回节点 PeerID 和监听地址。

### GET /p2p/peers (公开)
返回当前连接的对等节点列表。

---

## 数据库架构 (v2)

- **`users`**: 用户账号与 `authkey`。
- **`file_meta`**: 内容唯一元数据 (hash PK)。
- **`file_providers`**: 一个 hash 对应的多个物理位置 (hash FK)。
- **`collections`**: 用户合集指针 (current_hash $\rightarrow$ file_meta)。
- **`collection_entries`**: 工作区条目。
- **`collection_versions` / `version_entries`**: 历史版本快照。
- **`transfer_tasks`**: 异步任务跟踪。
