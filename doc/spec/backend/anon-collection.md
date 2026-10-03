# Anonymous Collection Layer

## Overview
An anonymous collection is an immutable, content-addressed JSON file containing a set of `path → hash` mappings. Its unique identifier (hash) is determined by the SHA256 of the canonical JSON, so identical content always produces the same hash.

## Collection Structure

```json
{
  "version": 1,
  "friendly_name": "my-collection",
  "entries": [
    {"path": "dir/file.txt", "hash": "a1b2c3..."},
    {"path": "dir/other.txt", "hash": "d4e5f6..."}
  ],
  "created_at": "2026-04-25T12:00:00Z"
}
```

## API

### POST /anon/collections

Create an anonymous collection.

**Request body:**
```json
{
  "friendly_name": "my-collection",
  "entries": [
    {"path": "relative/path", "hash": "64-hex-chars"}
  ]
}
```

**Validation rules:**
- All `path` values must be relative paths, must not start with `/`, and must not contain `..`
- All `hash` values must be 64-character lowercase hexadecimal strings
- Entries are sorted lexicographically by `path` on the server side (to ensure determinism)

**Success response (201 Created):**
```json
{"hash": "sha256-of-the-json"}
```

**Error response (400 Bad Request):**
```json
{"error": "invalid path: ../etc/passwd"}
```

### GET /anon/collections/:hash

Retrieve the JSON content of an existing collection.

**Success response (200 OK):**
```json
{
  "version": 1,
  "entries": [...],
  "created_at": "2026-04-25T12:00:00Z"
}
```

**Error response (404 Not Found):**
```json
{"error": "collection not found"}
```

### GET /anon/collections/:hash/entries/*path

Download a file at a specified path from an anonymous collection.

**Parameters:**
- `:hash` — collection hash
- `*path` — file path within the collection entries

**Success response (200 OK):**
File content stream with a `Content-Disposition` header.

**Error responses:**
- Collection not found → 404
- Path not found in collection → 404
- File data unavailable → 404

### POST /anon/collections/fork

Create a new collection based on an existing one, with the ability to add or remove entries.

**Request body:**
```json
{
  "source_hash": "sha256-of-source-collection",
  "add_entries": [{"path": "...", "hash": "..."}],
  "remove_paths": ["path/to/remove"]
}
```

**Success response (201 Created):**
```json
{"hash": "sha256-of-the-new-collection"}
```

## Execution Flow

### Creation Flow
1. Validate the `path` and `hash` format of each entry
2. Sort entries lexicographically by `path`
3. Build an `AnonCollection` object (version=1, created_at=current UTC time)
4. Serialize to canonical JSON via `json.MarshalIndent`
5. Compute SHA256 -> hash
6. Write file to `storage/{hash[:2]}/{hash}`
7. Register `file_meta` (type=anon_collection, mime=application/json)
8. Register `file_providers` (provider_type=local, path=relative path)
9. Return hash

### Read Flow
1. Read file from `storage/{hash[:2]}/{hash}` by hash
2. Parse into `AnonCollection` via `json.Unmarshal`
3. Validate `version == 1`
4. Return parsed JSON

### File Download Flow
1. Read collection JSON
2. Match the `path` in entries
3. Obtain file stream via `Downloader.GetFileStream(entry.Hash)`
4. Stream back to the client

## Response Headers
- When downloading collection JSON via `GET /sha256sum/:hash`, if `file_meta.type == anon_collection`, add `X-Peerdrive-Collection: true` to the response headers

## Relationship with the Download Layer
- The collection JSON itself is a regular SHA256-addressed file and can be downloaded directly via `/sha256sum/:hash`
- Downloading files within a collection ultimately relies on the same code path as `/sha256sum/:hash` (reusing P2P fallback, multi-replica retry, etc.)

## Related Files

```
internal/model/anon.go             — AnonCollection / AnonCollectionEntry
internal/service/anon_service.go   — validation, sorting, creation, reading
internal/controller/anon.go        — HTTP entry points
internal/repository/anon_repo.go   — underlying storage
internal/controller/download.go    — X-Peerdrive-Collection response header
```

## Testing

Run `test_anon_collection.sh` to verify:
- Create collection (201)
- Path traversal interception (400)
- Read collection JSON (version=1)
- Download collection via sha256sum
- Download file from collection entry
- Fork collection and remove entry
