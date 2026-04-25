# Peerdrive 数据库设计

## 概述

Peerdrive 使用 SQLite3 存储元数据，将内容标识符（SHA256）与实际物理存储位置解耦。同一内容可对应多份副本（多行记录），每份副本独立标记可用状态。

## 技术栈

- **数据库**: SQLite3
- **驱动**: `github.com/mattn/go-sqlite3`
- **原因**: 零配置、文件级、轻量，适合元数据存储

## 表结构

### `files` — 文件元数据

| 列名 | 类型 | 约束 | 说明 |
|------|------|------|------|
| `hash` | `TEXT` | `PRIMARY KEY` | 文件内容的 SHA256 哈希（唯一） |
| `size` | `INTEGER` | `DEFAULT 0` | 文件大小（字节） |
| `created_at` | `DATETIME` | `DEFAULT CURRENT_TIMESTAMP` | 首次注册时间 |
| `mime_type` | `TEXT` | `DEFAULT ''` | MIME 类型（如 `image/png`） |
| `gziped` | `INTEGER` | `DEFAULT 0` | 文件是否为 gzip 压缩 |
| `filename` | `TEXT` | — | 原始文件名，用于下载时的 `Content-Disposition` |
| `type` | `TEXT` | `DEFAULT 'blob'` | 文件类型：`blob`（普通文件）、`anon_collection`（匿名合集） |

**DDL**：
```sql
CREATE TABLE IF NOT EXISTS file_meta (
    hash TEXT PRIMARY KEY,
    size INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    mime_type TEXT DEFAULT '',
    gziped INTEGER DEFAULT 0,
    filename TEXT,
    type TEXT DEFAULT 'blob'
);
```

### `file_providers` — 文件存储位置（多副本）

| 列名 | 类型 | 约束 | 说明 |
|------|------|------|------|
| `id` | `INTEGER` | `PRIMARY KEY AUTOINCREMENT` | 自增主键 |
| `hash` | `TEXT` | `NOT NULL REFERENCES file_meta(hash)` | 文件 SHA256（外键） |
| `provider_type` | `TEXT` | `NOT NULL` | 提供者类型：`local`（本地）、`http`（远程） |
| `path` | `TEXT` | `NOT NULL` | 上传文件：`{h[:2]}/{h}`；注册文件：用户指定路径 |
| `available` | `INTEGER` | `DEFAULT 1` | 可用标记：1=可用，0=不可用 |

**DDL**：
```sql
CREATE TABLE IF NOT EXISTS file_providers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash TEXT NOT NULL REFERENCES file_meta(hash),
    provider_type TEXT NOT NULL,
    path TEXT NOT NULL,
    available INTEGER DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_provider_hash ON file_providers(hash);
```

### `collections` — 注册用户合集

| 列名 | 类型 | 约束 | 说明 |
|------|------|------|------|
| `id` | `INTEGER` | `PRIMARY KEY AUTOINCREMENT` | 自增主键 |
| `username` | `TEXT` | `NOT NULL` | 所属用户（命名空间） |
| `collection_name` | `TEXT` | `NOT NULL` | 合集名称 |
| `current_hash` | `TEXT` | `DEFAULT NULL` | 当前最新快照的 SHA256（指向 files 表的一条 type='anon_collection' 记录） |
| `created_at` | `DATETIME` | `DEFAULT CURRENT_TIMESTAMP` | 创建时间 |

**约束**：`UNIQUE(username, collection_name)`

### `collection_entries` — 合集条目（工作区）

| 列名 | 类型 | 约束 | 说明 |
|------|------|------|------|
| `id` | `INTEGER` | `PRIMARY KEY AUTOINCREMENT` | 自增主键 |
| `collection_id` | `INTEGER` | `NOT NULL` | 外键 → collections(id) |
| `path` | `TEXT` | `NOT NULL` | 文件相对路径 |
| `file_hash` | `TEXT` | `NOT NULL` | 文件 SHA256 hash |

**约束**：`UNIQUE(collection_id, path)`，`FOREIGN KEY(collection_id) REFERENCES collections(id) ON DELETE CASCADE`

### `collection_versions` — 版本快照记录

| 列名 | 类型 | 约束 | 说明 |
|------|------|------|------|
| `id` | `INTEGER` | `PRIMARY KEY AUTOINCREMENT` | 自增主键 |
| `collection_id` | `INTEGER` | `NOT NULL` | 外键 → collections(id) |
| `version_number` | `INTEGER` | `NOT NULL` | 版本号（从 1 开始递增） |
| `commit_message` | `TEXT` | — | 提交信息 |
| `created_at` | `DATETIME` | `DEFAULT CURRENT_TIMESTAMP` | 提交时间 |
| `parent_version_id` | `INTEGER` | — | 父版本 ID（形成版本链） |

**外键**：`FOREIGN KEY(collection_id) REFERENCES collections(id) ON DELETE CASCADE`

### `version_entries` — 版本快照内容

| 列名 | 类型 | 约束 | 说明 |
|------|------|------|------|
| `id` | `INTEGER` | `PRIMARY KEY AUTOINCREMENT` | 自增主键 |
| `version_id` | `INTEGER` | `NOT NULL` | 外键 → collection_versions(id) |
| `path` | `TEXT` | `NOT NULL` | 文件相对路径 |
| `file_hash` | `TEXT` | `NOT NULL` | 文件 SHA256 hash |

### `transfer_tasks` — 异步任务跟踪

| 列名 | 类型 | 约束 | 说明 |
|------|------|------|------|
| `id` | `INTEGER` | `PRIMARY KEY AUTOINCREMENT` | 自增主键 |
| `type` | `TEXT` | `NOT NULL` | 任务类型 |
| `status` | `TEXT` | `DEFAULT 'pending'` | 状态：pending/running/completed/failed |
| `params` | `TEXT` | `DEFAULT ''` | 任务参数（JSON） |
| `result` | `TEXT` | `DEFAULT ''` | 任务结果（JSON） |
| `created_at` | `DATETIME` | `DEFAULT CURRENT_TIMESTAMP` | 创建时间 |
| `updated_at` | `DATETIME` | `DEFAULT CURRENT_TIMESTAMP` | 更新时间 |

## 设计要点

### 1. hash = PK — 元数据唯一
`file_meta` 以 hash 为主键，每份内容唯一一行。`size`、`mime_type`、`gziped` 等属性记录在专用列中。

### 2. 多副本存储
`file_providers` 表支持同一文件多份副本。下载时循环尝试，失败后 `available=0` 并试下一副本。

### 3. type 列区分文件角色
- `blob` — 普通上传/注册的文件
- `anon_collection` — 匿名合集元数据 JSON

### 4. 注册用户合集与匿名合集的关系
- 匿名合集 = 不可变 JSON，由 hash 寻址（存入 `file_meta` + `file_providers`）
- 注册用户合集 = 可变指针（`collections` 表），通过 `current_hash` 指向最新匿名快照
- Commit = 生成匿名快照 JSON + 更新 `current_hash`
- GetCollection 优先返回 `current_hash` 对应的快照内容

### 5. 迁移兼容
旧 `files` 表需手动迁移到 `file_meta` + `file_providers`。新系统建表时自动创建新表，旧表数据暂不迁移。
