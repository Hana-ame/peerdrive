# 架构简化待办

## 数据模型清理

| # | 项 | 状态 | 风险 |
|---|-----|------|------|
| M1 | `AnonEntry` 旧格式 | ✅ 已删除 | 低 |
| M2 | `file_meta` + `file_providers` 表 | ❌ 未做 | 最高 |

## API 端点简化

| # | 项 | 状态 | 风险 |
|---|-----|------|------|
| A1 | `/files/upload` → `/collections/` | ✅ 已过时（2026-08-17 admin 迁移后不再必须） | 中 |
| A2 | `/files/register_local` → `/collections/` | ✅ 已过时（同上） | 中 |
| A3 | `/files/register_url` → `/collections/` | ✅ 已过时（同上） | 中 |
| A4 | `/files/register_folder` → `/collections/` | ✅ 已过时（同上） | 中 |
| A5 | `/actions/*` → `/collections/` | ✅ 已合并 + 301 redirect | 中 |
| A6 | `/download/` vs `/sha256sum/` | ✅ 已区分：sha256sum=本地，download=多协议 | 中 |

> A1-A4 说明：2026-08-17 前端全面迁移到本地 WS admin 帧（transport/admin.go 内部
> 转发）后，前端不再直接调用这些 HTTP 端点；端点按 legacy 兼容策略保留（旧版
> 前端/curl/外部脚本 + 集成测试直走 HTTP），与 router.go 注释一致。合并到
> `/collections/` 已无收益，见 REFACTOR.md §3.10。

## 前端清理

| # | 项 | 状态 | 风险 |
|---|-----|------|------|
| F1 | `Explorer.jsx` 合并到 `AnonExplorer` | ❌ 未做 | 中 |
| F2 | `CollectionBuilder.jsx` | ✅ 已删除 | 低 |
| F3 | `AnonCollectionManager.jsx` | ✅ 已删除 | 低 |
| F4 | Navbar 搜索"文件"段 | ✅ 已移除 | 低 |

## 杂项

| # | 项 | 状态 | 风险 |
|---|-----|------|------|
| X1 | package.json 残留 | ✅ 已清理 | 低 |
