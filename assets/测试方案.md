# Peerdrive 测试方案

## 一、测试修复记录

### 1.1 重构前的旧问题

#### 问题 1: seed_db.go 重复 main
- **现象**: `main redeclared in this block`
- **原因**: `seed_db.go` 和 `main.go` 都定义了 `main()`
- **修复**: 将 `seed_db.go` 中的 `main()` 改为 `seedDB()` 函数

#### 问题 2: API 不匹配
- **现象**: `repo.SaveMetadata undefined`，`db.FileMetadata undefined`
- **原因**: 代码中调用了不存在的 API
- **修复**: 
  - `SaveMetadata` → `SaveLocation`
  - `db.FileMetadata` → `db.FileLocation`

### 1.2 重构后的测试补充

#### 问题 3: libp2p Ping API 变更
- **现象**: `not enough return values`
- **原因**: 新版 libp2p ping 返回 channel
- **修复**:
```go
func (p *P2PService) PingPeer(ctx context.Context, peerID peer.ID) (time.Duration, error) {
    result := p.Ping.Ping(ctx, peerID)
    select {
    case res := <-result:
        return res.RTT, res.Error
    case <-ctx.Done():
        return 0, ctx.Err()
    }
}
```

## 二、单元测试

### 2.1 Repository 层测试 (推荐)

```go
func TestFileRepoCRUD(t *testing.T) {
    err := InitDB(":memory:")  // 内存数据库
    meta := &model.FileMetadata{
        Hash:         "abcdef1234567890...",
        ProviderType: "local",
        Path:         "test.txt",
        Filename:     "hello.txt",
    }
    err = InsertFile(meta)
    found, err := GetFileByHash(meta.Hash)
    // 验证 found.Filename == "hello.txt"
}
```

### 2.2 Controller 层

#### TestPing
- 验证返回 "pong"
- HTTP 200

#### TestDownloadBySHA256
- 测试下载流程
- SHA256 格式验证

### 2.3 Provider 层

#### TestLocalProvider
- 本地文件读取
- 路径拼接
- 错误处理

#### TestHTTPProvider
- HTTP 请求
- 状态码处理

## 三、测试运行

```bash
# 运行所有测试
go test ./... -v

# 运行特定包
go test ./internal/repository -v

# 覆盖率
go test ./... -cover
```

## 四、测试覆盖

| 模块 | 测试文件 | 覆盖内容 |
|------|----------|----------|
| internal/model | - | (无) |
| internal/repository | file_repo_test.go | CRUD 操作 |
| internal/provider | - | (无) |
| internal/service | - | (无) |
| internal/controller | - | (需补充) |
| pkg/hashutil | - | (需补充) |