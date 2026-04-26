# Peerdrive 完整修复状态 & 测试报告

## 测试环境
- **前端**: Cloudflare Pages (peerdrive.pages.dev) — 35 modules, ~320KB JS, ~28KB CSS
- **后端**: Go 1.x + Gin (wsl-3000.moonchan.xyz) — 双分支 frontend/feat-stage2-e2e-test
- **数据库**: SQLite3 (peerdrive.db)
- **编译验证**: React `npm run build` ✅, Go `go build ./...` ✅

---

## ✅ 已完成项

| 来源 | 问题 | 修复方式 |
|------|------|---------|
| 这集神了.txt | 设置按钮位置 | Navbar 精简，⚙ 移到最右侧 |
| 这集神了.txt | P2P 指示灯 | 完全移除 global 连接状态指示器 |
| 这集神了.txt | 文件注册用 prompt() | 浏览文件系统模态框，浏览节点目录选文件 |
| 我要死了.txt | Merge 手动输入 | 改为下拉选择已知合集 + 自定义模式 |
| 我要死了.txt | 右上角用户名 | Navbar 移除用户名输入框 |
| 我要死了.txt | 匿名 collection 编辑按钮 | Fork → "克隆并修改" |
| collection.txt | 匿名 collection 必填 | 全流程匿名优先 |
| commit.txt | Fork/Commit 单文件表单 | 跳转 AnonCreator 工作区完整编辑 |
| commit.txt | Tag 功能 | 匿名+具名合集均支持 Tags []string（Go+React） |
| 文件管理器.txt | 扁平文件表 | 目录树 + 列表视图切换，分类 chips 筛选 |
| 文件管理器.txt | 文件夹自动 collection | 注册目录 → 自动创建匿名合集 |
| 文件管理器.txt | 扁平视图保留 | 列表/树状切换 + 搜索 + 分类 |
| collections.txt | Version/SHA 过大 | 折叠进 <details>，友好名称+日期为主 |
| collections.txt | 点击文件名下载 | AnonExplorer+FileManager 均支持 |
| collections.txt | 目录树模式 | Android 风格逐级导航（面包屑+←返回） |
| collections.txt | "合集"按钮无意义→草稿 | 跳转 AnonCreator draftFrom 模式 |
| UI.txt | 搜索框一级 + Ctrl+K | Navbar 全局搜索 + SearchPanel 下拉 |
| UI.txt | Dummy 测试合集 | Plaza 空状态显示 🧪 示例卡片 |
| UI.txt | VSCode 风格树编辑器 | AnonCreator 左右分栏 + FileTree 拖拽/重命名 |
| UI.txt | 左边文件源 | 本地文件 Android 目录浏览 / 已有合集作为源 |
| UI.txt | select → buttons | AnonCreator 排序/筛选改为 button groups |
| register.txt | 选中目录 → private 合集 | 文件浏览器注册目录自动创建合集 |
| Go cors.txt | CORS origin | 动态回显 Origin header |
| Go 游览文件.txt | /files/browse 301 redirect | RedirectTrailingSlash=false |
| Go 游览文件.txt | Windows 路径 | DefaultRootPath() 自适应平台 |
| Go collections权限 | public/unlisted/private | visibility 字段 + API 完备 |
| - | Mint → 保存 | 中文化按钮文本 |
| - | FileTree inline folder | prompt() → 内联输入框 |
| - | FileTree crash (node.files) | 叶子节点 isDir 判断 |
| - | FileManager folder cascade | 文件夹勾选递归子文件 |
| - | Private collection 旧文件 | mint/commit 后 deleteFile 旧 hash |
| - | Plaza 匿名标签 | 移除"匿名"，合并我的/公开合集 |
| - | AI 名称推荐 | 🤖 AI 按钮调用 LLM 生成合集名称 |
| - | Tags on anon collections | Go AnonCollection model + controller 全链路 |

---

## ⚠️ 遗留 / 远期

| 项目 | 状态 |
|------|------|
| 拖拽从左边到 FileTree | ⚠️ 代码已实现，drag/drop 逻辑完整，需实际浏览器测试 |
| 已有合集展开后逐级浏览 | ⚠️ 点击合集加载全部，非逐级 |
| Explorer VersionLog 说明 | ⚠️ 可加标题"版本历史" |
| P2P/注册中心远端点 | ⚠️ 远期 — 需搭建注册中心 |
| WebRTC 直连 | 远期 |
| STUN/中转/P2P 增强 | 远期 |
| 跨节点 P2P 拉取合集 | 远期 |
| LLM folder naming | 代码已保留接口，待触发方式确定 |

---

## 关键代码路径

| 功能 | 文件 |
|------|------|
| Navbar 搜索 | `components/Navbar.jsx` (SearchPanel inline) |
| AnonCreator 左右分栏 | `pages/AnonCreator.jsx` |
| FileTree 组件 | `components/FileTree.jsx` |
| AnonExplorer Android 浏览 | `pages/AnonExplorer.jsx` |
| Plaza 合集列表 | `pages/Plaza.jsx` |
| FileManager 文件管理 | `pages/FileManager.jsx` |
| Go Tags model | `go/internal/model/anon.go` |
| Go CORS middleware | `go/internal/router/router.go` |
| Go browse endpoint | `go/internal/controller/file.go` |
| Go tags repository | `go/internal/repository/anon_repo.go` |
