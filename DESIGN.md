# Peerdrive 系统设计文档

## 1. 概述
Peerdrive 是一个基于内容寻址（Content-Addressable Storage, CAS）的 P2P 文件共享与分发系统。它允许用户通过文件的 SHA256 哈希值唯一标识内容，并提供一个将这些内容组织为“合集（Collection）”的机制。系统支持多副本冗余、P2P 自动回退下载以及基于快照的版本控制。

## 2. 核心架构

### 2.1 内容寻址存储 (CAS)
- **唯一标识**：所有文件使用其内容的 SHA256 哈希值作为唯一 ID。
- **解耦存储**：通过元数据层将“内容标识（Hash）”与“物理位置（Path/URL）”解耦。
- **详细分析** $\rightarrow$ [SHA256SUM_LAYER.md](./SHA256SUM_LAYER.md)

### 2.2 多副本元数据模型
为了支持高可用性，系统采用分层存储模型：
- **`file_meta` 表**：存储内容的固有属性（大小、MIME 类型、是否 gzip 压缩、文件类型）。每种唯一内容仅有一条记录。
- **`file_providers` 表**：存储该内容的多个物理副本位置。记录包括提供者类型（`local` 或 `http`）和可用性状态（`available`）。
- **副本策略**：下载时循环尝试所有可用副本，若某副本失效则标记为不可用。
- **数据库定义** $\rightarrow$ [DB.md](./DB.md) | **实现逻辑** $\rightarrow$ [SHA256SUM_LAYER.md](./SHA256SUM_LAYER.md)

### 2.3 P2P 下载回退机制
系统实现了一套分级下载策略，以确保文件最高可用性：
1. **本地存储** $\rightarrow$ 2. **远程 HTTP 副本** $\rightarrow$ 3. **P2P Bitswap 网络**
- 当所有已知副本均失效时，系统将通过 libp2p 协议在网络中广播请求，尝试从其他对等节点拉取数据。
- 成功拉取的数据将自动缓存至本地，并更新元数据记录。
- **实现细节** $\rightarrow$ [SHA256SUM_LAYER.md](./SHA256SUM_LAYER.md)

## 3. 合集系统 (Collection System)

### 3.1 匿名合集 (Anonymous Collection)
- **本质**：一个包含 `path` $\rightarrow$ `hash` 映射关系的 JSON 文件。
- **特性**：不可变（Immutable）。一旦生成，其内容决定了其 SHA256 哈希值。
- **存储**：匿名合集本身也被视为一个文件，存储在 `file_meta` 中，通过其自身的哈希值寻址。
- **详细分析** $\rightarrow$ [ANON_COLLECTION_LAYER.md](./ANON_COLLECTION_LAYER.md)

### 3.2 注册用户合集
- **指针机制**：用户合集在数据库中是一个可变记录，通过 `current_hash` 字段指向一个特定的匿名合集快照。
- **版本控制**：
    - **Commit**：将当前工作区状态序列化为匿名合集 $\rightarrow$ 存储 $\rightarrow$ 更新 `current_hash` $\rightarrow$ 记录版本历史。
    - **Rollback**：将 `current_hash` 指向历史版本快照。
    - **Fork/Merge**：基于匿名快照的不可变性，可以轻松实现合集的分支与合并。
- **实现逻辑** $\rightarrow$ [ANON_COLLECTION_LAYER.md](./ANON_COLLECTION_LAYER.md) | **数据结构** $\rightarrow$ [DB.md](./DB.md)

## 4. 身份认证系统 (Auth System)

- **集中式管理**：使用 `users` 表存储账户信息及密码哈希（bcrypt）。
- **会话机制**：登录后发放随机生成的 `authkey` (Bearer Token)，存储在客户端 `localStorage` 中。
- **权限控制**：
    - **公开接口**：下载文件、浏览公共合集、搜索合集。
    - **受保护接口**：上传文件、创建/修改合集、执行 Commit/Rollback 等写操作。
- **数据定义** $\rightarrow$ [DB.md](./DB.md)

## 5. 文件导入与上传

- **文件上传 (Upload)**：通过 HTTP Multipart 上传 $\rightarrow$ 计算 Hash $\rightarrow$ 存储 $\rightarrow$ 注册元数据。
- **文件注册 (Register)**：直接引用本地已存在的文件路径，无需复制。
- **上传分析** $\rightarrow$ [UPLOAD_LAYER.md](./UPLOAD_LAYER.md) | **注册分析** $\rightarrow$ [REGISTER_LAYER.md](./REGISTER_LAYER.md)

## 6. 技术栈
- **后端**: 
    - 语言: Go
    - P2P 协议: libp2p
    - 数据库: SQLite3
    - Web 框架: Gin
- **前端**: 
    - 框架: React (JSX)
    - 样式: Tailwind CSS
    - 构建工具: Vite

## 7. API 逻辑概览
- `/auth/*`: 注册、登录、登出、身份校验。
- `/sha256sum/:hash`: 基于 CAS 的文件直接下载（含 P2P 回退）。
- `/anon/*`: 匿名合集的创建与读取。
- `/collections/*`: 注册用户的合集管理、版本提交与回滚。
- `/files/*`: 物理文件的上传与注册。
- `/p2p/*`: 节点状态查询与对等节点列表。
