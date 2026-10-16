# Peerdrive 现存问题汇总 — 给 Gemini 的上下文

> 2026-04-27 · 用户反复抱怨但未彻底解决的问题

---

## 项目背景

Peerdrive = P2P 文件分享系统。Go 后端 (Gin + SQLite + libp2p) + React 前端 (Vite + TypeScript + Tailwind 暗色主题)。
核心概念：内容寻址存储 (SHA256)、合集版本管理 (类似 Git commit/log/rollback)、P2P 跨节点同步 (Fork/Pull/Merge)。

用户的 .txt 文件散布在 `go/` 和 `react/` 目录下，是用户的测试反馈和抱怨。**用户多次指出 AI 说"改好了"但实际没改**。

---

## 🔴 问题 1：前端三种视图模式（抱怨 4 次）

**来源文件**: react/TODO.txt, react/我的测试结果.txt, react/我的测试结果2.txt, react/UI.txt

**用户要求**: AnonCreator 页面的左边文件源面板要有三种显示方式：
1. 🕐 时间线 — 所有已注册文件按时间排列，标注日期
2. 📁 已注册目录 — 按文件 provider_path 构建目录树
3. 🖥️ 本机目录 — 浏览服务器本机文件系统（不依赖已注册文件）

**当前状态**: 三个按钮已添加，但用户说"目录方式显示不出任何文件,认为是你的逻辑有问题"，且拖拽功能仍不工作。AI 多次声称已修复，用户多次说没修好。

**关键文件**: `react/src/pages/AnonCreator.jsx` (~454行), `react/src/components/FileTree.jsx`

---

## 🔴 问题 2：拖拽文件到 FileTree 无效（抱怨 3 次）

**来源文件**: react/我的测试结果.txt, react/我的测试结果2.txt, react/TODO.txt

**用户要求**: 从左边文件源拖动文件到右边 FileTree 编辑区应该添加条目。拖到文件夹上应添加到该文件夹路径下。

**当前状态**: MIME type `application/peerdrive-file` 在 dragStart 设置，在 onDrop 读取。代码中 onDrop 用了 try/catch 吞掉了错误。浏览器中可能完全不工作——`dataTransfer.getData()` 在非标准 MIME type 下可能返回空字符串。用户说"单纯没有效果"。

**关键文件**: `react/src/components/FileTree.jsx` 的 onDrop handlers, `react/src/pages/AnonCreator.jsx` 的 dragStart

**根因猜测**: 
- `e.dataTransfer.getData('application/peerdrive-file')` 在某些浏览器中不工作
- JSON.parse 失败被 try/catch 静默吞掉
- 或者 drop zone 的 CSS/事件处理有问题

---

## 🔴 问题 3：新建文件夹弹窗（抱怨 2 次）

**来源文件**: react/我的测试结果2.txt (#13, #14)

**用户要求**: 点"+ 新建文件夹" → 直接创建"新建文件夹"条目等待重命名，不弹输入框。

**当前状态**: 代码中 `openNewFolder` 已改为直接调用 `entryActions.onNewFolder('新建文件夹')`。但用户从未确认这个修复有效——他们可能根本没看到这个改动因为 AnonCreator 页面整体有问题（文件不显示、拖拽无效，所以用户无法测试新建文件夹）。

---

## 🔴 问题 4：重复添加同路径文件变成文件夹（抱怨 2 次）

**来源文件**: react/我的测试结果.txt (#12), react/我的测试结果2.txt (#12)

**用户要求**: 同一 path 重复添加时，两个文件并列显示，不要被渲染成文件夹。

**当前状态**: `FileTree.jsx` 的 `buildTree` 函数已添加 `_files.length > 1 && !hasChildren` 的处理分支。但同样——用户无法验证因为页面文件显示功能整体可能不工作。

---

## 🔴 问题 5：注册目录后应直接创建合集（抱怨 3 次）

**来源文件**: react/我的测试结果.txt (#4), react/我的测试结果2.txt (#4), react/register.txt

**用户要求**: 在文件管理器中选中目录注册后 → **直接创建匿名合集**，不要跳转到 AnonCreator 页面。可选添加 tag。

**当前状态**: `FileManager.jsx` 的 `handleRegisterCurrentFolder` 已改为直接调 `api.createAnonCollection`。但 `handleRegisterSelected` 的旧逻辑需要验证。

---

## 🔴 问题 6：合集名显示 SHA256（抱怨 2 次）

**来源文件**: react/TODO.txt, react/collections.txt

**用户要求**: 
- 合集名优先 `friendly_name` → `name_preview`（如"文件a, 文件b 等3个文件"）→ 最后才是 hash
- 不要显示 SHA256 hash
- "版本号写这么大干啥"

**当前状态**: 已改为显示 name_preview，兜底 "N 个文件"。但用户可能没看到效果因为合集创建流程可能有问题。

---

## 🔴 问题 7：已有合集布局问题（抱怨 2 次）

**来源文件**: react/我的测试结果.txt (#10, #11), react/我的测试结果2.txt (#10, #11)

**用户要求**: "← 返回 合集 在同一行" 过于 confusing。有两个列表，布局有误。

**当前状态**: 已改为 "← 合集列表"，加了 breadcrumb 逐级导航。需用户验证。

---

## 🔴 问题 8：Commit 按钮和"克隆并修改"（抱怨 2 次）

**来源文件**: react/TODO.txt, react/commit.txt

**用户要求**: 直接去掉。用户原话："提交,克隆并修改这两个按钮意义不明,请说服我保留,或者,直接去掉。"

**当前状态**: Commit 按钮已从 AnonCreator 移除，仅保留"保存"。但 Explorer 页面还有 Commit 功能（那是给正式合集用的版本提交，应该是合理的）。

---

## 🟡 问题 9：P2P 网络测试从未成功

**来源文件**: go/匿名collection stage2.txt ("PSS：p2p网络可靠吗，从来没测试过，因为我没用过这个包")

**当前状态**: 
- libp2p 节点可以启动，mDNS 可以发现节点
- Exchange 协议代码存在但从未在两个真实节点之间测试过
- DHT 查找理论上工作但未验证
- 分片传输代码已写但从未实际测试
- 没有多节点 e2e 测试环境

**根因**: 用户的开发环境可能没有两个独立节点运行的条件。需要在 `127.0.0.1:3000` 和 `127.0.0.1:3001` 同时启动两个进程测试。

---

## 🟡 问题 10：文件浏览 301 重定向 + Windows 路径

**来源文件**: go/游览文件系统.txt

"/files/browse/ 被 301 重定向到 /files/browse/?path=%2F"
"不一定运行在linux下面,也可能运行在win下面的"

**当前状态**: `router.go` 已设置 `RedirectTrailingSlash = false`。但用户没验证。Windows 路径用了 `filepath.Join`（跨平台安全）。

---

## 关键文件清单

```
react/src/pages/AnonCreator.jsx      — 创建合集页（问题最多）
react/src/components/FileTree.jsx    — 文件树组件（拖拽+显示）
react/src/pages/FileManager.jsx      — 文件管理页
react/src/pages/Explorer.jsx         — 合集详情页
react/src/pages/AnonExplorer.jsx     — 匿名合集浏览
react/src/pages/Plaza.jsx            — 首页合集广场
react/src/pages/Settings.jsx         — 设置页（含 LLM 配置）
react/src/components/Navbar.jsx      — 导航栏
react/src/components/LLMAssistant.jsx — AI 聊天机器人
react/src/api.js                     — 全部 API 调用
go/cmd/server/main.go                — 后端入口
go/internal/router/router.go         — 路由注册
go/internal/service/p2p.go           — P2P 核心
go/internal/service/p2p_connection.go — 连接管理器
go/internal/service/p2p_transfer.go  — 分片传输
go/internal/config/config.go         — 环境变量配置
```

## 用户反馈风格

- 用户用中文测试记录，语气直接
- 用户多次说"你没改"、"你怎么每次都不改就说好了好了"、"请你真的做完了再返回成功结果"
- 用户强调"你必须一个个进行修改和确认"——不要批量合并修改
- 用户不喜欢 select 下拉，用按钮一排
- 用户不喜欢递归展开树，要 Android 平级浏览（breadcrumb + 返回 + 逐级进入）
- 用户不喜欢英文术语，要中文化
- 用户不要显示 SHA256 hash，要可读名称
