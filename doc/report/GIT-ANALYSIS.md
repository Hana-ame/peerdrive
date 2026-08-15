# Git 记录分析报告

> 基于 328 个提交的全量分析，记录开发模式、常见问题与经验教训。最后更新: 2026-04-30。

---

## 1. 开发工作流

### 1.1 Agent 并行开发模式

项目大量使用 "Agent Branch + Merge" 模式：每个子模块在独立分支上开发，由专门的 agent 完成后合并回 main。

| Agent 分支 | 模块 | 合并方式 |
|-----------|------|---------|
| `auth-agent` | 认证 — token/角色/中间件 | merge |
| `bt-agent` | BT DHT — magnet/torrent/做种 | merge |
| `ipfs-agent` | IPFS — Bitswap/CID/网关 | merge |
| `p2p-agent` | P2P — 拓扑/连接质量/交换 | merge |
| `storage-agent` | 存储 — 文件/范围/复制 | merge |

**注意点:**
- Agent 合并后经常出现路由丢失 → 需要 `fix: restore P2PDashboard + BT/Dual/Signal APIs after merge` 这样的补救提交
- Agent 分支的改动范围大，合并前最好跑完整测试套件
- 多个 agent 并发时，注意 `go.mod/go.sum` 冲突风险

### 1.2 双轨分支策略

项目实际存在两条主干：
- **`main`** — Go 后端为主体，前端只有早期的 Code-dropped 快照
- **`origin/frontend`** — React 前端完整演化史（127 个提交，尚未 merge 回 main）

**注意点:**
- 前端分支包含了大量 UI/UX 迭代，与 main 上的后端 API 可能不完全同步
- 两边都有 `react:` / `go:` 前缀的提交，需要仔细对照 API 契约

### 1.3 模块分支策略

早期采用 `module/*` 分支模式（module/auth, module/p2p, module/bt 等 14 个分支），每个模块独立开发后合并。后期转向 agent 模式。

---

## 2. 高频 Bug 模式

### 2.1 Go 后端

| 模式 | 示例 | 预防方法 |
|------|------|---------|
| **goroutine 泄漏** | `fix: IPFSProvider 修复 goroutine 泄漏和超时问题` | 所有 goroutine 必须有 context 取消，defer close channel |
| **接口实现不完整** | `fix: IPFSProvider 实现 ContentProvider 接口` | 用 `var _ Interface = (*Impl)(nil)` 编译期检查 |
| **atomic 复制** | `fix: 移除 atomic.Int32 复制，修复 go vet copylocks` | CI 中加入 `go vet`，禁止复制含 atomic 的结构体 |
| **`.gitignore` 过宽** | `.gitignore server` 太宽导致 `cmd/server/main.go` 丢失 | gitignore 规则尽量具体到目录，用 `/back/server` 而非 `server` |
| **`.gitignore` 误排除源码** | `docs/` 规则排除了 `back/docs/docs.go` stub 包 | 检查 .gitignore 是否误伤了 `**/docs/` 目录下的源码文件 |

### 2.2 React 前端

| 模式 | 示例 | 预防方法 |
|------|------|---------|
| **falsy 0 值** | `fix: collName falsy 0 bug` — 合集名称为 `0` 时被判为 false | 用 `!= null` 或 `??` 代替 `||` |
| **数组 .map 崩溃** | `fix: P2PDashboard peers.map crash` — API 返回结构变化导致 `.peers` 为 undefined | 访问深层属性前做可选链 `data?.peers?.map()` |
| **JSX 嵌套大括号** | `fix: extract entryActions to avoid JSX nested brace parse error` | JSX 中 `{{...}}` 容易解析失败，提取为独立函数 |
| **合并冲突丢失** | `fix: restore P2PDashboard + BT/Dual/Signal APIs after merge` | 合并后 grep 检查所有路由注册是否完整 |
| **回归 bug** | `fix: 修复三列布局4个回归bug` — 重构引入 | 重构前先写测试，重构后跑 Playwright E2E |

### 2.3 CORS 三次迭代

```
1st: 无条件 Access-Control-Allow-Origin: *
2nd: 动态 echo Origin 头
3rd: 修复 CORS preflight，echo Access-Control-Request-Headers
```
**教训：** CORS 不要分步修，一次性处理 Origin/Headers/Methods/预检。

### 2.4 URL 方案反复

anon collection 的 URL 方案经历 4 次变更：
```
/{hash}/*filepath
→ remove /entries/ segment
→ add .json extension (for CF cache)
→ revert .json (兼容性问题)
```
**教训：** URL 方案一旦对外发布就难以更改，设计时需充分讨论。

---

## 3. 架构演变轨迹

### 3.1 目录结构三次大重排

| 阶段 | 结构 | 提交 |
|------|------|------|
| 初期 | 平铺在根目录 | `first commit` |
| 整理 | `docs/` + `test/` + `go/` | `reorg: move docs to docs/ and tests to test/` |
| 当前 | `front/` + `back/` + `doc/` monorepo | `scaffold: 基座架构 — front/ back/ doc/ 模块化目录` |

### 3.2 数据模型演变

| 变更 | 描述 |
|------|------|
| `files` 表拆分 | `files` → `file_meta` + `file_providers` |
| metadata JSON 列 | 文件元数据从固定列改为 JSON，支持扩展 |
| `path→provider` 升级 | 从单路径单提供者 → `path → [providers]` 多源数组 |
| SHA256 + CID 双索引 | 同时支持 SHA256 hash 和 IPFS CID 查找 |

### 3.3 组件拆分节奏

- AnonCreator: 1 个大文件 → 22 个组件文件
- AnonExplorer: 1 个大文件 → 14 个组件文件
- 拆分时机：功能稳定后进行，避免边开发边拆

---

## 4. 文档与反馈循环

### 4.1 TXT 反馈驱动开发

项目使用 `.txt` 文件收集用户反馈，开发者逐条修复后记录在 `reply.md`：
- `complain.txt` → `fix complain.txt issues`
- `TODO.txt` → 标记完成
- `MEMO.md` / `memo-go.md` → 开发备忘录

**优点:** 反馈可追溯，每个投诉都有对应的修复提交
**风险:** `.txt` 文件容易过时，建议定期清理已解决的条目

### 4.2 文档维护成本

- 出现过 "删 42 个冗余文件" 的大清理
- 出现过 8 处失效链接需要修复
- 出现过从 docs 分支恢复 127 个文件的操作
- 建议：每次 API 变更时同步更新对应文档，避免文档债堆积

---

## 5. CI/CD 教训

| 问题 | 解决方案 |
|------|---------|
| BT DHT 在 CI 中不稳定 | CI 环境默认禁用 BT DHT |
| P2P/Relay 测试 CI 不可靠 | 标记为 soft-fail，允许跳过 |
| YAML 缩进问题 | 统一使用 block scalar 格式 |
| 启动探测失败 | 加入 wait-for-startup 循环 |
| 测试目录缺失 | CI 脚本中 `mkdir -p` 创建 |

---

## 6. 提交消息规范

项目实际使用的约定：

```
<type>: <中文或英文描述>

常用 type:
  feat:     新功能
  fix:      修复
  docs:     文档
  test:     测试
  refactor: 重构
  ci:       CI/CD
  chore:    杂项
  go:       Go 后端变更
  react:    前端变更
  merge:    分支合并
  revert:   回退
  scaffold: 脚手架/架构
  spec:     规格说明
```

---

## 7. 关键注意事项清单

1. **Agent 合并后必须验证路由完整性** — 历史上有多次 API 丢失
2. **Go struct 避免包含 `sync/atomic` 值类型** — 复制会导致 go vet 报错
3. **所有 goroutine 必须有生命周期管理** — context 取消 + channel close
4. **前端访问 API 响应前做可选链检查** — `data?.peers?.map()`
5. **`.gitignore` 规则不要用短名称** — `server` 会误伤 `cmd/server/`，用 `/back/server` 明确路径
6. **CORS 一次性配置完整** — Origin/Headers/Methods/预检一起处理
7. **URL 方案设计后不要轻易改** — 兼容性代价高
8. **并发 agent 开发时注意 `go.mod` 冲突** — 合并前协调依赖变更
9. **CI 中 P2P/BT 默认关闭** — 网络相关测试在 CI 环境不稳定
10. **重构前先写测试** — 特别是 UI 组件，避免回归 bug
