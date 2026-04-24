# Database Design - Peerdrive

This document outlines the database schema and strategy used for metadata management in Peerdrive.

## Overview
Peerdrive uses a metadata layer to decouple the content identifier (SHA256 hash) from the actual physical storage location. This allows the system to support multiple storage backends (local, remote, etc.) transparently.

## Technology Stack
- **Database**: SQLite3
- **Driver**: `github.com/mattn/go-sqlite3`
- **Rationale**: SQLite provides a lightweight, zero-configuration, file-based database that is sufficient for storing metadata while remaining portable and easy to seed for testing.

## Schema Definition

### Table: `files`
The `files` table maps a unique content hash to its resource details.

| Column | Type | Constraints | Description |
| :--- | :--- | :--- | :--- |
| `hash` | `TEXT` | `PRIMARY KEY` | The SHA256 hash of the file content. Serves as the unique identifier. |
| `path` | `TEXT` | `NOT NULL` | The resource location. Can be a local relative path or a full remote URL. |
| `provider_type` | `TEXT` | `NOT NULL` | The identifier of the provider required to fetch the content (e.g., `local`, `http`). |
| `filename` | `TEXT` | - | The original filename to be used in the `Content-Disposition` header during download. |

### SQL DDL
```sql
CREATE TABLE IF NOT EXISTS files (
    hash TEXT PRIMARY KEY,
    path TEXT NOT NULL,
    provider_type TEXT NOT NULL,
    filename TEXT
);
```

## Design Rationale

### 1. Content Addressability
By using the `hash` as the Primary Key, the system ensures that the same content is never registered twice under different IDs, facilitating natural deduplication.

### 2. Provider Agnostic
The `provider_type` column allows the `ProviderManager` to dynamically route requests. Adding a new storage source (e.g., S3, IPFS, WebSocket) only requires adding a new provider implementation and registering its ID in the DB, without changing the table schema.

### 3. Path Flexibility
The `path` column is a generic string. This allows the same table to store:
- **Local files**: `uploads/2023/file.bin`
- **Remote files**: `https://remote-server.com/files/abc123...`

## Example Entry
| hash | path | provider_type | filename |
| :--- | :--- | :--- | :--- |
| `a1b2...` | `storage/test.txt` | `local` | `hello.txt` |
| `c3d4...` | `http://cdn.com/f1` | `http` | `image.png` |
