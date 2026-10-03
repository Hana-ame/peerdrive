# Peerdrive Backend Design Reference: "Node User" Approach

## One-Sentence Summary

In the backend, **the username is merely a namespace dimension, not an identity**.
There is no login, registration, authentication, or user table. Anyone can perform any operation with any username.

---

## User Model Breakdown

### No User Table

The database has the following tables:

```
files               → file metadata (hash, provider_type, path, filename)
collections         → collections (id, username, collection_name, created_at) ← username here
collection_entries  → collection entries (collection_id, path, file_hash)
collection_versions → version snapshots
version_entries     → version entries
transfer_tasks      → async tasks
```

Source: `internal/repository/db.go`

### username Is a Column in the Collections Table

```sql
CREATE TABLE collections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL,
    collection_name TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(username, collection_name)   -- ← unique constraint here
);
```

- `username` + `collection_name` form a composite unique key
- It does not reference any other table; there are no foreign key constraints

### All Endpoints Accept username as a URL Parameter or JSON Field

Two acquisition methods:

**Method A: URL Path Parameter**
```
GET  /collections/:username              → listCollections
GET  /collections/:username/:coll        → getCollection
POST /collections/:username/:coll/entries → addEntry
POST /collections/:username/:coll/commit → commitVersion
DELETE /collections/:username/:coll/entries/:path → removeEntry
GET  /:username/:coll_name/*filepath     → downloadCollectionFile
```

`c.Param("username")` extracts it directly with no validation whatsoever.

**Method B: JSON Request Body**
```
POST /collections           body: {"username":"alice", "collection_name":"music"}
POST /actions/fork           body: {"username":"bob", "source_username":"alice", ...}
POST /actions/merge          body: {"username":"alice", "source_username":"bob", ...}
POST /actions/pull           body: {"username":"alice", "source_username":"bob", ...}
```

Again, no validation whatsoever. Anyone can create collections under any username, or fork/merge any user's data.

Source: `internal/controller/collection.go`, `internal/controller/fork.go`, `internal/controller/merge.go`

---

## P2P Node Identity vs Username

These two concepts are **completely independent** with no relationship whatsoever.

| Dimension | P2P Node Identity (peer.ID) | Username (username) |
|------|------------------------|-------------------|
| Source | libp2p cryptographic key pair | Any arbitrary string in an HTTP request |
| Format | `12D3KooW...` (Base58-encoded multihash) | Free-form string (e.g. `alice`, `bob`) |
| Globality | Cryptographically unique | No global uniqueness; only `UNIQUE(username, coll_name)` ensures collection names are unique per user |
| Endpoints Used | `/p2p/node`, `/p2p/peers`, `/p2p/ping/:id` | All `/collections/*`, `/actions/*`, `/:username/:coll/*` |
| Verification | Cryptographic verification (peer.ID derived from public key) | Zero verification |
| Lifecycle | Generated at service startup / persisted to a key file | Request-level string, discarded after use |

P2P controller source: `internal/controller/p2p.go:1-8` explicitly states it only deals with `peer.ID` and `multiaddr`, with no username involved.

---

## username in the Request Lifecycle

Using the example of uploading a file to a collection and committing a version:

```
Browser enters alice/my_project
         │
         ▼
GET /collections/alice/my_project
         │  c.Param("username") = "alice"
         │  c.Param("collection_name") = "my_project"
         ▼
repository.GetCollection("alice", "my_project")
         │  SELECT * FROM collections WHERE username=? AND collection_name=?
         ▼
Returns { entries: [...] }
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
Returns { version_id, version_number }
```

No step verifies whether the **requestor** is `alice`. If Bob knows Alice's collection name, he can:
- `POST /collections` → Create `bob/alice_stuff`
- `POST /actions/fork` → Fork `alice` → `bob/`
- `POST /collections/bob/alice_stuff/entries` → Edit freely

---

## "Node User" Conclusion

**The backend has no concept of a node user.** All user data is stored in the same SQLite database, using the `username` column for logical partitioning. Any HTTP client can freely specify any username for read/write operations. Security relies on:
1. Network isolation (listening only on localhost or LAN)
2. Node-to-node access control within the P2P network (not implemented, planned for v2)

---

## Key Files Checklist

| File Path | Package Comment Summary | Relationship to User/Auth |
|----------|-----------|-------------------|
| `cmd/server/main.go` | Startup entry: DB → provider → downloader → P2P → router | No user initialization |
| `internal/router/router.go` | Route registration: /ping, /p2p/, /files/, /collections/, /actions/, /:user/:coll/ | username passed as URL parameter, no validation |
| `internal/controller/collection.go` | Collection CRUD + entries + version control | All functions get username via `c.Param("username")`, passed directly to repo |
| `internal/controller/fork.go` | fork/pull operations | Reads username (target) and source_username (source) from JSON body, no permission check |
| `internal/controller/merge.go` | merge operations (three strategies) | Also reads from JSON body, no validation |
| `internal/controller/p2p.go` | libp2p node info, peers, ping | Completely does not involve username, only operates on peer.ID |
| `internal/service/p2p.go` | libp2p node lifecycle (NewP2PService, GetNodeInfo, PingPeer) | No username concept |
| `internal/service/downloader.go` | Content-addressed download stream | No username concept |
| `internal/repository/db.go` | Table creation SQL for 6 tables | `collections` table contains `username TEXT NOT NULL` + `UNIQUE(username, collection_name)` |
| `internal/repository/collection_repo.go` | CRUD for four collection tables | All queries filter with `WHERE username=?`, no authentication |
| `internal/model/collection.go` | Collection / CollectionEntry / CollectionVersion / VersionEntry structs | Collection struct contains `Username string` field |
