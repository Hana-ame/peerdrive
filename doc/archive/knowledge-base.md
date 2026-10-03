# Peerdrive 技术知识库

## 一、项目概述

**Peerdrive** 是一个 P2P 文件分享系统，支持去中心化存储和传输。

---

## 二、架构方案

### 2.1 技术选型

| 方案 | 特点 | 适用场景 |
|------|------|----------|
| **WebRTC** | 浏览器可直接运行，无安装 | 网页分享 |
| **libp2p** | 完整 P2P 网络栈，DHT/NAT穿透 | 完整客户端 |
| **混合** | Web前端 + 服务端信令 | 兼容更多网络 |

### 2.2 核心技术问题

1. **Peer Discovery** - 客户端之间如何互相找到
2. **NAT 穿透 + 信令** - 如何建立直接连接
3. **文件传输** - 分块、加密、断点续传
4. **权限控制** - 匿名房间/令牌/角色

---

## 三、架构亮点

### 3.1 完美的依赖方向
`Controller` -> `Service` -> `Provider/Repository`，无反向依赖，无循环引用。

### 3.2 优雅的文件名降级策略
先取 DB 里的 `meta.Filename`，如果为空才用 `providerManager` 返回的 hint，避免 DB 元数据污染到底层。

### 3.3 Provider 接口极度精简
`GetReader` 和 `GetFilenameHint` 刚好满足下载流式传输需求，没有过度设计。

### 3.4 优雅退出
使用 `signal.Notify` 监听 SIGINT/SIGTERM，防止僵尸进程和端口占用。

---

## 四、代码结构 (按 prompt.md 分层)

```
peerdrive/
├── cmd/server/main.go           # 程序入口
├── internal/
│   ├── controller/               # HTTP 处理层
│   │   ├── ping.go              # GET /ping
│   │   ├── download.go          # GET /sha256sum/:sha256
│   │   └── p2p.go               # /p2p/node, /p2p/peers, /p2p/ping/:peer_id
│   ├── service/                 # 业务逻辑层
│   │   ├── downloader.go        # 下载逻辑
│   │   └── p2p.go               # libp2p 封装
│   ├── provider/               # 内容提供者
│   │   ├── provider.go          # ContentProvider 接口
│   │   ├── local.go             # 本地文件
│   │   ├── http.go              # 远程 URL
│   │   └── manager.go           # Provider 管理 + 路由
│   ├── repository/             # 数据访问层
│   │   ├── db.go                # 数据库连接
│   │   └── file_repo.go         # files 表 CRUD
│   └── model/                   # 数据结构
│       └── file.go              # FileMetadata
├── pkg/hashutil/               # 工具函数
│   └── hashutil.go              # SHA256 校验
├── storage/                    # 本地文件存储
├── testdata/                   # 测试文件
└── assets/                     # 知识库
```

---

## 五、API 接口

| 接口 | 方法 | 功能 |
|------|------|------|
| `/ping` | GET | 健康检查 |
| `/sha256sum/:sha256` | GET | 下载文件 |
| `/p2p/node` | GET | 获取节点信息 (PeerID, Addrs) |
| `/p2p/peers` | GET | 获取已连接节点列表 |
| `/p2p/ping/:peer_id` | GET | ping 指定节点，返回 RTT |

---

## 六、核心代码

### 6.1 Model (internal/model/file.go)

```go
type FileMetadata struct {
    ID           int    `db:"id"`
    Hash         string `db:"hash"`
    ProviderType string `db:"provider_type"`
    Path         string `db:"path"`
    Filename     string `db:"filename"`
}
```

### 6.2 Provider 接口 (internal/provider/provider.go)

```go
type ContentProvider interface {
    GetReader(path string) (io.ReadCloser, error)
    GetFilenameHint(path, originalFilename string) string
}
```

### 6.3 Repository (internal/repository/db.go)

```go
var DB *sql.DB

func InitDB(dbPath string) error
func GetFileByHash(hash string) (*model.FileMetadata, error)
func InsertFile(meta *model.FileMetadata) error
```

### 6.4 Downloader Service (internal/service/downloader.go)

```go
type Downloader struct {
    providerManager *provider.Manager
}

func (d *Downloader) GetFileStream(hash string) (io.ReadCloser, string, error)
```

### 6.5 P2P Service (internal/service/p2p.go)

```go
type P2PService struct {
    Host host.Host
    Ping *ping.PingService
}

func NewP2PService(ctx context.Context) (*P2PService, error)
func (p *P2PService) GetNodeInfo() (peer.ID, []string)
func (p *P2PService) GetConnectedPeers() []peer.ID
func (p *P2PService) PingPeer(ctx context.Context, peerID peer.ID) (time.Duration, error)
```

### 6.6 Hash Util (pkg/hashutil/hashutil.go)

```go
func IsValidSHA256(s string) bool
```

---

## 七、开发路线

### Phase 1: 最小可行原型
- 同一局域网内文件传输
- WebRTC + STUN/TURN
- 验证核心传输流程

### Phase 2: 完善传输
- 分块传输与断点续传
- 大文件支持
- 进度显示

### Phase 3: 去中心化发现
- DHT 方案
- 房间名发现机制

### Phase 4: 权限与安全
- 安全令牌机制
- 端到端加密
- 身份验证

---

## 八、部署配置

### 8.1 Go 后端
- 入口: `go run ./cmd/server/main.go`
- 端口: 3000
- 数据库: `./peerdrive.db`
- 本地存储: `./storage/`

### 8.2 React 前端
- 框架: Vite + React + TypeScript
- 分支: frontend
- 部署: Cloudflare Pages

---

## 九、测试方案

### 9.1 Repository 层测试 (internal/repository/file_repo_test.go)

```go
func TestFileRepoCRUD(t *testing.T) {
    err := InitDB(":memory:")  // 内存数据库
    // 测试 Insert, GetFileByHash
}
```

### 9.2 运行测试

```bash
go test ./... -v
```

### 9.3 测试结果

```
=== RUN   TestFileRepoCRUD
--- PASS: TestFileRepoCRUD

PASS ok peerdrive/internal/repository
```

---

## 十、参考项目

### 10.1 libp2p 方案

- **distributed-file-storage-go**: https://github.com/pxnt/distributed-file-storage-go
- **pcp**: https://github.com/dennis-tra/pcp
- **go-libp2p 官方示例**: https://github.com/libp2p/go-libp2p/tree/master/examples

### 10.2 WebRTC 方案

- **sharedrop**: https://github.com/szimek/sharedrop
- **file-transfer-go**: https://github.com/matrixji/file-transfer-go

### 10.3 去中心化发现

- **Hyperswarm**: https://github.com/holepunchto/hyperswarm
- **rust-libp2p 文件共享**: https://github.com/libp2p/rust-libp2p/tree/master/examples/file-sharing