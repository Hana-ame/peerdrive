# Peerdrive Database Design

## Overview

Peerdrive uses SQLite3 to store metadata, decoupling content identifiers (SHA256) from actual physical storage locations. The same content can correspond to multiple replicas (multiple rows), each independently marked for availability status.

## Tech Stack

- **Database**: SQLite3
- **Driver**: `github.com/mattn/go-sqlite3`
- **Reason**: Zero configuration, file-level, lightweight — suitable for metadata storage

## Table Structure

### `users` — User accounts

| Column | Type | Constraint | Description |
|--------|------|------------|-------------|
| `id` | `INTEGER` | `PRIMARY KEY AUTOINCREMENT` | Auto-incrementing primary key |
| `username` | `TEXT` | `NOT NULL UNIQUE` | Username |
| `password_hash` | `TEXT` | `NOT NULL` | Password hash |
| `authkey` | `TEXT` | `UNIQUE` | Current valid session key |
| `created_at` | `DATETIME` | `DEFAULT CURRENT_TIMESTAMP` | Creation time |
| `updated_at` | `DATETIME` | `DEFAULT CURRENT_TIMESTAMP` | Update time |

**DDL**:
```sql
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    authkey TEXT UNIQUE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```


### `files` — File metadata

| Column | Type | Constraint | Description |
|--------|------|------------|-------------|
| `hash` | `TEXT` | `PRIMARY KEY` | SHA256 hash of file content (unique) |
| `size` | `INTEGER` | `DEFAULT 0` | File size (bytes) |
| `created_at` | `DATETIME` | `DEFAULT CURRENT_TIMESTAMP` | First registration time |
| `mime_type` | `TEXT` | `DEFAULT ''` | MIME type (e.g. `image/png`) |
| `gziped` | `INTEGER` | `DEFAULT 0` | Whether the file is gzip compressed |
| `filename` | `TEXT` | — | Original filename, used for `Content-Disposition` during download |
| `type` | `TEXT` | `DEFAULT 'blob'` | File type: `blob` (regular file), `anon_collection` (anonymous collection) |

**DDL**:
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

### `file_providers` — File storage locations (multi-replica)

| Column | Type | Constraint | Description |
|--------|------|------------|-------------|
| `id` | `INTEGER` | `PRIMARY KEY AUTOINCREMENT` | Auto-incrementing primary key |
| `hash` | `TEXT` | `NOT NULL REFERENCES file_meta(hash)` | File SHA256 (foreign key) |
| `provider_type` | `TEXT` | `NOT NULL` | Provider type: `local` (local), `http` (remote) |
| `path` | `TEXT` | `NOT NULL` | Uploaded files: `{h[:2]}/{h}`; registered files: user-specified path |
| `available` | `INTEGER` | `DEFAULT 1` | Availability flag: 1=available, 0=unavailable |

**DDL**:
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

### `collections` — Registered user collections

| Column | Type | Constraint | Description |
|--------|------|------------|-------------|
| `id` | `INTEGER` | `PRIMARY KEY AUTOINCREMENT` | Auto-incrementing primary key |
| `username` | `TEXT` | `NOT NULL` | Owning user (namespace) |
| `collection_name` | `TEXT` | `NOT NULL` | Collection name |
| `current_hash` | `TEXT` | `DEFAULT NULL` | SHA256 of the current latest snapshot (points to a type='anon_collection' record in the files table) |
| `created_at` | `DATETIME` | `DEFAULT CURRENT_TIMESTAMP` | Creation time |

**Constraint**: `UNIQUE(username, collection_name)`

### `collection_entries` — Collection entries (workspace)

| Column | Type | Constraint | Description |
|--------|------|------------|-------------|
| `id` | `INTEGER` | `PRIMARY KEY AUTOINCREMENT` | Auto-incrementing primary key |
| `collection_id` | `INTEGER` | `NOT NULL` | Foreign key → collections(id) |
| `path` | `TEXT` | `NOT NULL` | File relative path |
| `file_hash` | `TEXT` | `NOT NULL` | File SHA256 hash |

**Constraint**: `UNIQUE(collection_id, path)`, `FOREIGN KEY(collection_id) REFERENCES collections(id) ON DELETE CASCADE`

### `collection_versions` — Version snapshot records

| Column | Type | Constraint | Description |
|--------|------|------------|-------------|
| `id` | `INTEGER` | `PRIMARY KEY AUTOINCREMENT` | Auto-incrementing primary key |
| `collection_id` | `INTEGER` | `NOT NULL` | Foreign key → collections(id) |
| `version_number` | `INTEGER` | `NOT NULL` | Version number (incrementing from 1) |
| `commit_message` | `TEXT` | — | Commit message |
| `created_at` | `DATETIME` | `DEFAULT CURRENT_TIMESTAMP` | Commit time |
| `parent_version_id` | `INTEGER` | — | Parent version ID (forms a version chain) |

**Foreign key**: `FOREIGN KEY(collection_id) REFERENCES collections(id) ON DELETE CASCADE`

### `version_entries` — Version snapshot content

| Column | Type | Constraint | Description |
|--------|------|------------|-------------|
| `id` | `INTEGER` | `PRIMARY KEY AUTOINCREMENT` | Auto-incrementing primary key |
| `version_id` | `INTEGER` | `NOT NULL` | Foreign key → collection_versions(id) |
| `path` | `TEXT` | `NOT NULL` | File relative path |
| `file_hash` | `TEXT` | `NOT NULL` | File SHA256 hash |

### `transfer_tasks` — Asynchronous task tracking

| Column | Type | Constraint | Description |
|--------|------|------------|-------------|
| `id` | `INTEGER` | `PRIMARY KEY AUTOINCREMENT` | Auto-incrementing primary key |
| `type` | `TEXT` | `NOT NULL` | Task type |
| `status` | `TEXT` | `DEFAULT 'pending'` | Status: pending/running/completed/failed |
| `params` | `TEXT` | `DEFAULT ''` | Task parameters (JSON) |
| `result` | `TEXT` | `DEFAULT ''` | Task result (JSON) |
| `created_at` | `DATETIME` | `DEFAULT CURRENT_TIMESTAMP` | Creation time |
| `updated_at` | `DATETIME` | `DEFAULT CURRENT_TIMESTAMP` | Update time |

## Design Notes

### 1. hash = PK — Unique metadata
`file_meta` uses hash as its primary key, with one row per unique content. Properties such as `size`, `mime_type`, `gziped` are recorded in dedicated columns.

### 2. Multi-replica storage
The `file_providers` table supports multiple replicas for the same file. During download, the system cycles through replicas; upon failure it sets `available=0` and tries the next replica.

### 3. type column distinguishes file roles
- `blob` — Regular uploaded/registered files
- `anon_collection` — Anonymous collection metadata JSON

### 4. Relationship between registered user collections and anonymous collections
- Anonymous collection = immutable JSON, addressed by hash (stored in `file_meta` + `file_providers`)
- Registered user collection = mutable pointer (`collections` table), pointing to the latest anonymous snapshot via `current_hash`
- Commit = generate anonymous snapshot JSON + update `current_hash`
- GetCollection preferentially returns the snapshot content corresponding to `current_hash`

### 5. Migration compatibility
The old `files` table must be manually migrated to `file_meta` + `file_providers`. The new system automatically creates new tables during setup; old table data is not migrated for now.
