# Peerdrive 测试说明

## 环境

| 组件 | 技术栈 | 端口 |
|------|--------|------|
| **后端** | Go + Gin + libp2p + SQLite3 | `:3000` |
| **前端** | React 19 + Vite + TypeScript | `:5173` (dev) |
| **API 代理** | Cloudflare Tunnel → `wsl-3000.moonchan.xyz` (可选) |

---

## 1. 启动后端

```bash
cd go/

# 安装依赖（首次）
go mod tidy

# 启动服务
go run ./cmd/server/main.go

# 或指定端口与存储目录
PORT=3000 PEERDRIVE_STORAGE=./storage go run ./cmd/server/main.go
```

启动后应看到：
```
libp2p 节点已启动: PeerID=12D3KooW..., 监听地址=[/ip4/...]
```

---

## 2. 快速测试（自动化脚本）

```bash
cd go/
bash test.sh
```

脚本覆盖：
- `/ping` 健康检查
- `/p2p/node` P2P 节点信息
- 文件上传 → 校验 → 删除
- 注册本地文件 / 文件夹
- 合集创建 → 添加条目 → Commit → 版本日志 → Rollback
- Fork 合集 → Merge 合集 → Pull
- 边界情况（无效 hash、重复合集、不存在的任务）
- 清理临时文件

---

## 3. 手动测试（curl 逐项）

### 3.1 基础

```bash
# 健康检查
curl -x "" http://localhost:3000/ping

# P2P 节点信息
curl -x "" http://localhost:3000/p2p/node

# P2P 对等点列表
curl -x "" http://localhost:3000/p2p/peers
```

### 3.2 文件管理

```bash
# 上传文件
echo "hello world" > /tmp/test.txt
curl -x "" -F "file=@/tmp/test.txt" http://localhost:3000/files/upload
# → {"hash":"...sha256...","filename":"test.txt"}

# 验证文件（用上面的 hash）
curl -x "" http://localhost:3000/files/verify/<HASH>
# → {"hash":"...","filename":"test.txt","provider":"local","path":".."}

# 通过 SHA256 下载
curl -x "" -o /tmp/downloaded http://localhost:3000/sha256sum/<HASH>

# 注册本地文件
curl -x "" -H "Content-Type: application/json" \
  -d '{"path":"test.txt","filename":"test.txt"}' \
  http://localhost:3000/files/register_local

# 注册文件夹
curl -x "" -H "Content-Type: application/json" \
  -d '{"folder_path":"testdata"}' \
  http://localhost:3000/files/register_folder

# 删除文件
curl -x "" -X DELETE http://localhost:3000/files/<HASH>
```

### 3.3 合集管理

```bash
# 创建合集
curl -x "" -H "Content-Type: application/json" \
  -d '{"username":"alice","collection_name":"music"}' \
  -X POST http://localhost:3000/collections

# 列出用户的合集
curl -x "" http://localhost:3000/collections/alice

# 获取合集详情（含条目）
curl -x "" http://localhost:3000/collections/alice/music

# 添加条目（path=合集内路径, hash=文件SHA256）
curl -x "" -H "Content-Type: application/json" \
  -d '{"path":"songs/hello.mp3","hash":"<HASH>"}' \
  -X POST http://localhost:3000/collections/alice/music/entries

# 从合集下载文件
curl -x "" -o /tmp/song http://localhost:3000/alice/music/songs/hello.mp3

# 删除条目
curl -x "" -X DELETE "http://localhost:3000/collections/alice/music/entries/songs%2Fhello.mp3"
```

### 3.4 版本管理

```bash
# Commit 当前状态
curl -x "" -H "Content-Type: application/json" \
  -d '{"commit_message":"first version"}' \
  -X POST http://localhost:3000/collections/alice/music/commit

# 查看版本日志
curl -x "" http://localhost:3000/collections/alice/music/log

# Rollback（用日志中的 version_id）
curl -x "" -X POST http://localhost:3000/collections/alice/music/rollback/<VERSION_ID>
```

### 3.5 Fork / Merge / Pull

```bash
# Fork（克隆别的合集到自己的名下）
curl -x "" -H "Content-Type: application/json" \
  -d '{"username":"bob","collection_name":"music-fork","source_username":"alice","source_coll_name":"music"}' \
  -X POST http://localhost:3000/actions/fork

# Merge（从源合集合并到本地）
curl -x "" -H "Content-Type: application/json" \
  -d '{"username":"alice","collection_name":"music","source_username":"bob","source_coll_name":"music-fork","strategy":"theirs"}' \
  -X POST http://localhost:3000/actions/merge
# strategy: "ours" = 本地优先, "theirs" = 源优先, "manual" = 返回冲突列表

# Pull（简单占位，v2 实现上游同步）
curl -x "" -H "Content-Type: application/json" \
  -d '{"username":"alice","collection_name":"music"}' \
  -X POST http://localhost:3000/actions/pull
```

### 3.6 任务系统

```bash
# 查看任务
curl -x "" http://localhost:3000/tasks

# 查询具体任务
curl -x "" http://localhost:3000/tasks/1
```

---

## 4. 前端测试

### 4.1 启动

```bash
cd react/
npm run dev
```

浏览器打开 `http://localhost:5173`

### 4.2 测试步骤

| 步骤 | 操作 | 预期 |
|------|------|------|
| 1 | 在输入框输入用户名（如 `alice`），点击 **Set** | 顶部显示用户名 |
| 2 | 在 "New collection name" 输入 `my-coll`，点击 **Create** | 下方出现合集卡片 |
| 3 | 点击合集卡片进入详情页 | 看到空条目表 |
| 4 | 在 Path 输入 `test.txt`，选择文件上传 | 文件上传成功，hash 填入 |
| 5 | 点击 **Add** | 条目表出现一行 |
| 6 | 输入 Commit message，点击 **Commit** | 版本历史出现 v1 |
| 7 | 再 Commit 一次 | 出现 v2 |
| 8 | 点击 v1 的 **Rollback** | 条目表恢复 v1 状态 |
| 9 | 回到首页，点 **Fork** 链接 | 进入 Fork 页面 |
| 10 | 填写本地方名 `my-coll-fork`，源用户 `alice`，源合集 `my-coll`，点 Fork | 跳转到新合集页 |
| 11 | 点 **P2P** 导航链接 | 可查看节点信息和对等点 |

---

## 5. 清理

```bash
# 删除数据库（重置环境）
rm go/peerdrive.db

# 删除上传文件
rm -rf go/storage/
```

---

## 6. API 端点一览

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/ping` | 健康检查 |
| GET | `/sha256sum/:sha256` | 按 hash 下载文件 |
| GET | `/p2p/node` | P2P 节点信息 |
| GET | `/p2p/peers` | 连接的对等点 |
| GET | `/p2p/ping/:peer_id` | Ping 对等点 |
| POST | `/files/upload` | 上传文件 |
| POST | `/files/register_local` | 注册本地文件 |
| POST | `/files/register_folder` | 注册文件夹 |
| GET | `/files/verify/:hash` | 验证文件 |
| DELETE | `/files/:hash` | 删除文件 |
| POST | `/files/diff` | 版本差异比较 |
| POST | `/collections` | 创建合集 |
| GET | `/collections/:username` | 列出合集 |
| GET | `/collections/:username/:coll` | 获取合集详情 |
| POST | `/collections/:username/:coll/entries` | 添加条目 |
| DELETE | `/collections/:username/:coll/entries/:path` | 删除条目 |
| POST | `/collections/:username/:coll/commit` | Commit 版本 |
| GET | `/collections/:username/:coll/log` | 版本日志 |
| POST | `/collections/:username/:coll/rollback/:vid` | Rollback |
| GET | `/:username/:coll/*filepath` | 从合集下载文件 |
| POST | `/actions/merge` | 合并合集 |
| POST | `/actions/fork` | Fork 合集 |
| POST | `/actions/pull` | Pull 更新 |
| GET | `/tasks` | 任务列表 |
| GET | `/tasks/:id` | 任务状态 |
