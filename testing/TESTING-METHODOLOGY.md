# Peerdrive 测试方法详解

## 测试环境

| 组件 | 地址 | 用途 |
|------|------|------|
| Node A (relay) | 97.64.30.221:3000 | 公网中继，IPFS+BT |
| Node B | 97.64.30.221:3001 | 客户端节点 |
| Reg Server | 97.64.30.221:4000 | JWT 认证 |
| IPFS test peer | 97.64.30.221:9001 | 模拟 IPFS 节点 |
| BT test peer | 97.64.30.221:6883 | 模拟 BT DHT 节点 |

---

## Phase 1: 基础健康检查

### 测试 1.1 — Ping 端点
**目的**: 确认服务进程在运行，HTTP 层可达  
**测试目标**: 200 响应 + `pong`

```bash
curl http://97.64.30.221:3000/ping
# → pong
```

### 测试 1.2 — P2P 状态
**目的**: 确认 libp2p host 已创建，DHT 已引导  
**测试目标**: `enabled: true`, `peer_id` 非空, `addrs` 非空

```bash
curl http://97.64.30.221:3000/p2p/status
# → {"enabled":true, "peer_id":"12D3KooW...", "relay_mode":"server", ...}
```

---

## Phase 2: IPFS/libp2p 连接

### 测试 2.1 — Node B 启动并连接
**目的**: 验证 bootstrap 机制能否让新节点自动发现并连接 relay  
**测试目标**: Node B 启动后 `connected_count >= 1`, Node A 的 peers 列表包含 B

```bash
# 启动 Node B，bootstrap 指向 relay
PORT=3001 PEERDRIVE_P2P_ENABLE=true \
  PEERDRIVE_BOOTSTRAP_PEER="/ip4/97.64.30.221/tcp/37537/p2p/<RELAY_ID>" \
  /root/pd-server &

sleep 5
curl http://127.0.0.1:3001/p2p/peers   # → ["<RELAY_ID>"]
curl http://127.0.0.1:3000/p2p/peers   # → ["<NODE_B_ID>"]
```

### 测试 2.2 — Ping 延迟
**目的**: 验证 libp2p ping 协议通信  
**测试目标**: RTT < 10ms (同机), 返回 JSON 含 `rtt`

```bash
B_ID=$(curl -s http://127.0.0.1:3001/p2p/node | jq -r .peer_id)
curl http://127.0.0.1:3000/p2p/ping/$B_ID
# → {"peer":"12D3...","rtt":"1.649995ms"}
```

### 测试 2.3 — 手动连接
**目的**: 验证手动 multiaddr 连接  
**测试目标**: 返回 `{"status":"connected"}`

```bash
curl -X POST http://127.0.0.1:3000/p2p/connect \
  -H 'Content-Type: application/json' \
  -d '{"addr":"/ip4/97.64.30.221/tcp/44729/p2p/<B_ID>"}'
# → {"status":"connected"}
```

---

## Phase 3: 文件操作

### 测试 3.1 — HTTP 上传
**目的**: 验证 multipart 上传 + SHA256 计算 + 存储
**测试目标**: 返回 `hash` (64 位 hex), `size` 正确

```bash
echo "test content" > /tmp/test.txt
curl -X POST http://97.64.30.221:3000/files/upload \
  -F "file=@/tmp/test.txt"
# → {"hash":"eafb6f7...","size":13,"filename":"test.txt"}
```

### 测试 3.2 — 本地文件注册（新文件）
**目的**: 验证从磁盘路径注册文件到 Peerdrive
**测试目标**: 返回 hash, 之后可 SHA256 下载

```bash
curl -X POST http://97.64.30.221:3000/files/register_local \
  -H 'Content-Type: application/json' \
  -d '{"path":"/etc/hostname"}'
# → {"hash":"abc123...","filename":"hostname"}
```

### 测试 3.3 — 重复注册（DB 中已有）
**目的**: 验证对已在 DB 中的文件再次注册不会创建重复记录
**测试目标**: 返回相同 hash, file_meta 表只有一条记录

```bash
# 第一次注册 → hash1
HASH1=$(curl -s -X POST http://97.64.30.221:3000/files/register_local \
  -H 'Content-Type: application/json' \
  -d '{"path":"/etc/hostname"}' | jq -r .hash)

# 第二次注册同文件 → 应返回相同 hash
HASH2=$(curl -s -X POST http://97.64.30.221:3000/files/register_local \
  -H 'Content-Type: application/json' \
  -d '{"path":"/etc/hostname"}' | jq -r .hash)

test "$HASH1" = "$HASH2"  # 必须相同
```

### 测试 3.4 — 本地文件注册（带自定义文件名）
**目的**: 验证注册时可以指定不同于磁盘名的文件名
**测试目标**: 返回的 filename 等于自定义名称

```bash
curl -X POST http://97.64.30.221:3000/files/register_local \
  -H 'Content-Type: application/json' \
  -d '{"path":"/etc/hostname","filename":"my-host.txt"}'
# → {"hash":"...","filename":"my-host.txt"}
```

### 测试 3.5 — 文件夹递归注册
**目的**: 验证递归遍历目录注册所有文件
**测试目标**: 返回 registered 数组, count 等于目录内文件数

```bash
curl -X POST http://97.64.30.221:3000/files/register_folder \
  -H 'Content-Type: application/json' \
  -d '{"folder_path":"/etc/ssl"}'
# → {"registered":[{path,hash,filename},...],"count":N}
```

### 测试 3.6 — 文件夹部分重复注册
**目的**: 验证目录中部分文件已注册时，仅注册新文件
**测试目标**: 新注册数 + 已存在数 = 总文件数

```bash
# 先注册单个文件
curl -X POST http://97.64.30.221:3000/files/register_local \
  -d '{"path":"/etc/ssl/certs/ca-certificates.crt"}'

# 再注册整个目录 → 应跳过已注册的文件
curl -X POST http://97.64.30.221:3000/files/register_folder \
  -d '{"folder_path":"/etc/ssl"}'
# → count 应 < 目录总文件数（跳过了已注册的）
```

### 测试 3.7 — URL 文件注册
**目的**: 验证通过 HTTP URL 注册远程文件
**测试目标**: 返回 hash, 之后可通过 SHA256 下载

```bash
curl -X POST http://97.64.30.221:3000/files/register_url \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com/robots.txt","filename":"robots.txt"}'
# provider_type = "http"
# → {"hash":"...","filename":"robots.txt","provider_type":"http"}
```

### 测试 3.8 — URL 注册的文件下载
**目的**: 验证 URL 注册的文件能通过 SHA256 下载（从远端拉取）
**测试目标**: HTTP 200, 返回正确内容

```bash
# 注册 URL 文件
RESULT=$(curl -s -X POST http://97.64.30.221:3000/files/register_url \
  -d '{"url":"https://example.com/robots.txt"}')
HASH=$(echo "$RESULT" | jq -r .hash)

# SHA256 下载 → 应通过 http provider 自动拉取
curl http://97.64.30.221:3000/sha256sum/$HASH | sha256sum
```

### 测试 3.9 — 已注册文件列表
**目的**: 验证列出所有已注册文件
**测试目标**: 返回数组, 包含 hash/filename/size/mime_type/provider_type

```bash
curl http://97.64.30.221:3000/files?sort=time
# → [{hash,filename,size,mime_type,provider_type,provider_path,created_at},...]
```

### 测试 3.10 — 文件验证
**目的**: 验证通过 hash 检查文件是否存在、一致
**测试目标**: exists=true, consistent=true

```bash
HASH=$(curl -s http://97.64.30.221:3000/files?sort=time | jq -r '.[0].hash')
curl http://97.64.30.221:3000/files/verify/$HASH
# → {"hash":"...","exists":true,"consistent":true,"filename":"...","size":N}
```

### 测试 3.11 — 不存在的文件验证
**目的**: 验证对不存在的 hash 返回正确状态
**测试目标**: exists=false

```bash
curl http://97.64.30.221:3000/files/verify/0000000000000000000000000000000000000000000000000000000000000000
# → {"exists":false} 或 404
```

### 测试 3.12 — 文件删除
**目的**: 验证删除文件元数据（不删物理文件）
**测试目标**: 删除后 SHA256 下载返回 404, 但物理文件还在

```bash
# 先注册一个文件
HASH=$(echo "del-test" > /tmp/del.txt && \
  curl -s -X POST http://97.64.30.221:3000/files/upload -F "file=@/tmp/del.txt" | jq -r .hash)

# 删除
curl -X DELETE http://97.64.30.221:3000/files/$HASH
# → 200

# SHA256 下载 → 404
curl -o /dev/null -w "%{http_code}" http://97.64.30.221:3000/sha256sum/$HASH
# → 404
```

### 测试 3.13 — 文件浏览器
**目的**: 验证浏览服务器文件系统（不限于已注册文件）
**测试目标**: 返回目录条目数组, 包含 is_dir/name/path/size

```bash
curl "http://97.64.30.221:3000/files/browse?path=/etc"
# → [{name,path,is_dir,size,mod_time},...]
```

### 测试 3.14 — 文件浏览器根目录
**目的**: 验证默认路径（Linux /, Windows C:\）
**测试目标**: 返回根目录内容

```bash
curl "http://97.64.30.221:3000/files/browse"
# → 默认路径的目录列表
```

### 测试 3.15 — 空文件夹注册
**目的**: 验证对空目录的注册行为
**测试目标**: 返回 registered=[], count=0, 不报错

```bash
mkdir -p /tmp/empty-dir
curl -X POST http://97.64.30.221:3000/files/register_folder \
  -d '{"folder_path":"/tmp/empty-dir"}'
# → {"registered":[],"count":0}
```

---

## Phase 4: BT DHT 协议

### 测试 4.1 — BT DHT 状态
**目的**: 验证 BT Mainline DHT 节点已启动并连上全局网络  
**测试目标**: `enabled: true`, `num_nodes > 0`（证明连上了全球 BT 网络）

```bash
curl http://97.64.30.221:3000/bt/status
# → {"enabled":true,"listen_addr":"0.0.0.0:6881","num_nodes":127}
```

### 测试 4.2 — BT Announce
**目的**: 验证能否向全球 BT DHT 宣告文件  
**测试目标**: 返回 `"status":"announced on BT DHT"`

```bash
curl -X POST http://97.64.30.221:3000/bt/announce \
  -H 'Content-Type: application/json' \
  -d '{"hash":"eafb6f737b516be4c8899299b4732f3d54ea5d119ce0571ce6f5cd2d55735275"}'
# → {"status":"announced on BT DHT"}
```

### 测试 4.3 — BT Find (跨节点)
**目的**: 验证跨节点 BT DHT 查找——Node A 宣告，Node B 查找  
**测试目标**: Node B 查询 BT DHT 能找到 Node A 宣告的文件（count > 0）

```bash
# Node A 宣告
curl -X POST http://127.0.0.1:3000/bt/announce \
  -d '{"hash":"FILE_HASH"}'

# Node B 查找（wait for DHT propagation）
sleep 3
curl -X POST http://127.0.0.1:3001/bt/find \
  -d '{"hash":"FILE_HASH"}'
# → {"count":1,"peers":["31.200.249.231:31934"]}
```

---

## Phase 5: P2P 文件交换

### 测试 5.1 — IPFS Exchange
**目的**: 验证 libp2p exchange 协议——B 从 A 下载文件  
**测试目标**: responses=1, 返回文件大小正确

```bash
A_ID=$(curl -s http://127.0.0.1:3000/p2p/node | jq -r .peer_id)
curl -X POST http://127.0.0.1:3001/p2p/request-file \
  -d "{\"hash\":\"FILE_HASH\",\"peer_ids\":[\"$A_ID\"]}"
# → {"responses":1,"details":[{"hash":"...","size":13}]}
```

### 测试 5.2 — 合集同步
**目的**: 验证合集跨节点同步——A 创建合集，B 拉取  
**测试目标**: synced count = 1

```bash
# A 创建合集
curl -X POST http://127.0.0.1:3000/anon/collections \
  -d '{"entries":[{"path":"test.txt","hash":"FILE_HASH"}],"friendly_name":"Sync Test"}'
# → {"hash":"COLLECTION_HASH"}

# B 从 A 同步
curl -X POST http://127.0.0.1:3001/p2p/sync \
  -d "{\"peer_id\":\"$A_ID\",\"hash\":\"COLLECTION_HASH\",\"target_dir\":\"/tmp/synced\"}"
# → {"synced":["FILE_HASH"],"count":1}
```

---

## Phase 6: Dual-Stack

### 测试 6.1 — Dual Announce
**目的**: 验证同时在 IPFS 和 BT 两个 DHT 宣告  
**测试目标**: `"announced on both networks"`

```bash
curl -X POST http://97.64.30.221:3000/p2p/dual/announce \
  -d '{"hash":"FILE_HASH"}'
# → {"status":"announced on both networks"}
```

### 测试 6.2 — Dual Find
**目的**: 验证同时在两个 DHT 查找提供者  
**测试目标**: 返回 `ipfs_peers` 和 `bt_peers` 两个字段

```bash
curl -X POST http://97.64.30.221:3000/p2p/dual/find \
  -d '{"hash":"FILE_HASH"}'
# → {"hash":"...","ipfs_peers":null,"bt_peers":["31.200.249.231:31934"]}
```

---

## Phase 7: 多源并行下载

### 测试 7.1 — 三源同时下载
**目的**: 验证同一文件可从 relay、IPFS test peer、BT test peer 三个来源下载  
**测试目标**: 三次下载返回内容完全一致，SHA256 匹配

```python
# 从 relay 下载
data1 = requests.get(f"http://97.64.30.221:3000/sha256sum/{hash}").content
# 从 IPFS test peer 下载
data2 = requests.get(f"http://97.64.30.221:9001/files/{hash}").content
# 从 BT peer 查询后下载
peers = bt_find(hash)  # 找到 peer 地址
data3 = download_from_peer(peers[0], hash).content

assert sha256(data1) == hash
assert sha256(data2) == hash
assert sha256(data3) == hash
assert data1 == data2 == data3  # 三个来源内容完全一致
```

---

## Phase 8: 前端

### 测试 8.1 — Playwright Smoke (16 tests)
**目的**: 验证所有页面加载、关键 UI 元素渲染  
**测试目标**: 16/16 pass

```javascript
// 测试页面：Plaza, AnonCreator, FileManager, AnonExplorer, Settings
// 检查项：搜索框、4-tab、复选框、SHA256 输入、LLM 配置
```

### 测试 8.2 — Playwright Functional (18 tests)
**目的**: 验证实际用户交互流程  
**测试目标**: 18/18 pass

```javascript
// Plaza: paste SHA256 → 导航到合集页
// AnonCreator: 4-tab 可见、时间线日期分组、注册目录面包屑
// FileManager: 复选框切换 → 选择计数更新
// Settings: LLM 端点输入、模型下拉
// Navbar: Ctrl+K 搜索面板
```

---

## Phase 9: Go 单元测试

### 测试 9.1 — 全量运行
**目的**: 验证所有 Go 包无回归  
**测试目标**: 7 packages 全部 `ok`

```bash
go test ./...
# ok  peerdrive/internal/config    0.021s
# ok  peerdrive/internal/controller 0.087s
# ok  peerdrive/internal/model      0.007s
# ok  peerdrive/internal/provider   0.008s
# ok  peerdrive/internal/repository 0.020s
# ok  peerdrive/internal/service    0.314s
```

---

## 未验证项

| 项目 | 原因 |
|------|------|
| Docker 5 节点组网 | 代码就绪，未执行 `docker compose up` |
| WebRTC 实际传输 | signaling + 前端组件就绪，未两个浏览器测试 |
| 断点续传 | ResumeManager 代码就绪，未中断下载场景验证 |
| BT torrent 下载 | piece exchange 代码就绪，未用真实 .torrent 文件测试 |
| 混沌网络 | chaos-net.sh 就绪，未在 chaos 条件下重跑测试 |
