# 架构审视修复记录

> 日期: 2026-04-30 | 关联报告: [ARCHITECTURE-REVIEW.md](./ARCHITECTURE-REVIEW.md)

## 已修复

### P0 Bug

| # | 问题 | 文件 | 修复 |
|---|------|------|------|
| 1 | HTTPProvider 返回 `(nil, nil)` | `provider/http.go:24` | 非200状态码返回 `fmt.Errorf("HTTP %d", resp.StatusCode)` |
| 2 | Collection.ScanRow 空操作 | `model/collection.go:41` | 重写为正确调用 `scanner.Scan()` 并将数据复制回 receiver |

### P1 死代码清理

| # | 问题 | 操作 |
|---|------|------|
| 3 | WebRTCFetcher 占位符（永远不可用） | 移除类型、构造函数、registry 条目、3 个测试函数 |
| 4 | 根目录 `*.zip`、`*.db` | 确认 .gitignore 已覆盖，无需额外操作 |

### 文档

| # | 文件 | 内容 |
|---|------|------|
| 5 | `doc/report/ARCHITECTURE-REVIEW.md` | 完整审视报告，含 14 项问题和优先级排序 |

## 后续 PR 建议

| 优先级 | 问题 | 预计工作量 |
|--------|------|-----------|
| P2 | 统一下载抽象（移除 ContentProvider/Manager 死代码） | 中等 |
| P2 | controller 依赖注入（去全局变量） | 大 |
| P3 | router 拆分（服务初始化提取到 main.go） | 中等 |
| P3 | HTTP Range 解析去重 | 小 |
| P3 | 两套 STUN/TURN 配置合并 | 小 |
