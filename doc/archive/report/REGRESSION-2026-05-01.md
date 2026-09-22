# 后端测试回归报告

> 日期: 2026-05-01 | 分支: main | 触发: 重构后验证

## 测试结果: ✅ 全部通过

```
go test ./... -count=1 -race
```

| 包 | 覆盖率 | 结果 | 耗时 |
|---|--------|------|------|
| `internal/config` | 45.9% | ✅ | 1.03s |
| `internal/controller` | 13.1% | ✅ | 1.23s |
| `internal/model` | 45.5% | ✅ | 1.03s |
| `internal/p2p_bt` | 36.9% | ✅ | 1.28s |
| `internal/provider` | 93.5% | ✅ | 26.76s |
| `internal/repository` | 28.1% | ✅ | 1.79s |
| `internal/service` | 11.6% | ✅ | 2.67s |
| **合计** | — | **全部通过** | **35.79s** |

## 静态分析

| 检查项 | 结果 |
|--------|------|
| `go vet ./...` | 干净 (0 警告) |
| `go build ./...` | 编译成功 |
| 残留引用检查 | 无引用已删除代码 |

## 重构验证

上次重构（29595be）移除了旧 ContentProvider/Manager/Downloader，统一为 UniversalDownloader。

| 对比 | 旧测试 | 新测试 |
|------|--------|--------|
| Provider 测试 | `provider_test.go` (92行) | 功能已纳入 `universal_downloader_test.go` |
| Downloader 测试 | `downloader_test.go` (66行) | `universal_downloader_test.go` (405行, 20 函数) |
| 合计覆盖 | ~158行 | 405行 (更全面) |

### UniversalDownloader 测试覆盖

- **LocalFetcher**: 存储目录 / P2P子目录 / DB提供者 / 文件不存在 / IsAvailable
- **IPFSFetcher**: 服务未就绪 / Name
- **BTDHTFetcher**: 服务未就绪 / Name
- **HTTPURLFetcher**: 从测试服务器获取 / 无提供者 / Name / IsAvailable
- **多协议编排**: 默认顺序 / 自定义顺序 / 未知协议跳过 / 顺序匹配
- **下载管线**: 本地文件 / Fallback (local→http) / 全部失败
- **缓存**: 写入本地 / 二次读取验证

## 结论

重构后无回归问题。所有测试通过，Race detector 无数据竞争，静态分析干净。新 UniversalDownloader 的测试覆盖比被替代的旧代码更全面。
