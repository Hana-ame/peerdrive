# Peerdrive API Behavior Documentation (v2)

> Backend Go + Gin, SQLite storage. Unless otherwise specified, all requests use Content-Type `application/json`.
> Authenticated requests must include in Header: `Authorization: Bearer <authkey>`.
> Base URL example: `http://wsl-3000.moonchan.xyz`

---

## Authentication API (Auth)

### POST /auth/register
User registration.
- **Body**: `{ "username": "...", "password": "..." }`
- **Success**: `{ "authkey": "...", "username": "..." }`
- **Error**: 409 User already exists.

### POST /auth/login
User login.
- **Body**: `{ "username": "...", "password": "..." }`
- **Success**: `{ "authkey": "...", "username": "..." }`
- **Error**: 401 Invalid credentials.

### POST /auth/logout
Logout.
- **Header**: `Authorization: Bearer <authkey>`
- **Success**: `{ "message": "logged out successfully" }`

### GET /auth/me
Get current logged-in user info.
- **Header**: `Authorization: Bearer <authkey>`
- **Success**: `{ "id": ..., "username": "...", "created_at": "...", ... }`

---

## Files and Content Addressed Storage (CAS)

### GET /sha256sum/:sha256
Download file by SHA256 hash.
- **Logic**:
    1. Loop through replicas marked `available=1` in `file_providers` table (prefer local).
    2. If all replicas fail, trigger **P2P Bitswap fallback**: attempt to pull from network peers.
    3. After successful pull, auto-cache locally and update DB.
- **Headers**: 
    - `Content-Disposition: attachment; filename="..."`
    - If metadata marks `is_gzip: true` $\rightarrow$ `Content-Encoding: gzip`
- **Error**: 404 File not found.

### POST /files/upload (authenticated)
Upload file. Content-Type: `multipart/form-data`.
- **Logic**: Calculate SHA256 $\rightarrow$ Store to `storage/{h[:2]}/{h}` $\rightarrow$ Write to `file_meta` and `file_providers`.
- **Success**: `{ "hash": "...", "filename": "..." }`

### POST /files/register_local (authenticated)
Register an existing local file.
- **Body**: `{ "path": "relative/path", "filename": "display_name" }`
- **Logic**: Calculate SHA256 $\rightarrow$ Write to `file_meta` and `file_providers` (provider_type='local') $\rightarrow$ Don't copy file.

### POST /files/register_folder (authenticated)
Batch register a folder.
- **Body**: `{ "folder_path": "subdir" }`
- **Success**: `{ "registered": [{ "filename": ..., "hash": ... }, ...] }`

### GET /files/verify/:hash (authenticated)
Query file metadata.
- **Success**: Returns complete file metadata and replica info.

### DELETE /files/:hash (authenticated)
Delete file record and its local physical file.

---

## Collection Management (Collections)

### GET /collections/search (public)
Search collections.
- **Query**: `?q=keyword` (matches username or collection name)
- **Success**: `{ "data": [ { "id", "username", "collection_name", "current_hash", ... }, ... ] }`

### POST /collections (authenticated)
Create new collection.
- **Body**: `{ "username": "...", "collection_name": "..." }`
- **Success**: `{ "id": ..., "username": "...", "collection_name": "..." }`

### GET /collections/:username (authenticated)
List all collections of this user.

### GET /collections/:username/:collection_name (authenticated)
Get collection details and current entries.
- **Logic**: If `current_hash` exists, prefer downloading and parsing the corresponding **anonymous collection JSON**; otherwise fallback to `collection_entries` table.

### POST /collections/:username/:collection_name/entries (authenticated)
Add/update entry (upsert).
- **Body**: `{ "path": "...", "hash": "..." }`

### DELETE /collections/:username/:collection_name/entries/*path (authenticated)
Remove entry.

### POST /collections/:username/:collection_name/commit (authenticated)
Commit version.
- **Logic**: 
    1. Serialize current entries to **anonymous collection JSON** $\rightarrow$ Store and get `hash`.
    2. Update `collections.current_hash` to this `hash`.
    3. Record version snapshot in `collection_versions`.
- **Success**: `{ "message": "committed", "version_number": ..., "snapshot_hash": "..." }`

### GET /collections/:username/:collection_name/log (authenticated)
Get version history.

### POST /collections/:username/:collection_name/rollback/:version_id (authenticated)
Roll back to specified version.
- **Logic**: Restore `collection_entries` $\rightarrow$ Regenerate anonymous snapshot $\rightarrow$ Update `current_hash`.

---

## Collaboration and P2P

### POST /actions/fork (authenticated)
Copy remote collection to local.

### POST /actions/merge (authenticated)
Merge remote collection into local. Supports `ours`, `theirs`, `manual` strategies.

### GET /p2p/node (public)
Returns node PeerID and listen addresses.

### GET /p2p/peers (public)
Returns current connected peer node list.

---

## Database Schema (v2)

- **`users`**: User accounts and `authkey`.
- **`file_meta`**: Unique content metadata (hash PK).
- **`file_providers`**: Multiple physical locations for one hash (hash FK).
- **`collections`**: User collection pointer (current_hash $\rightarrow$ file_meta).
- **`collection_entries`**: Workspace entries.
- **`collection_versions` / `version_entries`**: Historical version snapshots.
- **`transfer_tasks`**: Async task tracking.
