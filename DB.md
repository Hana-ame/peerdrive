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
| `id` | `INTEGER` | `PRIMARY KEY AUTOINCREMENT` | 自增主键 |
| `hash` | `TEXT` | `NOT NULL` | 文件内容的 SHA256 哈希（同一 hash 可多行 = 多副本） |
| `provider_type` | `TEXT` | `NOT NULL` | 提供者类型：`local`（本地文件）、`http`（远程） |
| `path` | `TEXT` | `NOT NULL` | 存储路径：`{h[:2]}/{h}`（local）/ 远程 URL（http） |
| `filename` | `TEXT` | — | 原始文件名，用于下载时的 `Content-Disposition` |
| `metadata` | `TEXT` | `DEFAULT '{}'` | JSON 格式扩展属性：`{"is_gzip": true}` |
| `type` | `TEXT` | `DEFAULT 'blob'` | 文件类型：`blob`（普通文件）、`anon_collection`（匿名合集） |
| `available` | `INTEGER` | `DEFAULT 1` | 可用标记：1=可用，0=不可用（文件被删或 provider 不可达） |

**索引**：`idx_hash ON files(hash)`（非唯一，允许多副本）

**DDL**：
```sql
CREATE TABLE IF NOT EXISTS files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash TEXT NOT NULL,
    provider_type TEXT NOT NULL,
    path TEXT NOT NULL,
    filename TEXT,
    metadata TEXT DEFAULT '{}',
    type TEXT DEFAULT 'blob',
    available INTEGER DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_hash ON files(hash);
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

### 1. hash 不唯一 — 多副本存储
同一文件可存在多个存储位置，`available` 标记每份独立状态。下载时优先尝试 local provider，失败后标记不可用并自动切换到下一副本。

### 2. metadata JSON 列
所有文件级扩展属性（gzip、mime_type、size 等）存入 `metadata` 列，不新增专用列。扩展新属性只需改 JSON 内容，无需 ALTER TABLE。

### 3. type 列区分文件角色
- `blob` — 普通上传/注册的文件
- `anon_collection` — 匿名合集元数据 JSON（与普通 blob 同路径存储）

### 4. 注册用户合集与匿名合集的关系
- 匿名合集 = 不可变 JSON，由 hash 寻址（存入 files 表，type='anon_collection'）
- 注册用户合集 = 可变指针（collections 表），通过 `current_hash` 指向最新匿名快照
- Commit = 生成匿名快照 JSON + 更新 `current_hash`
- GetCollection 优先返回 `current_hash` 对应的快照内容

### 5. 迁移兼容
旧数据库通过 `ALTER TABLE ADD COLUMN` 逐步迁移，新增列已有则忽略：
```sql
ALTER TABLE files ADD COLUMN metadata TEXT DEFAULT '{}';
ALTER TABLE files ADD COLUMN type TEXT DEFAULT 'blob';
ALTER TABLE files ADD COLUMN available INTEGER DEFAULT 1;
ALTER TABLE collections ADD COLUMN current_hash TEXT DEFAULT NULL;
```
