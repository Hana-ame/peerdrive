# Peerdrive 问题反馈与修复回复

## 这集神了.txt

| 问题 | 状态 |
|------|------|
| 设置按钮不该放菜单中间 | ✅ 已修复 — Navbar 精简，⚙ 移到最右侧 |
| P2P 指示灯意义不明 | ✅ 已修复 — 移除 Navbar P2P 状态点 |
| 文件注册用 prompt() 输入路径 | ✅ 已修复 — 新增"浏览文件系统"模态框 |
| Webapp 是纯前端，依赖 endpoint 设置 | ✅ Settings 页支持修改 endpoint |
| 一些链接点开后新页面了 | ⚠️ Explorer 下载用 target="_blank" 是有意的 |

## 我要死了.txt

| 问题 | 状态 |
|------|------|
| Merge 自己填用户名/合集名 | ✅ 已修复 — Merge 弹窗改为下拉选择已知合集 |
| Collection 界面应列出自己创建 + 远端收集 | ✅ 已修复 — Plaza 三 Tab：我的合集/公开合集/搜索 |
| 下拉选单不如选项按钮直观 | ✅ 已改 Tab/按钮，部分保留 select（选项多时更紧凑） |
| 右上角不要填用户名 | ✅ 已修复 — Navbar 移除用户名输入框 |
| 匿名是默认，不应该必填 | ✅ 已修复 — 匿名优先 |
| 匿名 collection 编辑按钮 | ✅ 已修复 — Fork → "克隆并修改" |
| 首屏从注册服务器/p2p 发现 | ✅ 公开合集 Tab 从 registry 拉取 |

## 我服了.txt

| 问题 | 状态 |
|------|------|
| 创建时自己填 hash | ⚠️ "按 hash 打开"仍保留为高级功能 |
| 文件管理 → 合集管理 → 分享层级 | ✅ 浏览+注册+自动创建合集一体化 |
| 操作习惯应像网盘/资源管理器 | ⚠️ 有目录树，待加右键操作 |

## collection.txt

| 问题 | 状态 |
|------|------|
| 匿名 collection 怎么又要必填了 | ✅ 已修复 — 全流程匿名优先 |

## 文件管理器.txt

| 问题 | 状态 |
|------|------|
| 文件管理器是扁平的 | ✅ 已修复 — 按目录结构组织的树状视图 |
| 添加文件夹自动按目录结构组织 collection | ✅ 已修复 |
| 同时保留扁平视图（时间排序+分类筛选+搜索） | ⚠️ 待做 — 需加 tab 切换 |

## 合集界面.txt

| 问题 | 状态 |
|------|------|
| 右边 VersionLog 是什么 | ⚠️ 是 Git 风格版本历史，UI 待加标题说明 |
| 创建合集时左边也能从其他合集选文件 | ⚠️ 待做 — 需展开历史合集 + 复选框选择 |
| CORS /files/browse 失败 | ✅ 已修复 — 动态回显 preflight 请求头 |

## 创建合集.txt

| 问题 | 状态 |
|------|------|
| 历史合集点了把所有文件怼过来 | ⚠️ 待做 — 需展开+复选框+按钮模式 |
| "如果不会改就问" | 逐项处理中 |

## commit.txt

| 问题 | 状态 |
|------|------|
| Fork/Commit 表单只能改一个文件 | ✅ 已修复 — 跳转 AnonCreator 工作区 |
| 应该新建内存中可编辑 collection | ✅ 已修复 — 同上 |
| tag 功能没做 | ✅ 已修复 — collections 加 tags 列（JSON array） |

## collections.txt

| 问题 | 状态 |
|------|------|
| Version 1 写太大，不应给用户看 | ⚠️ 待做 — 应显示名称/日期等 |
| SHA 重复显示 | ⚠️ 待做 — AnonExplorer 布局优化 |
| 点文件名应能下载 | ⚠️ 待做 — AnonExplorer 缺下载链接 |
| 目录树模式（d/f1,d/f2 → d/ 文件夹） | ⚠️ 待做 — 需前后端支持层级展开 |
| "合集"按钮意义不明 | ⚠️ 待做 — 点后跳 AnonCreator 创建草稿，确认后提交 |
| 目录树按需进入文件夹再 list | ⚠️ 待做 — Go 后端需按目录过滤 |
| Plaza 探索合集空白区链接注册中心/P2P | ⚠️ 待做 — 需注册中心 API 或 P2P 发现 |
| hash 和搜索放同一输入框 | ⚠️ 待做 — 智能识别 |
| 创建合集没改，txt 被挪走 | ⚠️ Navbar 有 /anon/create 链接，需确认 |

## Go: cors.txt

| 问题 | 状态 |
|------|------|
| 后端需配置允许的 origin 白名单 | ⚠️ 待做 — 当前放开所有 origin，需改为可配置（peerdrive.moonchan.xyz, peerdrive.pages.dev, localhost） |

## Go: 游览文件系统.txt

| 问题 | 状态 |
|------|------|
| /files/browse/ → 301 redirect | ⚠️ 待做 — Gin trailing slash 自动纠正，需修复路由 |
| 仅用 / 作根不尊重 Windows | ⚠️ 待做 — browse 需适配不同 OS |

## Go: 正式collection.txt

| 问题 | 状态 |
|------|------|
| 正式 collection /:username/:collection | ✅ 已实现 |
| fork/clone/merge 如 Git | ✅ actions/fork merge pull 端点 |
| collection P2P 分发设置 | ⚠️ 远期 — 需注册中心 |
| 中心化鉴权 | ⚠️ 远期 — 需搭建注册中心 |

## Go: 需要设置的内容.txt

| 问题 | 状态 |
|------|------|
| public access domain 可配置 | ⚠️ 待做 — config 加 public_domain 字段 |
| key 自动生成，#key URL 参数 | ⚠️ 待做 — 依赖注册中心 |

## Go: collections权限.txt

| 问题 | 状态 |
|------|------|
| public/unlisted/private 三种可见性 | ✅ 已实现 — visibility 字段 + API |

## Go: 远期目标（待规划）

| 文件 | 内容 | 优先级 |
|------|------|--------|
| 增强p2p.txt | STUN 打洞、中转模式 | 远期 |
| 远期目标：webRTC.txt | 无节点直连 WebRTC | 远期 |
| 匿名collection stage2.txt | 跨节点 P2P 拉取合集 | 远期 |

---

## LLM 助手技术细节

### 模型调用
使用自建 LLM 代理 `siliconflow.moonchan.xyz`，转发到 SiliconFlow API，代理层自动注入 API Key。

### 上下文获取
LLM 通过 `PageContext` 获得当前页面信息，各页面 push 自己的上下文 JSON。

### Function Calling
17 个 tool（navigate_to, collection CRUD, file ops, P2P status 等），多轮 tool-calling 最多 5 轮，本地 accMessages 数组防闭包 stale state。

### 流式输出
SSE 读取，逐行解析 `data: {...}` JSON，累积 delta.content 实时渲染，delta.tool_calls 按 index 增量拼接后统一执行。

### 免费模型
13 个 SiliconFlow 免费模型，Settings 页下拉选择 + 自定义输入。
