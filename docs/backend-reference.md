# Peerdrive 后端设计参考："节点用户" 方案

## 一句话总结

后端中的 **用户名只是命名空间维度（namespace），不是身份（identity）**。
不存在登录、注册、鉴权、用户表。任何人可以对任意用户名做任何操作。

---

## 用户模型剖析

### 没有用户表

数据库中有 6 张表，没有 `users` 表：

```
files               → 文件元数据（hash, provider_type, path, filename）
collections         → 集合（id, username, collection_name, created_at） ← username 在此
collection_entries  → 集合条目（collection_id, path, file_hash）
collection_versions → 版本快照
version_entries     → 版本条目
transfer_tasks      → 异步任务
```

来源：`internal/repository/db.go:27-50`

### username 是集合表的一个列

```sql
CREATE TABLE collections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL,
    collection_name TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(username, collection_name)   -- ← 唯一约束在此
);
```

- `username` + `collection_name` 形成复合唯一键
- 不指向任何其他表，没有外键约束

### 所有接口将 username 作为 URL 参数或 JSON 字段

两种获取方式：

**方式 A：URL 路径参数**
```
GET  /collections/:username              → listCollections
GET  /collections/:username/:coll        → getCollection
POST /collections/:username/:coll/entries → addEntry
POST /collections/:username/:coll/commit → commitVersion
DELETE /collections/:username/:coll/entries/:path → removeEntry
GET  /:username/:coll_name/*filepath     → downloadCollectionFile
```

`c.Param("username")` 直接提取，不做任何校验。

**方式 B：JSON 请求体**
```
POST /collections           body: {"username":"alice", "collection_name":"music"}
POST /actions/fork           body: {"username":"bob", "source_username":"alice", ...}
POST /actions/merge          body: {"username":"alice", "source_username":"bob", ...}
POST /actions/pull           body: {"username":"alice", "source_username":"bob", ...}
```

同样不做任何校验。任何人都可以创建指定任意用户名的合集，或 fork/merge 任意用户的数据。

来源：`internal/controller/collection.go`、`internal/controller/fork.go`、`internal/controller/merge.go`

---

## P2P 节点身份 vs 用户名

两个概念**完全独立**，没有任何关联。

| 维度 | P2P 节点身份 (peer.ID) | 用户名 (username) |
|------|------------------------|-------------------|
| 来源 | libp2p 加密密钥对 | HTTP 请求中的任意字符串 |
| 格式 | `12D3KooW...`（Base58 编码的 multihash） | 自由字符串（如 `alice`、`bob`） |
| 全局性 | 加密唯一 | 无全局唯一性，仅靠 `UNIQUE(username, coll_name)` 保证每个用户内合集名不重复 |
| 使用端点 | `/p2p/node`、`/p2p/peers`、`/p2p/ping/:id` | 所有 `/collections/*`、`/actions/*`、`/:username/:coll/*` |
| 验证方式 | 密码学验证（peer.ID 衍生自公钥） | 零验证 |
| 生命周期 | 服务启动时生成 / 持久化到密钥文件 | 请求级别的字符串，用完即弃 |

P2P 控制器源码：`internal/controller/p2p.go:1-8` 明确说明只涉及 `peer.ID` 和 `multiaddr`，没有用户名。

---

## 请求生命周期中的 username

以上传文件到合集并提交版本为例：

```
浏览器输入 alice/my_project
        │
        ▼
GET /collections/alice/my_project
        │  c.Param("username") = "alice"
        │  c.Param("collection_name") = "my_project"
        ▼
repository.GetCollection("alice", "my_project")
        │  SELECT * FROM collections WHERE username=? AND collection_name=?
        ▼
返回 { entries: [...] }
        │
        ▼
POST /collections/alice/my_project/entries
  body: {"path":"main.go", "hash":"abc123..."}
        │  c.Param("username") = "alice"
        ▼
repository.AddCollectionEntry(collectionID, "main.go", "abc123...")
        │  INSERT INTO collection_entries ...
        ▼
POST /collections/alice/my_project/commit
  body: {"commit_message":"init"}
        │  c.Param("username") = "alice"
        ▼
repository.CreateVersion(collectionID, message)
repository.SnapshotVersionEntries(versionID, collectionID)
        │  INSERT INTO collection_versions + INSERT INTO version_entries ...
        ▼
返回 { version_id, version_number }
```

任何步骤都没有验证**请求方**是否是 `alice`。如果 Bob 知道 Alice 的合集名，他完全可以：
- `POST /collections` → 创建 `bob/alice_stuff`
- `POST /actions/fork` → Fork `alice` → `bob/` 下
- `POST /collections/bob/alice_stuff/entries` → 自由编辑

---

## "节点用户"结论

**后端不存在节点用户的概念。** 所有用户数据存储在同一个 SQLite 数据库中，用 `username` 列做逻辑分区。任何 HTTP 客户端可以自由指定任意用户名进行读写。安全性依赖：
1. 网络隔离（仅监听到本机或内网）
2. P2P 网络中的节点间访问控制（未实现，v2 计划）

---

## 关键文件清单

| 文件路径 | 包注释摘要 | 与用户/认证的关系 |
|----------|-----------|-------------------|
| `cmd/server/main.go` | 启动入口：DB → provider → downloader → P2P → router | 无用户初始化 |
| `internal/router/router.go` | 路由注册：/ping, /p2p/, /files/, /collections/, /actions/, /:user/:coll/ | username 作为 url 参数传递，无校验 |
| `internal/controller/collection.go` | 集合 CRUD + 条目 + 版本控制 | 所有函数通过 `c.Param("username")` 获取用户名，直接传给 repo |
| `internal/controller/fork.go` | fork/pull 操作 | 从 JSON body 读取 username（目标）和 source_username（源），无权限检查 |
| `internal/controller/merge.go` | merge 操作（三种策略） | 同样从 JSON body 读取，不校验 |
| `internal/controller/p2p.go` | libp2p 节点信息、peers、ping | 完全不涉及 username，仅操作 peer.ID |
| `internal/service/p2p.go` | libp2p 节点生命周期（NewP2PService, GetNodeInfo, PingPeer） | 无 username 概念 |
| `internal/service/downloader.go` | 内容寻址下载流 | 无 username 概念 |
| `internal/repository/db.go` | 6 张表建表 SQL | `collections` 表含 `username TEXT NOT NULL` + `UNIQUE(username, collection_name)` |
| `internal/repository/collection_repo.go` | 集合四表 CRUD | 所有查询用 `WHERE username=?` 过滤，无认证 |
| `internal/model/collection.go` | Collection / CollectionEntry / CollectionVersion / VersionEntry 结构体 | Collection 结构体含 `Username string` 字段 |
| `go/LOG.md` | 开发日志 | 提及 `repo_files` 表设计但未实现 |
| `go/DB.md` | 数据库 schema 文档 | 描述集合表结构 |
