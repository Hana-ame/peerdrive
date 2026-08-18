# Registration Server 测试方案

> 覆盖 REQ 6.6-6.9（群组查询、服务策略、Node 操作者、存储追踪）

## 测试环境

| 项目 | 配置 |
|------|------|
| 服务器 | `localhost:4000` |
| 数据库 | SQLite (临时文件，测试后删除) |
| JWT | `JWT_SECRET=test-secret-key` |
| 测试脚本 | `go/test/reg-server-user-mgmt.sh` |

## 测试用例矩阵 (19 cases)

### 1. 服务连通 & 认证

| ID | 测试项 | 方法 | 预期 | 状态 |
|----|--------|------|------|------|
| A-01 | Ping | `GET /ping` | 200, `{"service":"peerdrive-registration","status":"ok"}` | ✅ |
| A-02 | 注册新用户 | `POST /auth/register` | 201, 返回 JWT token | ✅ |
| A-03 | 重复注册 | `POST /auth/register` | 409, "username already exists" | ✅ |
| A-04 | whoami | `GET /auth/whoami` (Bearer) | 200, username + role | ✅ |

### 2. 群组管理 (REQ 6.6)

| ID | 测试项 | 方法 | 预期 | 状态 |
|----|--------|------|------|------|
| G-01 | 加入群组 | `POST /auth/group/:user` | 200, 群组自动创建 | ✅ |
| G-02 | 查询用户群组 | `GET /auth/group/:user` | 200, 返回群组列表 | ✅ |
| G-03 | 列出所有群组 | `GET /auth/groups` | 200, 含 member_count | ✅ |
| G-04 | 群组成员列表 | `GET /auth/groups/:name/members` | 200, 用户名数组 | ✅ |
| G-05 | 移除成员 | `DELETE /auth/group/:user/:group` | 200 | ✅ |
| G-06 | 移除后成员数递减 | `GET /auth/groups/:name/members` | total 减少 | ✅ |

### 3. 服务策略 (REQ 6.7)

| ID | 测试项 | 方法 | 预期 | 状态 |
|----|--------|------|------|------|
| P-01 | 默认策略 | `GET /auth/service-policy/:user` | `allow_relay:true, allow_p2p:true` | ✅ |
| P-02 | 设置策略 | `POST /auth/service-policy/:user` | 200, 返回更新后策略 | ✅ |
| P-03 | 策略持久化 | `GET /auth/service-policy/:user` | 与设置值一致 | ✅ |
| P-04 | 无认证拒绝 | `GET /auth/service-policy/:user` (无 Bearer) | 401 | ✅ |

### 4. Node 操作者 (REQ 6.8)

| ID | 测试项 | 方法 | 预期 | 状态 |
|----|--------|------|------|------|
| O-01 | 注册 relay | `POST /p2p/relay/register` | 200 | ✅ |
| O-02 | 绑定操作者 | `POST /p2p/relay/:peer_id/operator` | 200 | ✅ |
| O-03 | 查询 relay operator | `GET /p2p/relay/:peer_id/operator` | 200, 含 operator_username | ✅ |
| O-04 | 查询用户 relays | `GET /auth/relays/:username` | 200, relay 列表 | ✅ |

### 5. 存储追踪 (REQ 6.9)

| ID | 测试项 | 方法 | 预期 | 状态 |
|----|--------|------|------|------|
| S-01 | 默认存储 | `GET /auth/storage/:user` | `used_bytes:0, limit_bytes:0` | ✅ |
| S-02 | 更新使用量 | `POST /auth/storage/:user` | 200, 返回更新后值 | ✅ |
| S-03 | 存储查询一致 | `GET /auth/storage/:user` | 与设置值完全匹配 | ✅ |

### 6. 综合统计

| ID | 测试项 | 方法 | 预期 | 状态 |
|----|--------|------|------|------|
| ST-01 | 统计端点 | `GET /stats` | users>=3, active_relays>=1 | ✅ |

## 运行测试

```bash
# 1. 启动注册服务器
cd /mnt/d/WorkPlace/peerdrive/registration-server
DB_PATH=/tmp/regsvr_test.db PORT=4000 JWT_SECRET=test-secret-key go run ./cmd/server &

# 2. 运行测试
cd /mnt/d/WorkPlace/peerdrive/go/test
bash reg-server-user-mgmt.sh

# 3. 预期输出
# === 结果: PASS=19 FAIL=0 ===
```

## 手动测试

```bash
# 绕过 Privoxy 代理
unset http_proxy https_proxy HTTP_PROXY HTTPS_PROXY
export no_proxy='*'
REGSRV="http://localhost:4000"

# 注册
TOKEN=$(curl -s -X POST "$REGSRV/auth/register" \
  -H 'Content-Type: application/json' \
  -d '{"username":"test","password":"123456"}' | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")
echo "Token: ${TOKEN:0:30}..."

AUTH="-H 'Authorization: Bearer $TOKEN'"

# 加入群组
eval "curl -s -X POST '$REGSRV/auth/group/test' -H 'Content-Type: application/json' $AUTH -d '{\"group_name\":\"dev\"}'"

# 查询群组
eval "curl -s '$REGSRV/auth/group/test' $AUTH"

# 查看服务策略
eval "curl -s '$REGSRV/auth/service-policy/test' $AUTH"

# 查询存储
eval "curl -s '$REGSRV/auth/storage/test' $AUTH"

# 注册 relay + 绑定操作者
curl -s -X POST "$REGSRV/p2p/relay/register" \
  -H 'Content-Type: application/json' \
  -d '{"peer_id":"12D3KooTEST","addrs":["/ip4/10.0.0.1/tcp/4001"],"storage_mb":1024,"version":"v3.0"}'

eval "curl -s -X POST '$REGSRV/p2p/relay/12D3KooTEST/operator' -H 'Content-Type: application/json' $AUTH -d '{\"username\":\"test\"}'"

# 查询 relay operator
eval "curl -s '$REGSRV/p2p/relay/12D3KooTEST/operator' $AUTH"
```

## DB 表结构

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
    operator_username TEXT DEFAULT '',     -- NEW: 链接到用户
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
