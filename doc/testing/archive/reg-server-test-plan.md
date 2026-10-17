# Registration Server Test Plan

> Covers REQ 6.6-6.9 (group queries, service policy, Node operators, storage tracking)

## Test Environment

| Item | Configuration |
|------|------|
| Server | `localhost:4000` |
| Database | SQLite (temporary file, deleted after tests) |
| JWT | `JWT_SECRET=test-secret-key` |
| Test Script | `go/test/reg-server-user-mgmt.sh` |

## Test Case Matrix (19 cases)

### 1. Service Connectivity & Authentication

| ID | Test Item | Method | Expected | Status |
|----|--------|------|------|------|
| A-01 | Ping | `GET /ping` | 200, `{"service":"peerdrive-registration","status":"ok"}` | ✅ |
| A-02 | Register new user | `POST /auth/register` | 201, returns JWT token | ✅ |
| A-03 | Duplicate registration | `POST /auth/register` | 409, "username already exists" | ✅ |
| A-04 | whoami | `GET /auth/whoami` (Bearer) | 200, username + role | ✅ |

### 2. Group Management (REQ 6.6)

| ID | Test Item | Method | Expected | Status |
|----|--------|------|------|------|
| G-01 | Join group | `POST /auth/group/:user` | 200, group auto-created | ✅ |
| G-02 | Query user groups | `GET /auth/group/:user` | 200, returns group list | ✅ |
| G-03 | List all groups | `GET /auth/groups` | 200, includes member_count | ✅ |
| G-04 | Group member list | `GET /auth/groups/:name/members` | 200, array of usernames | ✅ |
| G-05 | Remove member | `DELETE /auth/group/:user/:group` | 200 | ✅ |
| G-06 | Member count decrements after removal | `GET /auth/groups/:name/members` | Total decreases | ✅ |

### 3. Service Policy (REQ 6.7)

| ID | Test Item | Method | Expected | Status |
|----|--------|------|------|------|
| P-01 | Default policy | `GET /auth/service-policy/:user` | `allow_relay:true, allow_p2p:true` | ✅ |
| P-02 | Set policy | `POST /auth/service-policy/:user` | 200, returns updated policy | ✅ |
| P-03 | Policy persistence | `GET /auth/service-policy/:user` | Matches set values | ✅ |
| P-04 | Unauthenticated rejection | `GET /auth/service-policy/:user` (no Bearer) | 401 | ✅ |

### 4. Node Operators (REQ 6.8)

| ID | Test Item | Method | Expected | Status |
|----|--------|------|------|------|
| O-01 | Register relay | `POST /p2p/relay/register` | 200 | ✅ |
| O-02 | Bind operator | `POST /p2p/relay/:peer_id/operator` | 200 | ✅ |
| O-03 | Query relay operator | `GET /p2p/relay/:peer_id/operator` | 200, includes operator_username | ✅ |
| O-04 | Query user relays | `GET /auth/relays/:username` | 200, relay list | ✅ |

### 5. Storage Tracking (REQ 6.9)

| ID | Test Item | Method | Expected | Status |
|----|--------|------|------|------|
| S-01 | Default storage | `GET /auth/storage/:user` | `used_bytes:0, limit_bytes:0` | ✅ |
| S-02 | Update usage | `POST /auth/storage/:user` | 200, returns updated value | ✅ |
| S-03 | Storage query consistency | `GET /auth/storage/:user` | Exactly matches set values | ✅ |

### 6. Aggregate Statistics

| ID | Test Item | Method | Expected | Status |
|----|--------|------|------|------|
| ST-01 | Statistics endpoint | `GET /stats` | users>=3, active_relays>=1 | ✅ |

## Run Tests

```bash
# 1. Start the registration server
cd /mnt/d/WorkPlace/peerdrive/registration-server
DB_PATH=/tmp/regsvr_test.db PORT=4000 JWT_SECRET=test-secret-key go run ./cmd/server &

# 2. Run tests
cd /mnt/d/WorkPlace/peerdrive/go/test
bash reg-server-user-mgmt.sh

# 3. Expected output
# === Result: PASS=19 FAIL=0 ===
```

## Manual Tests

```bash
# Bypass Privoxy proxy
unset http_proxy https_proxy HTTP_PROXY HTTPS_PROXY
export no_proxy='*'
REGSRV="http://localhost:4000"

# Register
TOKEN=$(curl -s -X POST "$REGSRV/auth/register" \
  -H 'Content-Type: application/json' \
  -d '{"username":"test","password":"123456"}' | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")
echo "Token: ${TOKEN:0:30}..."

AUTH="-H 'Authorization: Bearer $TOKEN'"

# Join group
eval "curl -s -X POST '$REGSRV/auth/group/test' -H 'Content-Type: application/json' $AUTH -d '{\"group_name\":\"dev\"}'"

# Query groups
eval "curl -s '$REGSRV/auth/group/test' $AUTH"

# View service policy
eval "curl -s '$REGSRV/auth/service-policy/test' $AUTH"

# Query storage
eval "curl -s '$REGSRV/auth/storage/test' $AUTH"

# Register relay + bind operator
curl -s -X POST "$REGSRV/p2p/relay/register" \
  -H 'Content-Type: application/json' \
  -d '{"peer_id":"12D3KooTEST","addrs":["/ip4/10.0.0.1/tcp/4001"],"storage_mb":1024,"version":"v3.0"}'

eval "curl -s -X POST '$REGSRV/p2p/relay/12D3KooTEST/operator' -H 'Content-Type: application/json' $AUTH -d '{\"username\":\"test\"}'"

# Query relay operator
eval "curl -s '$REGSRV/p2p/relay/12D3KooTEST/operator' $AUTH"
```

## DB Table Schema

### users
```sql
CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'user',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### user_profiles (6.7)
```sql
CREATE TABLE user_profiles (
    username TEXT PRIMARY KEY,
    allow_relay INTEGER NOT NULL DEFAULT 1,
    allow_p2p INTEGER NOT NULL DEFAULT 1,
    notes TEXT DEFAULT '',
    FOREIGN KEY (username) REFERENCES users(username)
);
```

### user_storage (6.9)
```sql
CREATE TABLE user_storage (
    username TEXT PRIMARY KEY,
    used_bytes INTEGER NOT NULL DEFAULT 0,
    limit_bytes INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (username) REFERENCES users(username)
);
```

### relay_nodes (6.8)
```sql
CREATE TABLE relay_nodes (
    peer_id TEXT PRIMARY KEY,
    addrs TEXT NOT NULL,
    storage_mb INTEGER DEFAULT 0,
    load_pct REAL DEFAULT 0,
    version TEXT DEFAULT '',
    operator_username TEXT DEFAULT '',     -- NEW: linked to user
    registered_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_heartbeat DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### groups + user_groups (6.6)
```sql
CREATE TABLE groups (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    description TEXT DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE user_groups (
    user_id INTEGER NOT NULL,
    group_id INTEGER NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, group_id),
    FOREIGN KEY (user_id) REFERENCES users(id),
    FOREIGN KEY (group_id) REFERENCES groups(id)
);
```
