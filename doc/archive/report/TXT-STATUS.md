# .txt 文件处理状态

> 2026-04-29

---

## 已处理完成 → 移入 `docs/archive/`

| 文件 | 内容 | 状态 |
|------|------|------|
| `archive/TODO.txt` | 22 项前端 UX 需求 | ✅ 全部实现 |
| `complain.txt` | 5 项 UI 问题 | ✅ 全部修复，已移至 archive |
| `我的测试结果3.txt` | 用户测试反馈 | ✅ 已移至 archive |
| archive 中其他 30+ .txt | 早期用户反馈 | ✅ 历史存档，不再查看 |

## 仅保留活跃文件

| 文件 | 位置 | 说明 |
|------|------|------|
| `todo.txt` | 根目录 | 当前需求（已重构为 3 条核心要求） |

---

## complain.txt 5 项修复验证

| # | 问题 | 修复 | 代码验证 |
|---|------|------|---------|
| 1 | WebDAV URL 用 `window.location.origin` | 改用 `api.getApiBase()` | ✅ Settings.jsx |
| 2 | "Board 666" 无效链接 | 已删除 | ✅ Settings.jsx 无此字符串 |
| 3 | Plaza 广播按钮不该存在 | 已移除 | ✅ Plaza.jsx 含 0 处 `广播` |
| 4 | "🌐 P2P 打开" 文案错误 | 改为 "📡 广播" | ✅ AnonExplorer.jsx |
| 5 | `alert()` 弹窗 | 改为内联 toast | ✅ AnonExplorer.jsx 含 0 处 `alert(` |

## archive/TODO.txt 22 项验证（抽查）

| # | 需求 | 代码验证 |
|---|------|---------|
| 1-4 | 左侧栏合集列表/排序/标签/搜索历史 | ✅ AnonCreator.jsx |
| 5 | coll 名不显示 SHA256，显示 "N 个文件" | ✅ `collDisplayName()` |
| 6-8 | Android 式导航 | ✅ `enteredColl` state (16 处) |
| 9-11 | 新建文件夹/拖拽/移动到 | ✅ FileTree.jsx `showMoveModal`/`getAllDirs` |
| 12 | Fork/Commit 按钮移除 | ✅ AnonExplorer.jsx 无按钮 |
| 13-14 | 保存到本机（合集级+文件级） | ✅ `saveCollToNode`/`💾 保存到本机` |
| 15-16 | 选择模式/批量保存 | ✅ `selectMode`/`batchSaveFiles`/`batchSaveColls` |
| 17 | 双击防抖 500ms | ✅ AnonCreator.jsx |
| 18 | 移动端适配 sm 断点 | ✅ FileTree.jsx `hidden sm:inline` |
| 19 | 合集名未设定时 LLM 提示 | ✅ toast 替代 alert |
| 20-22 | 文件浏览三种模式/本地合集判断 | ✅ AnonCreator.jsx 时间线/已注册/本机 三 tab |

---

## 当前 todo.txt 状态

```
1. 完善文档          ✅ CODE-DOC-MAPPING / API-REFERENCE / Swagger
2. 重构 Go 代码      ✅ base + 14 模块分支 merge 14/14
3. 测试管线文档      ✅ TEST-PIPELINE / TESTING-HANDBOOK / 如何测试
4. CI 修复           ✅ Peerdrive CI + Go Build Matrix + React CI 全绿
5. Provider 重构     ✅ path→[providers] sha256/url 双源
6. 前端适配          ✅ api.js / AnonCreator / FileTree
7. 闭环执行          ✅ /loop 持续监控
```

**所有 .txt 需求已完成，活跃文件只剩根目录 `todo.txt`。**
