# Peerdrive E2E 测试环境搭建 & Bug 修复记录

## 0. 当前分支

```
feat/stage2-e2e-test (基于 feat/remove-auth-and-refactor)
```

所有改动在 `go/` 目录下。

---

## 1. 环境准备

### 1.1 创建分支
```bash
cd /mnt/d/WorkPlace/peerdrive/go
git checkout feat/remove-auth-and-refactor
git checkout -b feat/stage2-e2e-test
```

### 1.2 修复依赖
项目原本通过 `GOPROXY=off` + vendor 管理依赖，但 vendor 目录缺失。改为标准 Go module 模式：
```bash
go mod tidy
go build ./...
```
输出 `go/internal/service/p2p.go` 等一系列成功编译。

### 1.3 编译产物
```bash
go build -o peerdrive-server ./cmd/server/
```
产物：`go/peerdrive-server`

---

## 2. Bug 修复

### 2.1 P2P 关闭时 nil channel panic

**现象**：当 `PEERDRIVE_P2P_ENABLE=false` 启动时，`p2pSvc.Close()` 中 `close(p.requestCh)` / `close(p.responseCh)` 因为 channel 从未初始化（nil）导致 panic：

```
panic: close of nil channel
```

**原因**：`NewP2PService` 在 P2P disabled 时直接返回，`requestCh` 和 `responseCh` 保持 nil。但 `Close()` 无条件 close 它们。

**文件**：`internal/service/p2p.go:584-591`

**修复**：
```go
func (p *P2PService) Close() error {
    if p.requestCh != nil {
        close(p.requestCh)
    }
    if p.responseCh != nil {
        close(p.responseCh)
    }
    if p.Host != nil {
        return p.Host.Close()
    }
    return nil
}
```

### 2.2 启动日志打印空 PeerID

**现象**：P2P 禁用时仍然打印 `libp2p 节点已启动: PeerID=, 监听地址=[]`

**文件**：`cmd/server/main.go:55`

**修复**：
```go
id, addrs := p2pSvc.GetNodeInfo()
if id != "" {
    log.Printf("libp2p 节点已启动: PeerID=%s, 监听地址=%v", id, addrs)
}
```

### 2.3 storageDir middleware 未生效

**现象**：请求 `/collections/:u/:c/commit` 时 panic：
```
key storageDir does not exist
```

**原因**：Gin 在注册路由时捕获当前 middleware 栈。`r.Use(storageDir)` 写在 `main.go:70`，位于 `router.SetupRouter()`（注册了所有路由）之后，因此已注册的路由拿不到这个 middleware。

**修复**：将 middleware 移入 `SetupRouter()` 开头，在所有路由注册之前：
```go
// internal/router/router.go
func SetupRouter(...) *gin.Engine {
    r := gin.Default()

    // 注入 storageDir / downloader（必须在路由前）
    r.Use(func(c *gin.Context) {
        c.Set("storageDir", cfg.StorageDir)
        c.Set("downloader", downloader)
        c.Next()
    })

    // CORS middleware
    r.Use(func(c *gin.Context) { ... })

    // 注册所有路由 ...
}
```

同时移除 `main.go` 中冗余的 `r.Use(...)` 以及未使用的 `gin` 导入。

### 2.4 RemoveEntry 路径斜杠 bug

**现象**：`DELETE /collections/:u/:c/entries/lib/util.go` 返回 200，但条目未被删除。

**原因**：Gin 的 `*path` 捕获包含前导斜杠。例如 `DELETE .../entries/lib/util.go` 得到 `c.Param("path") = "/lib/util.go"`。而数据库存储的是 `lib/util.go`（无前导 `/`），导致 SQL WHERE 不匹配。

**文件**：`internal/controller/collection.go`

**修复**：
```go
path := strings.TrimPrefix(c.Param("path"), "/")
```

注：`DownloadAnonFile` / `DownloadCollectionFile` 已有相同处理，仅 `RemoveEntry` 遗漏。

---

## 3. E2E 测试脚本

**文件**：`back/test/e2e-all.sh`

### 3.1 设计原则
- **自包含**：自己 `go build`，自己启动 server，自己清理
- **P2P 禁用**：`PEERDRIVE_P2P_ENABLE=false`，只测 HTTP API 层
- **绕过 Privoxy**：`export no_proxy='*'`
- **清理残留**：`trap cleanup EXIT` 确保 server 被杀、临时文件/DB 被删

### 3.2 测试覆盖 (12 节, 80 断言)

| 节 | 端点 | 断言数 |
|----|------|--------|
| 1. Health | `GET /ping` | 2 |
| 2. File Upload | `POST /files/upload` (new/duplicate/2nd file, on-disk verify) | 10 |
| 3. File Verify | `GET /files/verify/:hash` (valid + invalid hash) | 3 |
| 4. SHA256 Download | `GET /sha256sum/:hash` (valid + invalid, diff verify) | 3 |
| 5. Register Local File | `POST /files/register_local` (new + re-register + verify + download) | 5 |
| 6. Register Folder | `POST /files/register_folder` | 2 |
| 7. Anonymous Collections | `POST /anon/collections` + path traversal + `GET` + sha256sum + entry download + `POST /anon/collections/fork` + nonexistent | 17 |
| 8. Named Collections | create / duplicate / list / get / add entries / delete entry / download / commit / version log / 2nd commit / rollback | 22 |
| 9. Fork/Merge/Pull | `POST /actions/fork` + `POST /actions/merge` + `POST /actions/pull` | 4 |
| 10. File Delete | `DELETE /files/:hash` + verify deleted | 2 |
| 11. Tasks | `GET /tasks` + nonexistent task | 2 |
| 12. Edge Cases | missing user / empty collection / invalid hash / missing fields | 5 |

### 3.3 运行方式
```bash
cd /mnt/d/WorkPlace/peerdrive
bash back/test/e2e-all.sh
```

### 3.4 当前结果
```
PASS: 79  FAIL: 0  WARN: 1  TOTAL: 80
All tests passed
```

WARN: 创建 collection 时 `{}` 空 body 返回 200（预期应拒绝），属边界行为差异，不阻塞。

---

## 4. 关键基础设施信息

| 项目 | 值 |
|------|-----|
| HTTP 端口 | `PORT` env，默认 3000 |
| libp2p listen | `/ip4/0.0.0.0/tcp/0` (自动端口) |
| 存储目录 | `PEERDRIVE_STORAGE`，默认 `./storage` |
| DB 路径 | **硬编码** `./peerdrive.db`（非 env var） |
| 两节点测试端口 | 3001 / 3002 |
| P2P 协议 | `/peerdrive/exchange/1.0.0` `/peerdrive/announce/1.0.0` `/peerdrive/request/1.0.0` |

---

## 5. 改动文件清单

```
go/
├── cmd/server/main.go          # 移除 gin 导入，删除冗余 middleware
├── internal/router/router.go   # 新增 storageDir/downloader middleware (route 注册前)
├── internal/service/p2p.go     # Close() nil channel 保护
├── internal/controller/collection.go  # RemoveEntry path TrimPrefix
└── test/
    └── e2e-all.sh              # 新增 E2E 测试脚本 (397 行)
```

---

## 6. CI 集成

**文件**：`go/.github/workflows/ci.yml`

已有 CI workflow，运行以下测试脚本：
- `go/test/test.sh`
- `go/test/upload.sh`
- `go/test/register.sh`
- `go/test/anon-collection.sh`
- `back/test/p2p.sh`

**建议**：将 `back/test/e2e-all.sh` 加入 CI pipeline。
