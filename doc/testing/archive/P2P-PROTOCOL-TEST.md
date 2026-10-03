# Peerdrive P2P Protocol Test Matrix

> 2026-05-03

## Protocols

There are two libp2p stream handlers on the P2P layer, with no separate port:

| Protocol ID | Purpose | Status |
|-------------|------|------|
| `/peerdrive/collections/list/1.0.0` | Query the peer's collections list and existence | ✅ Implemented |
| `/peerdrive/exchange/1.0.0` | Query/download files by SHA256 | ✅ Implemented |

---

## 1. Collections List — `/peerdrive/collections/list/1.0.0`

### 1.1 List All Public Collections

| Test ID | Test Item | Operation | Expected Result | Verification |
|--------|--------|------|----------|----------|
| L-01 | Empty query lists all public collections | Send `\n` (empty query) | `OK <size>\n` + JSON, all `user_collections` have visibility=public | Every visibility field is "public" |
| L-02 | Peer has no public collections | Send `\n` to an empty node | `OK <size>\n` + `{"user_collections":[],"anon_collections":[]}` | Empty JSON arrays |
| L-03 | Peer has anonymous collections but no user collections | Send `\n` | `anon_collections` non-empty, `user_collections` possibly empty | Anon list shows all stored anonymous collections |
| L-04 | Response JSON structure integrity | Parse all fields | id, username, collection_name, current_hash, visibility, tags, created_at | Field types correct, none missing |
| L-05 | Large response not truncated | Peer has 100+ collections | Fully receive all JSON | JSON parse length matches OK declaration |

### 1.2 Query Collection by Name

| Test ID | Test Item | Operation | Expected Result | Verification |
|--------|--------|------|----------|----------|
| L-06 | Fuzzy query by collection_name | Send `my-coll\n` | Return collections whose collection_name contains "my-coll" | LIKE semantics: return matching entries |
| L-07 | Fuzzy query by username | Send `alice\n` | Return collections whose username contains "alice" | Partial username match |
| L-08 | Combined match (name + user) | Send `test\n` | Return if either username or collection_name matches | OR semantics |
| L-09 | Query with no matches | Send `zzzznonexistent\n` | Empty result `user_collections:[]` | Does not return ERR |
| L-10 | Query with special characters | Send `test@#$%\n` | Handle correctly, no SQL injection risk | Does not crash, returns empty or safe result |
| L-11 | Long query string | Send 1000-character query | Truncate or handle normally, no crash | Server remains stable |

### 1.3 Check Collection Existence

| Test ID | Test Item | Operation | Expected Result | Verification |
|--------|--------|------|----------|----------|
| L-12 | Known existing collection | Query a known collection_name | Result count > 0 | Exact match |
| L-13 | Non-existent collection | Query a random non-existent name | Result count = 0 | Judged as non-ERR |
| L-14 | Empty string query for existence | Send `\n` and check count > 0 | If peer has any public collection, count > 0 | Boolean check |

### 1.4 Visibility Filtering Extension (to be implemented)

> Currently **only returns collections with visibility=public**. Future support will be needed for filtering by visibility,
> allowing queries for unlisted and private levels (requires authentication).

| Test ID | Test Item | Expected Result |
|--------|--------|----------|
| L-15 | Request visibility=unlisted | Return unlisted collections |
| L-16 | Request visibility=private (with permission) | Return private collections |
| L-17 | Request visibility=private (without permission) | Return ERR or only public results |
| L-18 | Request visibility=all (with permission) | Return all visibilities |
| L-19 | Unauthenticated request for private → only public returned | Permission isolation |

### 1.5 Edge/Security

| Test ID | Test Item | Operation | Expected Result | Verification |
|--------|--------|------|----------|----------|
| L-20 | Query without connecting to peer | Call NewStream without connecting | Connection error | Return error, do not panic |
| L-21 | Query after peer disconnects | connect → peer disconnects → query again | Stream error | Timeout or connection reset |
| L-22 | Query with oversized payload | Construct oversized JSON response (if applicable) | OK declaration matches actual size | Read all size bytes |
| L-23 | ERR format response | If peer returns `ERR xxx\n` | Client recognizes ERR prefix | Does not attempt to read JSON |

---

## 2. Exchange — `/peerdrive/exchange/1.0.0`

### 2.1 File Query

| Test ID | Test Item | Operation | Expected Result | Verification |
|--------|--------|------|----------|----------|
| E-01 | Query existing file (small) | Send `<64hex>\n`, known <1KB | `OK <size>\n<data>` | SHA256 of data matches requested hash |
| E-02 | Query existing file (large) | Send `<64hex>\n`, known >10MB | `OK <size>\n<data>` | Fully received, SHA256 matches |
| E-03 | Query non-existent hash | Send `000...001\n` | `ERR not found\n` | Error prefix ERR |
| E-04 | Invalid hash length (short) | Send `abc\n` | `ERR invalid hash length ...\n` | Error message includes length info |
| E-05 | Invalid hash length (long) | Send 128 hex chars | `ERR invalid hash length ...\n` | Same as above |
| E-06 | Empty request | Send `\n` | `ERR bad request\n` or `ERR invalid hash length 0\n` | Does not crash |
| E-07 | Special characters | Send `not-a-hex!\n` | `ERR invalid hash length ...\n` | Validated by length |

### 2.2 SIZE Command

| Test ID | Test Item | Operation | Expected Result | Verification |
|--------|--------|------|----------|----------|
| E-08 | SIZE of an existing file | Send `SIZE <64hex>\n` | `OK <size>\n` | size is a positive integer, equals actual file size |
| E-09 | SIZE of a non-existent file | Send `SIZE 000...001\n` | `ERR not found\n` | Error response |
| E-10 | SIZE with invalid hash | Send `SIZE abc\n` | `ERR invalid hash length ...\n` | Length validation |
| E-11 | SIZE boundary (0-byte file) | Send `SIZE <empty-file-hash>\n` | `OK 0\n` | size=0 |

### 2.3 Reliability & Performance

| Test ID | Test Item | Operation | Expected Result | Verification |
|--------|--------|------|----------|----------|
| E-12 | Concurrent download of multiple files | Concurrent goroutine requests for different hashes | All succeed and content matches | Verify SHA256 one by one |
| E-13 | Large file timeout protection | Request >100MB file with short timeout | Read partial then timeout, no OOM | Client controls memory |
| E-14 | Binary data integrity | Request binary file (.bin, .zip) | `OK <size>\n<data>` data contains all bytes including nulls | Read all size bytes |
| E-15 | Repeated requests for same hash | Request the same hash twice consecutively | Both responses are identical | Data completely matches |

---

## 3. Connection Layer Tests

| Test ID | Test Item | Operation | Expected Result | Verification |
|--------|--------|------|----------|----------|
| N-01 | Direct connect to known peer | `/ip4/<IP>/tcp/<port>/p2p/<peerID>` | Connect succeeds | No error |
| N-02 | Connect through relay | `/ip4/<relay>/tcp/<port>/p2p/<relayID>/p2p-circuit/p2p/<targetID>` | Connect succeeds | Relay forwards |
| N-03 | Connect to invalid address | `/ip4/1.2.3.4/tcp/9999/p2p/<randomID>` | Connection timeout/refused | Returns error within reasonable time |
| N-04 | Send protocol after connecting | connect → NewStream | Stream opens successfully | Read/write available |
| N-05 | Multiple sequential requests | Multiple sequential collections/list + exchange on same connection | All succeed | Not affected by previous requests |
| N-06 | Peer with incompatible version | Connect to peer without this handler | Stream refused/protocol unsupported | Does not panic |

---

## 4. Test Topology

```
┌──────────────────────────────────────┐
│  p2p-test (this tool)                │
│  ─────────────                       │
│  Bare go-libp2p host                 │
│  No DHT / No Relay / No Storage      │
│  Stream layer protocol only           │
│                                      │
│  CLI params:                         │
│    --peer  target peer multiaddr     │
│    --op    operation (list/query/    │
│             exist/get/size)           │
│    --query  query string             │
│    --hash  SHA256 hex                │
│    --out   download output path      │
└──────────┬───────────────────────────┘
            │ libp2p stream
            ▼
┌──────────────────────────────────────┐
│  Target Peerdrive Node               │
│  ────────────────                     │
│  Handler: /peerdrive/collections/list │
│  Handler: /peerdrive/exchange         │
└──────────────────────────────────────┘
```

## 5. Quick Run

```bash
# List all public collections on peer
go run -tags nosqlite ./cmd/p2p-test/main.go \
  --peer "/ip4/97.64.30.221/tcp/37537/p2p/12D3KooWFgwXYLY84fi6UgwT3k4KFbDVdU7nKzAoErz2eskvrW33" \
  --op list

# Query by name
go run -tags nosqlite ./cmd/p2p-test/main.go \
  --peer "/ip4/97.64.30.221/tcp/37537/p2p/12D3KooWFgwXYLY84fi6UgwT3k4KFbDVdU7nKzAoErz2eskvrW33" \
  --op query --query "test"

# Check if a collection exists
go run -tags nosqlite ./cmd/p2p-test/main.go \
  --peer "/ip4/97.64.30.221/tcp/37537/p2p/12D3KooWFgwXYLY84fi6UgwT3k4KFbDVdU7nKzAoErz2eskvrW33" \
  --op exists --query "my-collection"

# Get a file
go run -tags nosqlite ./cmd/p2p-test/main.go \
  --peer "/ip4/97.64.30.221/tcp/37537/p2p/12D3KooWFgwXYLY84fi6UgwT3k4KFbDVdU7nKzAoErz2eskvrW33" \
  --op get --hash <64hex> --out /tmp/downloaded

# Query file size
go run -tags nosqlite ./cmd/p2p-test/main.go \
  --peer "/ip4/97.64.30.221/tcp/37537/p2p/12D3KooWFgwXYLY84fi6UgwT3k4KFbDVdU7nKzAoErz2eskvrW33" \
  --op size --hash <64hex>
```
