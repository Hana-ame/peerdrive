# 架构简化待办

## 数据模型清理

| # | 项 | 当前 | 目标 | 风险 |
|---|-----|------|------|------|
| M1 | `AnonEntry` 旧格式 | `model/anon.go` 中 `{path, hash, url}` 旧 struct | 删除，全部使用 `AnonCollectionEntry` | 可能有引用未清理 |
| M2 | `file_meta` + `file_providers` 表 | 独立的"文件"SQL 存储 | 合并到 collection 体系，文件=单 entry collection | 大改，影响所有下载 |

## API 端点简化

| # | 项 | 当前 | 目标 | 风险 |
|---|-----|------|------|------|
| A1 | `/files/upload` | 独立文件上传端点 | 转为 `POST /collections` 创建单 entry 合集 | 前端调用需更新 |
| A2 | `/files/register_local` | 注册本地文件 | 同上 | 同上 |
| A3 | `/files/register_url` | 注册 URL 文件 | 同上 | 同上 |
| A4 | `/files/register_folder` | 注册文件夹 | 同上 | 同上 |
| A5 | `/actions/*` | fork/merge/pull 独立路由 | 并入 `/collections/` | 路由变动 |
| A6 | `/download/` vs `/sha256sum/` | 两个下载端点重复 | 统一为一个 | 向前兼容需保留 |

## 前端清理

| # | 项 | 当前 | 目标 | 风险 |
|---|-----|------|------|------|
| F1 | `Explorer.jsx` | 独立命名合集浏览页 | 合并到 `AnonExplorer` | 功能差异（版本控制） |
| F2 | `CollectionBuilder.jsx` | 未使用的独立组件 | 删除 | 低 |
| F3 | `AnonCollectionManager.jsx` | 未使用的独立组件 | 删除 | 低 |
| F4 | Navbar 搜索"文件"段 | 搜索结果含 files 分类 | 移除 | 低 |

## 杂项

| # | 项 | 当前 | 目标 | 风险 |
|---|-----|------|------|------|
| X1 | package.json 残留 | playwright install 遗留 | 恢复干净状态 | 低 |
