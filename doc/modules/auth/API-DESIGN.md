# Peerdrive Auth Module API Design

## Overview

The Peerdrive auth system uses a **Registration Server** for centralized user management, JWT token issuance, relay node registry, comments, and stats. Peerdrive nodes use the registration server's `/auth/whoami` endpoint to validate Bearer tokens via a middleware layer.

| Component | Base URL | Purpose |
|---|---|---|
| Registration Server | `http://<host>:4000` | Auth, relay registry, comments, stats |
| Peerdrive Node | `http://<host>:3000` | P2P auth status, middleware validation |

---

## 1. Registration Server Endpoints

### 1.1 Health Check

```
GET /ping
```

**Response `200`**
```json
{
  "status": "ok",
  "service": "peerdrive-registration"
}
```

**Curl**
```bash
curl -s http://localhost:4000/ping
```

---

### 1.2 User Registration

```
POST /auth/register
```

**Request**
```json
{
  "username": "alice",
  "password": "securepass123",
  "role": "user"
}
```
- `username`: 3-32 chars, required
- `password`: 6+ chars, required
- `role`: optional, defaults to `"user"`; set `"admin"` for admin privileges

**Response `201`**
```json
{
  "token": "eyJhbGciOiJIUzI1NiIs...",
  "username": "alice",
  "role": "user"
}
```

**Response `409`** (username taken)
```json
{
  "error": "username already exists"
}
```

**Curl**
```bash
curl -s -X POST http://localhost:4000/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"securepass123"}'
```

---

### 1.3 User Login

```
POST /auth/login
```

**Request**
```json
{
  "username": "alice",
  "password": "securepass123"
}
```

**Response `200`**
```json
{
  "token": "eyJhbGciOiJIUzI1NiIs...",
  "username": "alice",
  "role": "user"
}
```

**Response `401`**
```json
{
  "error": "invalid username or password"
}
```

**Curl**
```bash
curl -s -X POST http://localhost:4000/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"securepass123"}'
```

---

### 1.4 Get Current User (WhoAmI)

```
GET /auth/whoami
Authorization: Bearer <token>
```

**Response `200`**
```json
{
  "username": "alice",
  "role": "user"
}
```

**Response `401`**
```json
{
  "error": "missing authorization header"
}
```
or
```json
{
  "error": "invalid or expired token"
}
```

**Curl**
```bash
# With valid token
curl -s http://localhost:4000/auth/whoami \
  -H 'Authorization: Bearer eyJhbGciOiJIUzI1NiIs...'

# Without token (expect 401)
curl -s http://localhost:4000/auth/whoami
```

---

### 1.5 List Users (Admin only)

```
GET /auth/list
Authorization: Bearer <admin-token>
```

**Response `200`**
```json
{
  "users": [
    {
      "id": 1,
      "username": "alice",
      "role": "user",
      "created_at": "2026-04-28T12:00:00Z"
    }
  ],
  "total": 1
}
```

**Response `403`** (non-admin user)
```json
{
  "error": "admin role required"
}
```

**Curl**
```bash
curl -s http://localhost:4000/auth/list \
  -H 'Authorization: Bearer <admin-token>'
```

---

### 1.6 Get User Groups

```
GET /auth/group/:username
Authorization: Bearer <token>
```

**Response `200`**
```json
{
  "username": "alice",
  "groups": [
    {
      "user_id": 1,
      "group_id": 1,
      "username": "alice",
      "group_name": "peerdrive-users",
      "created_at": "2026-04-28T12:05:00Z"
    }
  ]
}
```

**Response `200`** (no groups → empty array)
```json
{
  "username": "alice",
  "groups": []
}
```

**Curl**
```bash
curl -s http://localhost:4000/auth/group/alice \
  -H 'Authorization: Bearer <token>'
```

---

### 1.7 Add User to Group

```
POST /auth/group/:username
Authorization: Bearer <token>
```

**Request**
```json
{
  "group_name": "peerdrive-users"
}
```
The group is created on the fly if it does not exist.

**Response `200`**
```json
{
  "status": "added to group",
  "username": "alice",
  "group": "peerdrive-users"
}
```

**Curl**
```bash
curl -s -X POST http://localhost:4000/auth/group/alice \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <token>' \
  -d '{"group_name":"peerdrive-users"}'
```

---

### 1.8 Register Relay Node

```
POST /p2p/relay/register
```

**Request**
```json
{
  "peer_id": "12D3KooW...",
  "addrs": ["/ip4/1.2.3.4/tcp/4001"],
  "storage_mb": 10240,
  "version": "1.0.0"
}
```
No authentication required. The registration server stores the node and sets `last_heartbeat`.

**Response `200`**
```json
{
  "status": "registered"
}
```

**Curl**
```bash
curl -s -X POST http://localhost:4000/p2p/relay/register \
  -H 'Content-Type: application/json' \
  -d '{"peer_id":"12D3KooW...","addrs":["/ip4/1.2.3.4/tcp/4001"],"storage_mb":10240,"version":"1.0.0"}'
```

---

### 1.9 List Relay Nodes

```
GET /p2p/relay/list
```

**Response `200`**
```json
{
  "relays": [
    {
      "peer_id": "12D3KooW...",
      "addrs": ["/ip4/1.2.3.4/tcp/4001"],
      "storage_mb": 10240,
      "load_pct": 0,
      "version": "1.0.0",
      "registered_at": "2026-04-28T12:00:00Z",
      "last_heartbeat": "2026-04-28T12:05:00Z"
    }
  ]
}
```
Only relays with heartbeat within the last 5 minutes are returned.

**Curl**
```bash
curl -s http://localhost:4000/p2p/relay/list
```

---

### 1.10 Relay Heartbeat

```
POST /p2p/relay/heartbeat
```

**Request**
```json
{
  "peer_id": "12D3KooW...",
  "load_pct": 42.5
}
```
Updates `last_heartbeat` and `load_pct` for the relay node.

**Response `200`**
```json
{
  "status": "ok"
}
```

**Curl**
```bash
curl -s -X POST http://localhost:4000/p2p/relay/heartbeat \
  -H 'Content-Type: application/json' \
  -d '{"peer_id":"12D3KooW...","load_pct":42.5}'
```

---

### 1.11 Get Comments (by collection hash)

```
GET /comments/:hash
```

No authentication required. Returns all comments for a given collection hash.

**Response `200`**
```json
{
  "hash": "abc123...",
  "comments": [
    {
      "id": 1,
      "hash": "abc123...",
      "username": "alice",
      "content": "Great collection!",
      "created_at": "2026-04-28T12:10:00Z"
    }
  ],
  "total": 1
}
```

**Curl**
```bash
curl -s http://localhost:4000/comments/abc123def456
```

---

### 1.12 Post Comment (auth required)

```
POST /comments/:hash
Authorization: Bearer <token>
```

**Request**
```json
{
  "content": "Great collection!"
}
```

**Response `201`**
```json
{
  "id": 1,
  "hash": "abc123...",
  "username": "alice",
  "content": "Great collection!",
  "created_at": "2026-04-28T12:10:00Z"
}
```

**Response `401`** (no auth)
```json
{
  "error": "authentication required"
}
```

**Curl**
```bash
curl -s -X POST http://localhost:4000/comments/abc123def456 \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <token>' \
  -d '{"content":"Great collection!"}'
```

---

### 1.13 Registration Server Stats

```
GET /stats
```

**Response `200`**
```json
{
  "total_users": 5,
  "active_relays": 2,
  "total_comments": 12
}
```

**Curl**
```bash
curl -s http://localhost:4000/stats
```

---

## 2. Peerdrive Node Auth Endpoints

### 2.1 P2P Auth Status

```
GET /p2p/auth/status
Authorization: Bearer <token> (optional)
```

Returns the authentication status as determined by the peerdrive node's auth middleware (which validates the token against the registration server's `/auth/whoami`). Optional auth — with or without a token the endpoint succeeds.

**Response `200`** (with valid token)
```json
{
  "authenticated": true,
  "username": "alice",
  "role": "user"
}
```

**Response `200`** (no token)
```json
{
  "authenticated": false,
  "username": "",
  "role": ""
}
```

**Curl**
```bash
# With token
curl -s http://localhost:3000/p2p/auth/status \
  -H 'Authorization: Bearer <token>'

# Without token
curl -s http://localhost:3000/p2p/auth/status
```

---

## 3. Auth Middleware Design

The peerdrive node runs a Gin middleware that intercepts every request and validates Bearer tokens:

```
AuthOptional() — sets authenticated=false when no/invalid token, never rejects
AuthRequired() — returns 401 when no/invalid token
```

Both middleware functions call `GET <reg-server>/auth/whoami` with the token to validate it. The validation result is cached in the Gin context as `authenticated`, `username`, and `role`.

**Middleware config** (in peerdrive node env):
```
PEERDRIVE_REG_SERVER=http://localhost:4000
```

When `PEERDRIVE_REG_SERVER` is empty, auth is effectively disabled (all requests pass through as unauthenticated).

---

## 4. Relay Registry (peerdrive node → reg server)

Peerdrive nodes with P2P enabled and `PEERDRIVE_REG_SERVER_URL` set automatically:

1. **Register** as a relay via `POST /p2p/relay/register` at startup
2. **Heartbeat** every 60 seconds via `POST /p2p/relay/heartbeat`
3. **Bootstrap** connections by fetching the relay list via `GET /p2p/relay/list`

The registration server only returns relays with heartbeat within the last 5 minutes.

---

## 5. Test Users

For development and testing:

| Username | Password | Role |
|---|---|---|
| `testadmin` | `admin123` | `admin` |
| (dynamic test user) | `testpass123` | `user` |

---

## 6. Node directory: user ↔ node (design proposal, 2026-09-19)

**Original requirement**: "A Peerdrive Node itself should log into the registration
server with the user account at startup and report the whole node's information
(not just the relay). Other nodes can query via the registration server 'who is
operating this peer.'" "A node has public endpoints to query info, one of which is
the operator's account."

Note the difference from section 4: section 4 only lets relays register
**anonymously**; this section registers the entire node **with an account**.

### 6.1 Endpoints

```
POST /nodes/register      # Node registers itself with user token (idempotent upsert)
POST /nodes/heartbeat     # 60s heartbeat, refreshes last_seen / addresses / online status
DELETE /nodes/{peer_id}   # Deregister (or auto-offline on heartbeat timeout)
GET  /nodes/{peer_id}     # Public query: who is operating this peer
GET  /users/{username}/nodes  # Which online nodes does a given account have
```

`POST /nodes/register` request body (fields are all node self-descriptions; the
server only validates and stores):

```json
{
  "peer_id": "pd-7f3a1c2e",
  "endpoints": ["wss://node-a.example/peerjs"],
  "capabilities": { "relay": true, "bt": false, "ipfs": false },
  "version": "v0.4.0",
  "visibility": "public"
}
```

### 6.2 Visibility (same semantics as anonymous collection's three tiers, so the frontend can reuse the same UI copy)

| visibility | Who can query | Description |
|---|---|---|
| `public` | Anyone (including unauthenticated) | Default; `GET /nodes/{peer_id}` returns the operator account |
| `registered` | Authenticated users | Anonymous query only sees "exists but not visible" |
| `private` | Only the operator | Unauthorized query returns 404 (does not confirm existence) |

### 6.3 Key decisions (why this approach)

1. **Unregistered nodes run normally, they just aren't in the directory**. The
   directory is an "optional value-add" not a runtime prerequisite —— consistent with
   collection `owner` semantics (no operator = unowned, but still usable).
2. **`peer_id` is the unique key, not the account**. The same account can operate
   multiple nodes; registering the same `peer_id` under a different account is treated
   as **transfer of ownership** (requires confirmation from the old account within the
   validity period, otherwise it equals letting anyone squat someone else's peer_id).
   Squatting is the most likely hole in this module: the register interface must verify
   "the current request's token matches the peer_id's current owner, or the peer_id
   has not yet been registered".
3. **Operator account is a publicly queryable field**, so at registration the user must
   explicitly choose visibility, not default public (default value is set to `public`
   for ease of discovery, but the UI must explicitly show "others can see your account").
4. After heartbeat timeout (5 minutes, same as relay), node is offlined from the
   directory, avoiding zombie nodes being treated as trusted operators.

---

## 7. Traffic statistics and anti-falsification (design proposal, 2026-09-19)

**Original requirement**: "Save some statistics like upload/download amounts" "Count
upload/download info (needs anti-falsification)" "Statistics are verified and uploaded
to the centralized registration/auth server" "Relay nodes can also count relayed traffic".

### 7.1 Definition (define clearly first, otherwise "anti-falsification" has no foothold)

- Counted object is **verified content bytes**: only complete/chunked transfers that
  pass sha256 verification are counted (`FetchFromPeer`'s `fetchReader` already
  verifies at EOF, naturally a billing anchor).
- Three dimensions: `direction` (up / down) × `channel` (direct / relay) × `peer_id`.
  `channel=relay` traffic is counted once by both ends and once by the relay, for
  cross-verification in 7.2.
- Only **aggregated values** are reported (bytes and sessions per peer per 5-min
  window); file hashes or filenames are not reported —— statistics ≠ content
  monitoring.

### 7.2 Anti-falsification: four gates (core design)

A unilateral node can always falsely report its traffic, so **any single-party data is
untrustworthy** and must be cross-checked:

| # | Mechanism | Approach | Falsification blocked |
|---|---|---|---|
| 1 | **Peer countersigning** | Every 5-minute window (or at session end) both sides exchange count summaries, signed with the node's Ed25519 private key (`countersign`). Reports carry the peer's signature. Server only accepts records where **both sides' counts are within tolerance**; billing takes `min(A_reported, B_reported)` | Unilateral inflation (inflating requires collusion with the peer, greatly raising the cost) |
| 2 | **Relay backstop** | `channel=relay` traffic is independently counted and reported by the relay node (third party), cross-checked against both ends | Collusion between both ends (relay is an independent observer) |
| 3 | **Sample challenge** | Server randomly challenges a node to return data for a random range of a specified hash + compute sha256; if the node has no such data/didn't do those transfers, the challenge must fail | Pure fabrication (no real transfer records) |
| 4 | **Physical caps + drift detection** | Single-window traffic ≤ bandwidth cap × window duration; moving average of a node's historical baseline, exceeding a threshold (e.g. 3σ) is not billed first and triggers manual/auto review | Order-of-magnitude absurd values |

**Reputation and consequences**: consecutive N windows failing verification → marked
`untrusted` (no relay tasks assigned, not participating in traffic leaderboard),
reflected in the node directory. **No auto-ban** —— false positive cost is higher than
the benefit.

### 7.3 Reporting interface

```
POST /stats/report          # node → reg server, batch windows
Authorization: Bearer <node token>
{
  "node_id": "pd-7f3a1c2e",
  "windows": [
    {
      "start": "2026-09-19T12:00:00Z", "end": "2026-09-19T12:05:00Z",
      "peer_id": "pd-9c11ab20",
      "direction": "up", "channel": "direct",
      "bytes": 104857600, "sessions": 3,
      "countersign": "base64(Ed25519 sig by peer_id over (window, direction, bytes))"
    }
  ]
}
```

```
GET /stats/me               # own cumulative up/down, decomposed by peer
GET /stats/nodes/{peer_id}  # node public traffic (only public nodes)
POST /stats/challenge       # server initiates sample challenge (see 7.2 #3)
```

### 7.4 Open decisions (need to be decided)

1. **Do we need OAuth**: this design **does not introduce it for now**. The centralized
   server itself is the only account source; OAuth's value is third-party federated
   login (GitHub/WeChat etc.), which adds no incremental value for "single public
   server + self-hosted accounts"; if really needed, use **Authorization Code + PKCE**,
   plugged in as "one login method", not taking on scope semantics. At this stage HTTP
   only uses `Bearer <JWT>`.
2. **JWT algorithm**: recommend **EdDSA/RS256** (asymmetric) rather than HS256 —— nodes
   only need to embed a public key to verify signatures; regserver can rotate keys
   (`kid` header + JWKS endpoint); HS256 requires distributing the symmetric key to
   every node, and leak means total compromise. Validity access 15min / refresh 7d.
3. **Whether billing ties to incentives**: if future traffic rewards are done, 7.1's
   "only aggregate reporting" would need to relax to "aggregate + content hash prefix",
   introducing content-side privacy issues; review separately at that time.
4. The relay traffic **metering starting point** is decided by the relay (it sees
   encrypted packet sizes), which differs systematically from what both ends see
   (plaintext sizes) by a fixed overhead (frame headers/chunking); tolerance thresholds
   must allow for this systematic deviation.

---

## 8. Registered users' encrypted channels and HTTP auth (design proposal, 2026-09-19)

**Original requirement**: "For registered-user transfers, p2p uses a simple encrypted
channel, http also carries auth".

- **HTTP**: node → node, node → regserver all use `Authorization: Bearer <token>`.
  regserver is the **only, must be publicly reachable, HTTP(S) only** central service
  (no P2P entry); node to regserver must use TLS (HTTPS), otherwise JWT is exposed on
  the wire.
- **P2P**: WebRTC DataChannel itself is DTLS-encrypted (SCTP over DTLS), already has
  link-level encryption; this design adds another layer of **application-layer
  envelope** on top. The purpose is not "more encryption" but:
  1. **Bind account identity**: during handshake both sides mutually sign with the
     node's Ed25519 key (nonce challenge), making the `peer_id ↔ username` binding
     real; `restricted` collections' `requester` then has a basis (currently P2P sync
     passes an empty requester, see REFACTOR §3.16 known limitation).
  2. **No leak on relay scenarios**: when `channel=relay` data passes through the relay
     node, the application-layer envelope guarantees the relay at most sees ciphertext
     and metering metadata, cannot casually take the content.
- **Algorithm**: X25519 ECDH derives session key → ChaCha20-Poly1305; per-session
  random nonce + periodic rekey. No extra flairs beyond forward secrecy (requirement is
  "simple encrypted channel").
- **Anonymous nodes**: encrypted envelope not mandatory (compatibility), but
  **restricted collections require authentication** (anonymous identity cannot get
  restricted/private content) —— this is the prerequisite for the three-tier permission
  model to work.

---

## 9. Relay location: in node, not in server (design proposal, 2026-09-19)

**Original requirement**: "Having an account can drag data on relay nodes" "Relay
nodes can also count relayed traffic" "relay is integrated in node, not in this
server".

- relay is a **node-bundled capability** (registers `capabilities.relay = true` into
  section 6 directory), not part of the central server —— the server is only
  responsible for directory, auth, statistics and verification, does not forward
  files, so server bandwidth/compliance pressure decouples from network scale.
- "Having an account can drag data on relay nodes" = using account identity to pull
  own (or authorized) collections through the relay node; relay only forwards and
  doesn't persist (or does limited caching per config; cache hits still need billing
  and marking `cache_hit=true` to avoid treating cache as real end-to-end traffic).
- Relay traffic is counted per 7.1's `channel=relay` definition and reported as an
  independent observer per 7.2 #2; the relay's own operator account also appears in
  the node directory (who is providing relay is queryable).

---

## 10. Integration points with existing code

| Requirement | Current state | Gap |
|---|---|---|
| User register/login/JWT | regserver already has `/auth/register`, `/auth/login`, `/auth/whoami` | JWT algorithm TBD (see 7.4); OAuth not done (recommend not to) |
| Node-side auth middleware | `AuthOptional`/`AuthRequired`, token forwarded to regserver for validation (section 3) | Remote validation per request → should add local cache (TTL = token remaining validity) |
| Account ↔ node directory | Only relay anonymous registration (section 4) | **All of section 6 not done**; collection `Owner` currently takes `nodestate.GetOperator()`, source is this layer |
| Traffic statistics | None | **All of section 7 not done** (including countersign, challenge, tolerance) |
| Restricted collection cross-node | P2P sync doesn't carry requester → only this node readable (REFACTOR §3.16) | Need section 8's identity binding to bring requester into sync requests |
| relay | Port forwarding v2 implemented (REFACTOR §3.9) + relay register/heartbeat/list (section 4) | Relay traffic statistics and node directory visibility |

**Recommended implementation order**: 6 (directory, minimum dependencies, immediately
supports owner semantics) → 8 (identity binding, unlocks restricted collection
cross-node) → 7 (statistics, depends on 6's node identity and countersigning key) →
8/9 relay statistics.
