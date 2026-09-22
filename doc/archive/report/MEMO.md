# Peerdrv MEMO

> **最后修改**: 2026-04-27 · **版本**: v3.0
> **上一版本**: v2.0 (2026-04-25) — 匿名合集、版本管理、Fork/Pull/Merge
> **本版新增**: 前端 .txt 投诉修复（AnonCreator/FileManager/Explorer）、P2P 连接管理+分片传输、CORS 配置

## 项目结构
- `/go/` — Go 后端 (Gin + SQLite + libp2p)
- `/react/` — React 前端 (Vite + TypeScript)
- `/storage/` — 内容寻址文件存储 (SHA256)

## 核心架构
- 内容寻址存储 (SHA256)
- P2P 同步 (libp2p)
- 版本化合集 (commit/log/rollback/merge)
- Fork 远程 → Pull 上游 → Merge 合并

## 已注册路由 (go/internal/router/router.go)
| 方法 | 路径 | Handler |
|------|------|---------|
| GET | `/ping` | Ping |
| POST | `/upload` | UploadFile |
| GET | `/sha256sum/:sha256` | DownloadBySHA256 |
| POST | `/files/register_local` | RegisterLocal |
| POST | `/files/upload` | Upload |
| POST | `/files/register_folder` | RegisterFolder |
| GET | `/api/v1/collections/:username` | ListCollections |
| GET | `/api/v1/collections/:username/:collection_name` | GetCollection |
| GET | `/api/v1/collections/:username/:collection_name/entries` | ListCollectionEntries |
| POST | `/api/v1/collections/:username/:collection_name/commit` | CommitVersion |
| GET | `/api/v1/collections/:username/:collection_name/log` | GetVersionLog |
| GET | `/api/v1/collections/:username/:collection_name/versions/:version_id` | GetVersion |
| POST | `/api/v1/collections/:username/:collection_name/rollback` | RollbackVersion |
| POST | `/api/v1/collections/:username/:collection_name/entry` | AddEntry |
| DELETE | `/api/v1/collections/:username/:collection_name/entry` | DeleteEntry |
| POST | `/api/v1/collections/:username/:collection_name/pull` | PullFromUpstream |
| POST | `/api/v1/collections/:username/:collection_name/merge` | MergeFromSource |
| POST | `/api/v1/actions/fork` | ForkCollection |
| GET | `/api/v1/tasks/:task_id` | GetTask |
| GET | `/p2p/node` | GetNodeInfo |
| GET | `/p2p/peers` | GetPeers |
| GET | `/p2p/ping/:peer_id` | PingPeer |
| GET | `/swagger/*any` | swagger |

## Service 层
- `CollectionService` — 版本管理 (commit/log/rollback/getVersion)
- `FileService` — 文件上传/本地路径
- `Downloader` — 通过 provider 链下载 (local → HTTP)
- `TransferService` — Fork/Pull/MergeFrom 远程合集
- `MergeService` — 三路合并 + 冲突检测
- `P2PService` — libp2p 节点信息/peers/ping
- `GCService` — 垃圾回收 (无 HTTP 端点)

## 本会话已完成 (2026-04-27)
- 删除 go/ref.md, react/ref.md (旧开发草稿)
- 删除 go/docs/ 中 5 个过时文档
- 重写 api.md, services.md
- 修复 design.md 乱码
- 创建 testing.md, ROADMAP.md, technical_report.md
- 恢复 test.sh
- 内联 pkg/hashutil, 删除 pkg/
- 更新根 README.md
- **修复 MergeFromSource handler** — 之前忽略请求体中的 source 参数
- **添加 DELETE /entry** — 删除合集条目
- **添加 GET /collections/:username** — 列出用户合集
- **接入 DownloadBySHA256** — 使用完整的 provider 链下载

## 2026-04-27: .txt 投诉修复 + P2P 增强
### 前端修复 (AnonCreator)
- **移除 "Commit" 按钮** — 仅保留"保存"，去除版本提交概念
- **修复合集名显示** — 不显示 SHA256 hash，优先 name_preview，兜底显示 "N 个文件"
- **合集列表增强** — 添加排序（时间/名称/文件数）和标签过滤
- **合集文件浏览** — breadcrumb 逐级导航，支持进入/返回目录
- **新建文件夹** — 直接创建"新建文件夹"等待重命名，不弹输入框

### 前端修复 (FileManager)
- **复选框增强** — 更大复选框 (w-5 h-5)，选中行蓝色高亮
- **布局加大** — 列表项增大，更宽敞
- **注册即创建合集** — 选中文件注册后直接创建匿名合集，不跳转
- **点击行切换选中** — 整行可点击切换复选框

### 前端修复 (Explorer)
- **文件夹浏览** — breadcrumb 逐级导航，替代平铺表格
- **中文化** — Commit→提交, Merge→合并
- **优化显示** — 不重复显示 hash，仅显示截断版本

### 后端修复
- **CORS 配置** — 默认允许 localhost:5173, peerdrive.moonchan.xyz, peerdrive.pages.dev
- **公共域名配置** — PEERDRIVE_PUBLIC_DOMAIN 环境变量
- **修复 model 测试** — NewAnonCollection 签名更新（tags 参数）

### P2P 增强
- **连接管理器** (p2p_connection.go) — 自动连接、30s 心跳、断线重连、连接统计
- **分片传输** (p2p_transfer.go) — 256KB chunk、8 并发、多源并行、进度回调、SHA256 验证
- **Exchange 协议增强** — 支持 SIZE 命令查询文件大小
- **P2P 状态 API** — 返回连接管理统计和活跃传输任务
- **前端 P2P 仪表板** — 显示连接统计、活跃传输进度条
- **P2P 文档** (go/docs/specs/p2p.md) — 完整协议栈/环境变量/运行模式文档
- **P2P 测试** (p2p_test.go, p2p_transfer.sh) — 7 个单元测试 + E2E 测试脚本

## 已知问题
- 无认证/鉴权 (已推迟)
- 无 Pin/Unpin (已计划未实现)
- GCService 无 HTTP 端点
- Swagger 文档不完整
- `service/collection.go:122` 的 `GetCollectionByID()` 是桩函数

## AI 理解错误记录（必须避免）

| # | 错误理解 | 正确理解 | 来源 |
|---|---------|---------|------|
| 1 | 在 AnonExplorer 做递归目录树 | 用户要的是 Android 风格逐级导航（点文件夹→进入→显示内容→返回），不是展开的树 | collections.txt |
| 2 | "合集在上，文件在下"指布局 | 用户从未说过这句话，是我自己产生的错误理解 | TODO.txt |
| 3 | Commit/Fork 按钮保留在页面上 | 用户觉得意义不明，应该直接去掉 | TODO.txt |
| 4 | 合集名称 fallback 用 SHA 前缀 | 应显示文件a, 文件b 等N个文件 (Go name_preview field) | TODO.txt |
| 5 | 文集条目按 path 去重 | 用户可以重复添加同路径文件，去重是 SHA256 文件层的事 | addEntry |
| 6 | LLM endpoint 用 wsl-3000.moonchan.xyz | 应用 siliconflow.moonchan.xyz 代理，通过 api.getLlmEndpoint() 获取 | TODO.txt |
| 7 | `nav.replace(path, state)` 是有效 API | React Router v6 的 useNavigate() 没有 .replace 方法，要用 nav(path, { replace: true }) | AnonCreator crash |
| 8 | useLocation().state 默认值 `= {}` 覆盖 null | 解构默认值不处理 null，要用 `|| {}` | AnonCreator crash |
| 9 | 用户想保留"提交/克隆并修改" | 用户明确说去掉了，不要"说服" | TODO.txt |
| 10 | 合集(collection)是文件夹概念 | 合集中文件路径可嵌套，但集合本身不是文件夹，是文件引用集合 | 整体理解 |
| 11 | 文件浏览器用递归树展示 | 用户要求 Android 平级浏览：进入文件夹→看内容→返回→退出。不做展开树 | 多次 |
| 12 | 已有重名文件直接替换 | 用户允许同一个collection里存在多个同名不同hash文件 | TODO.txt |
| 13 | select 下拉选单可以用 | 用户要求用按钮做一排，不要 select。除选项非常多的情况外 | 多次 |
| 14 | 合集名可为空直接保存 | 用户要求未设名时弹窗，让用户选择 LLM 推荐或留空 | TODO.txt |
| 15 | collection就是不可变的 | 用户在 AnonCreator 编辑的就是 mutable 草稿, save 才变成 immutable | 设计理解 |
| 16 | AnonExplorer 是文件浏览器 | 用户眼中的合集是"文件夹"，点进去就是文件夹里的文件 | 多次冲突 |

## 前端设计要点

- **不做递归树**，做 Android 文件管理器的平级浏览（breadcrumb + 后退 + 逐级进入）
- **不用英文术语**，全部中文化（Mint→保存，Commit→提交）
- **不做 select 下拉**，用按钮一排
- **合集名字**优先显示 name_preview（文件名1, 文件名2 等N个文件），其次 friendly_name，最后才是 hash
- **drag 用 custom MIME type** `application/peerdrive-file`，需要确保 `e.dataTransfer.getData()` 在 drop 事件可读
- **LLM 用 siliconflow.moonchan.xyz 代理，不要直连其他 endpoint**
- **合集不是文件夹**，文件路径有层级但集合本体就是一组 path→hash 映射
