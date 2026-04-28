# Peerdrive 测试文档

## 测试分类

### 1. Go 单元测试 (`go test ./...`)

覆盖率 5 个包，66 个测试全部通过。

| 包 | 测试文件 | 测试数 | 覆盖内容 |
|----|----------|--------|----------|
| `internal/config` | `config_test.go` | 10 | Load() defaults, getEnv (default/set), getEnvBool (true/false/1/0), parseRelayMode |
| `internal/model` | `anon_test.go` | 3 | NewAnonCollection (name+entries, nil→empty, empty name) |
| | `collection_test.go` | 3 | Collection/CollectionEntry/CollectionVersion struct |
| `internal/repository` | `file_repo_test.go` | 5 | InsertFileMeta + GetFileMeta, nonexistent, InsertFileProvider + GetFileProviders, MarkProviderUnavailable, empty providers |
| `internal/service` | `file_service_test.go` | 9 | NewFileService, RegisterLocal (valid/nonexistent/disabled), RegisterFolder, Verify, Delete, DeleteStorageDisabled |
| | `anon_service_test.go` | 10 | CreateCollection (valid/traversal/invalid hash/empty/empty path/absolute path), GetCollection, ForkCollection, DownloadFile |
| | `sync_service_test.go` | 10 | PathTraversal, Filtering |
| `internal/controller` | `ping_test.go` | 1 | Ping 返回 pong |
| | `collection_test.go` | 16 | CreateCollection (valid/duplicate/invalidBody), ListCollections, GetCollection, AddEntry, RemoveEntry, CommitCollection, GetVersionLog, SearchCollections, ForkCollection, RollbackCollection |

运行方式：
```bash
cd go && go test ./... -count=1
```

### 2. E2E 测试 (`go/test/e2e-all.sh`)

自包含脚本，覆盖全部 HTTP API 端点。85 条断言，12 个测试段。

运行方式：
```bash
cd /mnt/d/WorkPlace/peerdrive
rm -f peerdrive.db
bash go/test/e2e-all.sh
```

**要求**：
- Go 编译器（脚本自己 build）
- `python3`（JSON 解析）
- 端口 3999 空闲
- 无 Privoxy 或已配置 `no_proxy='*'`（脚本自动设置）

**测试段**：

| # | 段名 | 断言数 | 覆盖端点 |
|---|------|--------|----------|
| 1 | Health | 2 | `GET /ping` |
| 2 | File Upload | 10 | `POST /files/upload` (new/duplicate/2nd, on-disk verify) |
| 3 | File Verify | 3 | `GET /files/verify/:hash` (valid + invalid) |
| 4 | SHA256 Download | 3 | `GET /sha256sum/:hash` (valid + invalid + diff) |
| 5 | Register Local File | 5 | `POST /files/register_local` (new + re-register + verify + download) |
| 6 | Register Folder | 2 | `POST /files/register_folder` |
| 7 | Anonymous Collections | 22 | Create, path traversal, friendly_name, GET + entries, sha256sum, entry download, fork, **commit** (versioning), nonexistent |
| 8 | Named Collections | 22 | Create/duplicate/list/get/add entry/delete entry/download/commit/log/2nd commit/rollback |
| 9 | Fork/Merge/Pull | 4 | `POST /actions/fork`, `/actions/merge`, `/actions/pull` |
| 10 | File Delete | 2 | `DELETE /files/:hash` + verify deleted |
| 11 | Tasks | 2 | `GET /tasks` + nonexistent task |
| 12 | Edge Cases | 5 | Missing user, empty collection, invalid hash, missing fields |

预期输出：
```
PASS: 84  FAIL: 0  WARN: 1  TOTAL: 85
```

### 3. 其他测试脚本

这些脚本假设服务器已在端口 3000 运行：

| 脚本 | 用途 | 运行方式 |
|------|------|----------|
| `go/test/test.sh` | 完整集成测试 | 需要先启 server: `PORT=3000 PEERDRIVE_P2P_ENABLE=false ./peerdrive-server` |
| `go/test/upload.sh` | 文件上传测试 | 同上 |
| `go/test/register.sh` | 文件注册测试 | 同上 |
| `go/test/anon-collection.sh` | 匿名合集测试 | 同上 |
| `go/test/p2p.sh` | P2P 双节点测试 | 自包含（自己 build 并启动 2 节点） |
| `go/test/relay.sh` | Relay + NAT 穿透测试 | 自包含 |

### 4. React 前端测试

```bash
cd react
npm test             # 运行 vitest
npm run dev          # 启动 Vite dev server (开发用)
npm run build        # 生产构建
```

### 5. CI 配置

**文件**：`go/.github/workflows/ci.yml`

当前运行的测试：
```yaml
- go/test/test.sh
- go/test/upload.sh
- go/test/register.sh
- go/test/anon-collection.sh
- go/test/p2p.sh
```

建议加入：
```yaml
- go/test/e2e-all.sh
- go test ./...
```
