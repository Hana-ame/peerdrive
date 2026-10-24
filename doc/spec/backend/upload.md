# Upload Layer

## Overview
The upload feature is implemented through the `POST /files/upload` endpoint, storing files in the content-addressed storage system.  
Files are named by their SHA256 hash and stored in the configured storage directory, with metadata and provider information automatically registered.

## Configuration
- `PEERDRIVE_STORAGE`: Storage root directory, default `./storage`
- `PEERDRIVE_STORAGE_ENABLE`: Whether to enable storage write operations, default `true`
  - When set to `false`, all write operations such as upload and local file registration return 403 Forbidden

## Endpoint

### POST /files/upload
- Content-Type: `multipart/form-data`
- Form field: `file` (file)
- Success response (new file):
  - Status code: `201 Created`
  - Body: `{"hash": "...", "size": ..., "mime": "...", "filename": "...", "already_exists": false}`
- File already exists response:
  - Status code: `200 OK`
  - Body: `{"hash": "...", "size": ..., "mime": "...", "filename": "...", "already_exists": true}`
- When storage is disabled:
  - Status code: `403 Forbidden`
  - Body: `{"error": "storage is disabled"}`

## Execution Flow
1. Check `StorageEnable`; if `false`, immediately return 403.
2. Write the upload stream to a temporary file, calculating SHA256 and file size simultaneously.
3. Detect MIME type (based on first 512 bytes of content + extension fallback).
4. Query the `file_meta` table; if a record with the same hash already exists:
   - Return metadata directly with `already_exists: true`; **do not duplicate storage**.
5. If not exists:
   - Create directory `storage/{hash[:2]}/`, move temporary file to `storage/{hash[:2]}/{hash}`.
   - Insert record into `file_meta` table (hash, size, mime_type, filename, gziped=false, type=blob).
   - Insert record into `file_providers` table, with `provider_type` as `"local"` and `path` as `"{hash[:2]}/{hash}"` (relative to storage root).
6. Return `201 Created` with metadata.

## Integration with Download Layer
- `LocalProvider` concatenates `provider.path` with `StorageDir` to read files.
- After upload, files can be immediately downloaded via `GET /sha256sum/{hash}`.
