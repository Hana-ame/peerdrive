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
