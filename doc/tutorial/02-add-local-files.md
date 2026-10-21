# Chapter 2: Add Local Files to Your Node

> Continues from Chapter 1: node is already running (signaling 9100 / node 3001 convention continues), panel can connect.
> This chapter solves "I have a local file, how to get it into the node and be able to see it."
> **No compilation needed throughout**: panel uses drag-and-drop, command line uses `curl` against local HTTP.

---

## 2.0 First Distinguish Two Things: Content Store ≠ Shared Manifest

This is the most common pitfall in this chapter, let's nail down the definitions first:

| | **Content Store (CAS)** | **Shared Manifest (Externally Visible)** |
|---|---|---|
| Answers what | What bytes does this node have | What does this node want to show others |
| Who can see | Only you (`/files` queries it) | Any connected peer (`share` frame) |
| How to add | "Local Ingest" / "Network Ingest" / `POST /files/upload` | File placed **inside a shared directory** and **registered into the index** |
| Storage location | `PEERDRIVE_STORAGE/<hash-first-2-chars>/<hash>` | File stays in place, not moved |
| Default state | Always available | **Default off** (`PEERDRIVE_SHARE_ENABLE=false`) |

In one sentence: **ingesting just puts content in, it doesn't mean external sharing**.
After successful ingest you get a `hash` that can be retrieved immediately (panel "Retrieve Verify"), but it won't show up in others' manifests — see §2.5.

---

## 2.1 Method 1: Panel "Local Ingest" (Drag Files in Browser)

Suitable for: temporarily adding stuff, wanting to verify retrieval immediately.

1. Panel connected to node (Chapter 1 §1.4)
2. Top-right "Select File" → can multi-select (`in-file` is `multiple`)
3. Click **Local Ingest**
4. Watch logs: `Ingest complete: <name> → sha256 <64-char hex>`

> Panel logs will prompt: **this content can be retrieved anywhere using this hash later**;
> to verify immediately, click **Retrieve Verify** on the task row — it re-pulls by hash and recalculates sha256.

Hard constraints in the implementation (all at the top of `panel/app.js`):

| Constraint | Value | Why |
|---|---|---|
| Single file warning threshold | 256 MB | `put()` reads entire content into browser memory before chunking, exceeding it causes large files to crash the tab mid-upload |
| Chunking | 64 KB, **serial** | One connection can only have one upload stream at a time |
| Per-round timeout | 30 s | Receiver needs to write to disk, don't treat "slow" as "hung" |
| Preview limit | 2 MB | Above this, save directly, don't enter preview box |

---

## 2.2 Method 2: `POST /files/upload` (Script / Server Scenarios)

Suitable for: batch, scheduled, machines without browsers.

```bash
curl -F "file=@./upload-me.txt" http://127.0.0.1:3001/files/upload
```

Actual response (v0.1.1, Windows):

```json
{"already_exists":false,"filename":"upload-me.txt",
 "hash":"b71c35903f41425f7173b1fe6c1c264d529c809492a3682573ef2e136b252488",
 "mime":"text/plain; charset=utf-8","size":22}
```

Uploading the same file again returns `"already_exists":true` — content-addressed storage, same content only stored once, no error.

Storage location (this is the CAS layout):

```
PEERDRIVE_STORAGE/b7/b71c35903f41425f7173b1fe6c1c264d529c809492a3682573ef2e136b252488
```

### Size Limits (Counter-intuitive, remember to check)

| Scenario | Limit | Environment Variable |
|---|---|---|
| Anonymous request | **10 MB** | `PEERDRIVE_MAX_UPLOAD_ANON_BYTES` |
| Authenticated user | 100 MB | `PEERDRIVE_MAX_UPLOAD_BYTES` |

**Single-node runs count as anonymous**: `AuthRequired` directly passes when no registration server is configured (otherwise local single-node returns 401 for everything),
but `authenticated` is still false ⇒ uses the anonymous 10 MB limit. To increase, set `PEERDRIVE_MAX_UPLOAD_ANON_BYTES`.

---

## 2.3 Method 3: Register Existing Files to Node (Use This to Make Them Appear in Manifest)

Files already on disk, don't want to copy them — use registration:

```bash
# Single file
curl -X POST -H 'Content-Type: application/json' \
  -d '{"path":"D:/.../shared/demo.txt","filename":"demo.txt"}' \
  http://127.0.0.1:3001/files/register_local

# Entire directory (recommended: register a batch at once)
curl -X POST -H 'Content-Type: application/json' \
  -d '{"folder_path":"D:/.../downloads/shared"}' \
  http://127.0.0.1:3001/files/register_folder
```

Actual result (directory has two files):

```json
{"registered":[{"filename":"demo.txt","hash":"c787d82a71a194ff..."},
                {"filename":"note.md","hash":"f957b19529906961..."}]}
```

**Registration ≠ Copy**: file content stays in place, the node just records its path and hash into the index.
So if the original file is deleted/modified, this index becomes invalid.

### Path Boundaries (Cases That Will Be Rejected)

Registration only accepts paths within these roots; out-of-bounds returns `path outside storage root`:

- `PEERDRIVE_STORAGE`
- `PEERDRIVE_DOWNLOAD_DIR`
- Directories already declared in `PEERDRIVE_SHARE_DIRS`

Also **hardlinked files are rejected** (to prevent boundary bypass), copy them if you really need to register.

---

## 2.4 Viewing: Three Perspectives

### ① Panel (Peer Perspective, i.e., "How Others See It")

- After connecting, logs: `Manifest: N collections · M files`
- Click an item → preview (images/audio/video render directly, text truncated at 2 MB)
- "Save" → writes to local; task row "Retrieve Verify" → pulls by hash and recalculates sha256

> `M > 0` means the peer actually shared something. **Empty manifest is a valid result** (other side didn't enable sharing/didn't declare directories), not an error.

### ② Local HTTP (Your Full View)

```bash
curl http://127.0.0.1:3001/files                       # Local index (includes provider_path)
curl http://127.0.0.1:3001/files/verify/<hash>         # Does a specific hash exist
curl 'http://127.0.0.1:3001/files/browse?path=.'       # Browse by directory
```

`/files` actual excerpt (note uploads and registrations are both here):

```json
{"hash":"f957b195...","filename":"note.md","size":12,
  "provider_path":"D:\\...\\shared\\note.md"},
 {"hash":"b71c3590...","filename":"upload-me.txt","size":22,
  "provider_path":"b7/b71c3590..."}
```

### ③ Ask from Another Node (Closest to Real Usage)

```bash
curl http://127.0.0.1:3002/peerjs/nodes/my-node-1/shares
```

Actual result (before registration → after registration):

```json
// Before registration
{"collections":[],"dirs":["D:\\...\\downloads\\shared"],"files":[],"peer":"my-node-1","total":0}
// After registration
{"collections":[],"dirs":["D:\\...\\downloads\\shared"],
 "files":[{"hash":"f957b195...","name":"note.md","path":"note.md","size":12},
          {"hash":"c787d82a...","name":"demo.txt","path":"demo.txt","size":22}],
 "peer":"my-node-1","total":2}
```

---

## 2.5 Why "Ingest Succeeded, But Not in Manifest"

This is not a bug, it's the two indexes having different responsibilities (`back/internal/service/nodeshare.go`):

- Shared manifest only lists entries in the **local file index whose `Path` falls within a shared directory**;
- But `upload` / panel "Local Ingest" writes to the **content store** (`file_meta` + provider),
  its `provider_path` is a CAS relative path (like `b7/b71c...`), **not in any shared directory** ⇒ filtered out.

Actual comparison (same batch of operations):

| Action | Visible in `/files` | Visible in peer `shares` |
|---|---|---|
| HTTP upload `upload-me.txt` (22 B) | ✅ | ❌ |
| Place `demo.txt` in shared directory + `register_folder` | ✅ | ✅ |

### Three Ways to Make It Appear in Manifest

1. **Put the file in a shared directory, then `register_folder`** (recommended, §2.3)
2. **Shared directory = your working directory**: point `PEERDRIVE_SHARE_DIRS` to where you normally store files,
   after one registration, files added later auto-appear in manifest (new files still need one more registration)
3. **Not in manifest is fine too**: if you just want "store it, retrieve anytime", use hash retrieval — panel "Retrieve Verify",
   or admin panel download by hash. Content is always there, just not broadcast externally

> The reverse is also true: **setting `PEERDRIVE_SHARE_DIRS` to the storage root directory won't make ingested content visible** —
> because it filters on `Path` in the index, and ingested content doesn't have a "directory" path record written to disk.

---

## 2.6 Limits and Defaults Quick Reference

| Item | Value | Source |
|---|---|---|
| HTTP upload (anonymous) | 10 MB | `PEERDRIVE_MAX_UPLOAD_ANON_BYTES` |
| HTTP upload (authenticated) | 100 MB | `PEERDRIVE_MAX_UPLOAD_BYTES` |
| Panel ingest warning threshold | 256 MB | `panel/app.js` `PUT_WARN_BYTES` |
| Panel chunking / per-round timeout | 64 KB serial / 30 s | Same as above |
| Preview limit | 2 MB | `PREVIEW_MAX_BYTES` |
| Shared manifest single-query limit | 1000 files | `NodeShare.SetFileLister` |
| Share switch | Default **off** | `PEERDRIVE_SHARE_ENABLE` |
| Shared directories | Default empty | `PEERDRIVE_SHARE_DIRS` (comma-separated) |

---

## 2.7 Self-Check Checklist

1. Node alive: `curl http://127.0.0.1:3001/ping` → `pong`
2. Content ingested: `curl http://127.0.0.1:3001/files/verify/<hash>` → has `filename`/`size`
3. Written to disk: `<storage>/<hash-first-2>/<hash>` exists
4. Sharing enabled: `/peerjs/nodes/<your-id>/shares` `dirs` non-empty (means `SHARE_DIRS` is effective)
5. It's in the manifest: find that `name` in `files` of the same response
6. Panel can retrieve: click "Retrieve Verify" → `sha256 matches ingest return value`

---

## 2.8 Symptom → Cause → Resolution

| Symptom | Cause | Resolution |
|---|---|---|
| Ingest succeeded, but not in manifest | Ingest writes to content store, shared manifest only shows registered indexes in shared directories | §2.5: put in shared directory + `register_folder` |
| Manifest `total:0`, `dirs` also empty | `PEERDRIVE_SHARE_ENABLE=false` or `SHARE_DIRS` not set | Set both, restart node |
| `path outside storage root` | Registration path not in storage / download / declared shared directories | Add directory to `PEERDRIVE_SHARE_DIRS` then register |
| Upload returns 413 / connection reset | Exceeded anonymous 10 MB | Increase `PEERDRIVE_MAX_UPLOAD_ANON_BYTES` |
| `already_exists: true` | Same content already stored (content-addressed, not a failure) | Use the returned hash |
| `psk: this node requires pre-shared key` (during panel ingest) | Panel PSK not filled / filled wrong | Fill the node's PSK in panel PSK box; **HTTP API is not affected by PSK** (doesn't use WebRTC) |
| Ingest times out midway | Single round 30s no response (receiver writing to disk / stuck) | Retry; above 256 MB don't use panel |
| Hardlinked file registration rejected | Boundary prevention hardlink check | Copy then register |

> Next: [Chapter 3: Share Levels — public / unlisted / private](03-share-levels.md)
> (things you put in, who do you show them to later); then
> [Chapter 4: Freely Choose What to Share](04-choose-what-to-share.md) (per-row selection + set level).
