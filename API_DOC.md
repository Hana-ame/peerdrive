# Peerdrive API 行为文档

> 后端 Go + Gin，SQLite 存储。所有请求若无特殊说明，Content-Type 为 `application/json`。
> 基础地址示例：`http://wsl-3000.moonchan.xyz`

---

## GET /ping

健康检查。无参数。直接返回 HTTP 200，body 为字符串 `pong`。

---

## GET /sha256sum/:sha256

按 SHA256 哈希下载文件。

- 从路径参数得到 64 位十六进制哈希
- 验证哈希格式，不合法返回 400
- 调用 Downloader 服务从 `storage/{hash[:2]}/{hash}` 读取文件流
- 返回文件内容（Content-Disposition: attachment），文件名取自 DB 记录
- 文件不存在返回 404

---

## GET /p2p/node

返回本节点的 libp2p 信息。

- 从 P2P 服务获取 PeerID 和监听地址列表
- 返回 `{ "peer_id": "12D3KooW...", "addrs": ["/ip4/...", ...] }`

---

## GET /p2p/peers

返回当前已连接的对等节点列表。

- 从 P2P 服务获取已连接节点的 PeerID
- 返回 `{ "peers": ["12D3KooW...", ...] }`

---

## GET /p2p/ping/:peer_id

向指定 PeerID 发送 libp2p ping 并测量 RTT。

- 从路径参数拿到 peer_id
- 解码为 libp2p 的 PeerID，格式不合法返回 400
- 发送 ping 并等待回应，超时或失败返回 500
- 成功返回 `{ "peer": "12D3KooW...", "rtt": "42.5ms" }`

---

## POST /files/upload

上传文件。Content-Type: multipart/form-data。

- 从 `file` 字段读取上传的文件
- 计算文件的 SHA256 哈希
- 按 `storage/{哈希前两位}/{完整哈希}` 写入磁盘（自动建目录）
- 将 `{ hash, provider_type: "local", path, filename }` 写入 SQLite files 表
- 哈希已存在（重复上传）返回 409
- 成功返回 `{ "hash": "...", "filename": "..." }`

**磁盘存储结构：**
```
storage/
  ab/
    abcdef123456...   # 实际文件
  cd/
    cdef7890abcd...   # 实际文件
```

---

## POST /files/register_local

注册一个已在 storage 目录中的文件，不复制文件。

- 从 body 拿到 `{ "path": "relative/path.txt", "filename": "display_name.txt" }`
- 路径相对于 storage 目录，拼接后打开文件
- 计算 SHA256 哈希
- 检查哈希是否已在 DB 中，已在则直接返回已有信息（幂等）
- 未注册则插入 DB，`provider_type` 为 "local"，`path` 保持原相对路径
- 文件不存在返回 400

---

## POST /files/register_folder

注册 storage 下某个子文件夹中的所有文件（非递归，只扫一层）。

- 从 body 拿到 `{ "folder_path": "subfolder" }`
- 列出 `storage/subfolder/` 下所有条目
- 跳过子目录
- 对每个文件：计算 SHA256 → 检查是否已注册 → 未注册则插入 DB
- `path` 记录为 `{folder_path}/{filename}`
- 已存在的文件跳过，不报错
- 返回所有处理结果数组 `{ "registered": [{"filename": ..., "hash": ...}, ...] }`
- 文件夹不存在返回 400

---

## GET /files/verify/:hash

按 SHA256 查询文件元数据。

- 验证哈希格式，不合法返回 400
- 查 files 表，未找到返回 404
- 返回 `{ "hash": "...", "filename": "...", "provider": "local", "path": "..." }`

---

## DELETE /files/:hash

按 SHA256 删除文件。

- 验证哈希格式
- 查 files 表，未找到返回 404
- 如果 `provider_type == "local"`，删除磁盘文件 `storage/{path}`
- 删除 DB 记录
- 返回 `{ "message": "deleted" }`

---

## POST /files/diff

对比两个版本的条目差异（diff 两个 version_entries 快照）。

- 从 body 拿到 `{ "version_a": 1, "version_b": 2 }`
- 分别加载两个版本的 `version_entries` 快照
- 按 path 建立 map 对比
- 返回三个列表：
  - `added`：B 中有 A 中没有的条目
  - `removed`：A 中有 B 中没有的条目
  - `modified`：AB 都有但 hash 不同的条目（含 old_hash / new_hash）

---

## POST /collections

创建新集合。

- 从 body 拿到 `{ "username": "alice", "collection_name": "music" }`
- 插入 collections 表，`UNIQUE(username, collection_name)` 约束
- 重复创建返回 409
- 成功返回 `{ "id": 1, "username": "alice", "collection_name": "music" }`

---

## GET /collections/:username

列出某用户的所有集合。

- 查 collections 表 WHERE username = ?
- 按 created_at DESC 排序
- 返回 `{ "data": [ { "id", "username", "collection_name", "created_at" }, ... ] }`

---

## GET /collections/:username/:collection_name

获取集合详情，含当前所有条目。

- 查 collection + 关联的 collection_entries
- 集合不存在返回 404
- 返回 `{ "collection": {...}, "entries": [ { "id", "collection_id", "path", "file_hash" }, ... ] }`

---

## POST /collections/:username/:collection_name/entries

向集合添加一个 path → hash 映射条目。

- 从 body 拿到 `{ "path": "dir/file.txt", "hash": "sha256hex..." }`
- 集合不存在时自动创建（GetOrCreateCollection）
- 写入 collection_entries 表，`UNIQUE(collection_id, path)` 约束
- 同 path 重复写入 = 覆盖更新（upsert 语义）
- 返回 `{ "message": "entry added" }`

---

## DELETE /collections/:username/:collection_name/entries/*path

从集合中移除一个 path 映射。

- 路径参数中的 `*path` 包含前导 `/`，后端会自动去掉
- 集合不存在返回 404
- 删除 collection_entries 中对应的行
- 返回 `{ "message": "entry removed" }`

---

## POST /collections/:username/:collection_name/commit

将集合当前所有条目快照为一个新版本。

- 从 body 拿到 `{ "commit_message": "initial commit" }`
- 查当前最大版本号，新版本号 = 最大 + 1
- 最新版本的 id 作为 parent_version_id，形成版本链
- 将 collection_entries 全部复制到 version_entries（SnapshotVersionEntries）
- 返回 `{ "message": "committed", "version_number": 1 }`

---

## GET /collections/:username/:collection_name/log

获取集合的版本历史。

- 查 collection_versions 表，按 version_number DESC 排序
- 返回 `{ "data": [ { "id", "collection_id", "version_number", "commit_message", "created_at", "parent_version_id" }, ... ] }`

---

## POST /collections/:username/:collection_name/rollback/:version_id

将集合回滚到指定版本。

- 路径参数 version_id 是 collection_versions 的 id
- 先删当前 collection_entries 全部数据
- 再将指定版本的 version_entries 重新插入为 collection_entries
- 事务执行，失败自动回滚
- 返回 `{ "message": "rolled back" }`

---

## GET /:username/:collection_name/*filepath

从集合中按路径下载文件。

- 查当前集合的 collection_entries 表，找 path 匹配的条目
- 拿到对应的 file_hash
- 委托给 `/sha256sum/:hash` 的下载逻辑（DownloadBySHA256Internal）
- 集合不存在或路径不存在返回 404
- 路径参数 `*filepath` 包含前导 `/`，后端自动去掉

---

## POST /actions/fork

将源集合的所有条目复制到一个新集合（fork 语义）。

- 从 body 拿到 `{ "username", "collection_name", "source_username", "source_coll_name" }`
- 源集合不存在返回 404
- 新集合名已存在返回 409
- 查询源集合的全部 collection_entries
- 逐条插入新集合（path → file_hash 不变）
- 返回 `{ "message": "forked", "id": ..., "entries_count": N }`

---

## POST /actions/merge

将源集合合并到本地集合。三种策略：

- 从 body 拿到 `{ "username", "collection_name", "source_username", "source_coll_name", "strategy" }`
- 加载本地和源集合的全部条目，按 path 建立 map
- 检测冲突：同 path 不同 hash 即为冲突
- `strategy == "manual"`：发现冲突返回 409 + 冲突列表 `{ "conflicts": [{ "path", "local_hash", "source_hash" }] }`，前端解决后重试
- `strategy == "ours"`：冲突时保留本地 hash
- `strategy == "theirs"`：冲突时接受源 hash
- 源有但本地没有的条目（非冲突）始终添加
- 逐条 upsert 写入本地 collection_entries
- 返回 `{ "message": "merge complete", "conflicts_found": N, "total_entries": N }`

---

## POST /actions/pull

拉取上游更新（当前为占位实现，v2 才真正做）。

- 从 body 拿到 `{ "username", "collection_name" }`
- 返回提示消息
- 同时创建一个 transfer_tasks 记录，状态标为 completed（无实际行为）
- 返回 `{ "message": "pull not implemented (upstream sync coming in v2)" }`

---

## GET /tasks

列出所有异步任务（当前为占位）。

- 返回 `{ "tasks": [] }`，空数组

---

## GET /tasks/:id

查询异步任务状态。

- 从路径参数拿到任务 ID
- 查 transfer_tasks 表
- 未找到返回 404
- 返回 `{ "task": { "id", "type", "status", "params", "result", "created_at", "updated_at" } }`

---

## GET /swagger/*any

Swagger UI 文档页面。

- 由 gin-swagger 提供
- 浏览器访问 `/swagger/index.html` 即可

---

## 数据库总览

六张表：

| 表 | 用途 | 关键约束 |
|---|---|---|
| files | 文件元数据（哈希 → 存储位置） | hash UNIQUE |
| collections | 集合（用户命名空间） | UNIQUE(username, collection_name) |
| collection_entries | 集合内的 path → hash 映射 | UNIQUE(collection_id, path) |
| collection_versions | 版本快照记录 | 带 parent_version_id 链 |
| version_entries | 版本快照中的条目内容 | 关联 collection_versions |
| transfer_tasks | 异步任务跟踪 | status: pending/completed/failed |

## 存储结构

所有上传的文件存在 `storage/` 目录下，按 SHA256 前两位分目录：

```
storage/
  ab/
    abcdef0123456789...   # 文件内容
  12/
    1234567890abcdef...   # 文件内容
```

每个文件只存一份（内容寻址），files 表的 path 列记录相对于 storage 的路径，如 `"ab/abcdef..."`。
