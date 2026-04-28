# Peerdrive — P2P 文件分享系统

> **最后修改**: 2026-04-27 · **版本**: v3.0
> **上一版本**: v2.0 (2026-04-25) — 内容寻址存储、合集版本管理、Fork/Pull/Merge
> **本版新增**: P2P 连接管理+分片传输、前端 UI 重构（breadcrumb 导航/三视图模式）、CORS 白名单

基于内容寻址的 P2P 文件分享系统，支持 Git 风格的版本历史和跨节点同步。

## 功能特性

| 功能 | 说明 |
|------|------|
| 内容寻址存储 | 文件通过 SHA256 哈希标识，相同内容自动去重 |
| 合集与版本管理 | Git 风格的 Collection/Commit/Rollback/Log |
| 跨节点同步 | Fork 远程合集、Pull 上游更新、三路合并冲突检测 |
| 异步传输 | Fork/Pull 后台下载缺失文件 |
| Provider 模式 | 存储后端抽象（本地文件 / 远程 HTTP），可扩展 |
| 文件上传 | HTTP multipart 上传、本地路径注册、文件夹递归注册 |
| P2P 节点 | 基于 libp2p 的节点信息查询和 Ping 延迟测量 |
| 垃圾回收 | 自动清理未被引用的孤立文件 |
| 前端界面 | React 暗色主题 UI，合集浏览和版本操作 |
| 冲突处理 | 文件级冲突检测，自动重命名冲突副本 |

## 架构设计

```
Controller → Service → Provider / Repository
```

| 层 | 说明 |
|---|---|
| **Controller** | HTTP 请求处理、参数校验 |
| **Service** | 业务逻辑编排（注册、合并、传输、GC） |
| **Provider** | 数据源抽象（本地文件、远程 HTTP） |
| **Repository** | SQLite 数据库访问 |

### 核心概念

| 概念 | 说明 |
|---|---|
| **Collection** | 合集，类似 Git Repository |
| **CollectionEntry** | 合集当前条目，类似 Git Tree |
| **CollectionVersion** | 版本快照，类似 Git Commit |
| **TransferTask** | 异步传输任务（Fork/Pull） |
| **ContentProvider** | 存储后端抽象接口 |

## 目录结构

```
peerdrive/
├── go/
│   ├── cmd/server/main.go        # 程序入口
│   ├── internal/
│   │   ├── controller/            # HTTP 处理层
│   │   ├── service/               # 业务逻辑层
│   │   ├── provider/              # 内容提供者（local/http）
│   │   ├── repository/            # 数据库访问层
│   │   ├── model/                 # 数据结构
│   │   └── router/router.go       # 路由注册
│   ├── storage/                   # 本地存储目录
│   └── docs/                      # 详细文档
├── react/
│   └── src/
│       ├── App.tsx                # 主界面
│       ├── App.css                # 暗色主题样式
│       └── config.ts              # 配置
```

## 快速开始

### 启动后端

```bash
cd go
go run ./cmd/server/main.go
# 服务启动在 :3000
```

### 启动前端

```bash
cd react
npm run dev
# 开发服务器启动在 :5173
```

### 测试

```bash
cd go
go test ./...
```

## API 接口

### 系统接口

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/ping` | 健康检查 |

### 文件管理

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/upload` | 上传文件 |
| POST | `/files/register_local` | 注册本地文件 |
| POST | `/files/register_folder` | 注册文件夹 |
| POST | `/files/upload` | 上传并注册 |
| GET | `/sha256sum/:hash` | 通过 SHA256 下载文件 |

### 合集 API (v1)

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/collections/:user/:coll` | 获取合集元信息 |
| GET | `/api/v1/collections/:user/:coll/entries` | 列出合集条目 |
| POST | `/api/v1/collections/:user/:coll/entry` | 添加条目 |
| POST | `/api/v1/collections/:user/:coll/commit` | 提交新版本 |
| GET | `/api/v1/collections/:user/:coll/log` | 版本历史 |
| GET | `/api/v1/collections/:user/:coll/versions/:vid` | 查看特定版本 |
| POST | `/api/v1/collections/:user/:coll/rollback` | 回退版本 |
| POST | `/api/v1/collections/:user/:coll/pull` | 从上游拉取 |
| POST | `/api/v1/collections/:user/:coll/merge` | 合并来源 |

### 协作操作

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/actions/fork` | Fork 合集 |
| GET | `/api/v1/tasks/:task_id` | 查询任务进度 |

### P2P 节点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/p2p/node` | 获取本节点信息 |
| GET | `/p2p/peers` | 已连接节点列表 |
| GET | `/p2p/ping/:peer_id` | Ping 远程节点 |

## 数据库设计

### files

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | 自增主键 |
| hash | TEXT UNIQUE | SHA256 哈希 |
| provider_type | TEXT | 提供者类型（local/http） |
| path | TEXT | 文件路径或 URL |
| filename | TEXT | 原始文件名 |
| size | INTEGER | 文件大小 |

### collections

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | 自增主键 |
| username | TEXT | 所有者 |
| collection_name | TEXT | 合集名称 |
| current_version_id | TEXT | 当前版本 ID |
| upstream_addr/user/coll | TEXT | 上游节点信息 |
| is_public | INTEGER | 是否公开 |
| created_at | DATETIME | 创建时间 |

### collection_entries

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | 自增主键 |
| collection_id | INTEGER FK | 所属合集 |
| path | TEXT | 路径 |
| filename | TEXT | 文件名 |
| file_hash | TEXT | 文件哈希 |

### collection_versions

| 字段 | 类型 | 说明 |
|------|------|------|
| id | TEXT PK | 版本 ID (SHA256) |
| collection_id | INTEGER FK | 所属合集 |
| parent_version_id | TEXT | 父版本 |
| merge_source_* | TEXT | 合并来源信息 |
| snapshot_hash | TEXT | 快照哈希 |
| snapshot_data | TEXT | 快照 JSON |
| message | TEXT | 提交信息 |
| created_at | DATETIME | 创建时间 |

### transfer_tasks

| 字段 | 类型 | 说明 |
|------|------|------|
| id | TEXT PK | 任务 ID |
| type | TEXT | 类型 (fork/pull/commit) |
| status | TEXT | pending/running/completed/failed |
| collection_id | INTEGER FK | 关联合集 |
| source_addr | TEXT | 源地址 |
| message | TEXT | 消息 |
| created_at | DATETIME | 创建时间 |

## 核心服务

### FileService — 文件存储

- 接收上传流，计算 SHA256，去重存储
- 通过 Hash 获取本地路径

### RegisterService — 文件注册

- 读取本地文件计算 SHA256 并注册到数据库
- 支持递归注册文件夹

### CollectionService — 合集版本管理

- Commit：冻结当前工作区为版本快照
- Rollback：将历史版本恢复到工作区
- 基于快照 hash 检测是否有变更

### MergeService — 三路合并

| 场景 | 处理方式 |
|------|----------|
| 远端新增 | 自动合并 |
| 本地新增 | 保留 |
| 双方修改 | 冲突，保留双方副本 |
| 一方删除一方修改 | 冲突，保留修改方 |

### TransferService — 跨节点同步

- Fork：从远端克隆合集到本地
- Pull：从上游同步更新

### GCService — 垃圾回收

清理不再被任何合集引用的孤立文件。

### P2PService — 节点网络

- 基于 libp2p 的节点发现和通信
- Ping 协议测量节点延迟

## 前端界面

![Peerdrive UI](react/screenshot.png)

- **侧边栏**：节点管理、合集切换、Fork/Pull/GC
- **文件列表**：条目展示、状态图标、上传/下载
- **版本历史**：Commit 日志、合并来源展示
- **模态框**：Commit 提交、Fork 远端合集

## 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `CORS_MODE` | CORS 模式（all/localhost/空） | 空（白名单） |
| `VITE_API_BASE` | 前端 API 地址 | `http://localhost:3000` |

## 详细文档

参见 `go/docs/` 目录：
- [设计文档](go/docs/design.md) — 系统架构与核心设计
- [API 接口](go/docs/api.md) — 完整 REST API 参考
- [数据库设计](go/docs/database.md) — 表结构与索引
- [服务层文档](go/docs/services.md) — Service 层详解
- [测试文档](go/docs/testing.md) — 测试用例与运行方式
