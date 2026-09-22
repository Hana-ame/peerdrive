# Peerdrive 架构审视报告

> 日期: 2026-04-30 | 版本: 1.0

---

## 🔴 Bug

### 1. HTTPProvider 返回 `(nil, nil)` 导致空指针
- **文件**: `back/internal/provider/http.go:24`
- **问题**: `resp.StatusCode != http.StatusOK` 时，代码 close body 后返回 `err`（此时为 nil），调用方收到 `(nil, nil)`
- **修复**: 返回 `fmt.Errorf("HTTP %d", resp.StatusCode)`

### 2. Collection.ScanRow 完全无效
- **文件**: `back/internal/model/collection.go:41-50`
- **问题**: 方法创建局部变量、从空 receiver 复制字段、返回 nil，数据库扫描被跳过
- **修复**: 实现真正的 `scanner.Scan()` 调用

---

## 🟠 结构性问题

### 3. 两套并行的下载抽象
- **ContentProvider** + **Manager**（`provider/` 包）: GetReader + GetFilenameHint
- **ProtocolFetcher** + **UniversalDownloader**（`service/` 包）: Fetch + Name + IsAvailable
- **影响**: 两套接口服务于同一目的，controller 先调 UniversalDownloader 再回退旧 Downloader
- **建议**: 移除 ContentProvider/Manager，统一为 ProtocolFetcher

### 4. Controller 全局可变状态
- 17 个 `var` 声明 + `Init*` 函数（`controller/*.go`）
- 无法单测、隐式初始化顺序、隐藏依赖
- **建议**: 改为结构体字段注入

### 5. nodestate 包仅为解决循环引用
- **文件**: `back/internal/nodestate/nodestate.go`
- controller 和 service 都需要访问同一状态，创建了独立包当全局变量用
- **建议**: 定义接口打破循环依赖

### 6. router.go 是 God Object
- **文件**: `back/internal/router/router.go:48-440`
- SetupRouter 初始化 15+ 个服务实例，直接 import 所有内部包
- **建议**: 服务初始化提取到 main.go 或独立 DI 函数

---

## 🟡 重复代码

### 7. HTTP Range 解析重复
- `controller/download.go:185` — `handleRangeRequest`
- `service/relay.go:383` — `parseRange`
- **建议**: 提取到共享工具函数

### 8. IPFSCompatLayer 文件复制 vs IPFSService Blockstore
- `ipfs_compat.go` 回退模式复制文件到 `ipfs-blocks/`
- `ipfs_service.go` 的 `peerdriveBlockstore` 直接 CID→SHA-256 映射
- **建议**: 统一为一套存储路径

### 9. 两套 STUN/TURN 配置
- `Config.STUNServer`/`TURNServer`（默认 `stun.moonchan.xyz`）
- `Config.WebRTCSTUNServer`/`WebRTCTURNServer`（默认 `stun.l.google.com`）
- **影响**: 第一套疑似未使用

---

## ⚪ 死代码

### 10. WebRTCFetcher 占位符
- **文件**: `service/universal_downloader.go:158-175`
- `IsAvailable()` 永远 false，`Fetch()` 永远 `"not yet implemented"`

### 11. model.AnonEntry legacy 类型
- **文件**: `model/anon.go:138-142`
- 标注 "legacy compatibility"，需确认是否仍有引用

---

## 🔵 杂项

### 12. 根目录二进制文件
- `peerdrive.db` — SQLite 数据库，应加入 .gitignore
- `go.zip` / `react.zip` — 归档文件，应移除出 git

### 13. 测试不足
- `internal/log`、`internal/nodestate` 无测试
- 集成测试是 `main()` 而非 `*_test.go`

### 14. 错误处理不一致
- 部分 controller 返回 `{"error": "invalid request"}`，部分返回 `{"error": err.Error()}`
- 多处使用 `_ =` 吞掉错误

---

## 建议修复顺序

| 优先级 | 问题 | 影响 |
|--------|------|------|
| P0 | HTTPProvider nil-return bug | 运行时 panic |
| P0 | Collection.ScanRow 空操作 | 数据读取静默失败 |
| P1 | 根目录二进制清理 | 仓库清洁 |
| P1 | 死代码清理 (WebRTCFetcher) | 减少混淆 |
| P2 | 统一下载抽象 | 架构简化 |
| P2 | controller 依赖注入 | 可测试性 |
| P3 | router 拆分 | 可维护性 |
| P3 | Range 解析去重 | DRY |
