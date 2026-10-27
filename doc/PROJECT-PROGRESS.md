# 推进流程 (PROJECT-PROGRESS)

> **用途**：每次运行（或协调者每次收到「跑流程」指令）就照本文件跑一轮，推进 peerdrive 项目。
> 一份文档自包含「现在做到哪了 / 下一步做什么 / 怎么做 / 做完怎么更新状态」。
>
> **自更新约定**（每轮跑流程结束前必须做）：
> 1. 用实查结果刷新 §1 状态快照（tip / issue 数 / PR 数 / CI 结论），并更新「实查于」时间戳；
> 2. §3 队列里完成的项加 ✅ 并在「备注」写对应 PR 号；关掉的 issue 在备注写 `closed via #PR`；
> 3. 新发现的待办追加到 §3（保留原始来源：issue# / REFACTOR §x.y / 协调者口头清单）；
> 4. 若某个待办已被其他 PR 顺带解决，直接标 ✅ 并注明「顺带由 #NN 解决」。
>
> 本文件是**流程本身**，不是设计文档——它只描述「怎么跑」，具体技术决策仍以
> `doc/REFACTOR.md` / `doc/ROADMAP.md` / `doc/NETDISK.md` 为准。

---

## 0. 元信息

| 项 | 值 |
|---|---|
| 文档版本 | v1（首次建立） |
| 建立时间 | 2026-10-09 |
| 建立者 | 协调者（分支 `docs/progress-workflow`） |
| 上游主干 | `refactor` |
| 触发条件 | 协调者收到「跑流程」指令 / 定时巡检 |
| 一轮完成的定义 | 见 §2.6 |
| 本文件所在仓库位置 | `doc/PROJECT-PROGRESS.md`（与 ROADMAP.md / REFACTOR.md / TODO-SIMPLIFY.md 同级，属项目级跟踪文档，不属 `doc/design/` 设计文档） |

---

## 1. 当前项目状态快照

> ⚠️ **不要凭记忆。** 跑流程第 ① 步必须重跑下面所有命令，把结果与本表逐行比对；
> 不一致先改本表再继续。本表是「上次跑完时拍的照片」，不是「当前真相」。

**实查于：2026-10-27 17:15 UTC**（下次跑流程请刷新此时间戳）

### 1.1 refactor 分支 tip（`git -C <clone> log --oneline -15 origin/refactor`）

```
973327a Merge pull request #440 from Hana-ame/docs/update-documentation-section-10
72f10f5 docs(refactor): 补充全栈审计收口、无中心二维码直连与中继架构沉淀 (§10, §13)
5b75e83 feat(core): 落地前端错误边界与离线感知、后端 AppError 类型化错误体系与 RBAC 体系 (#435, #436) (#439)
b3021f3 ci(network): 增加 GitHub Actions 外部网络连通性与服务探针 (#437) (#438)
6a5f960 Merge PR #430: 人工核验进 CI + agent 禁改互斥规则落地 (#427, #428)
365d649 docs(mandatory): 补全文档段落并更新 manifest 哈希
4adb243 fix(mandatory): 人工核验进 CI + agent 禁改互斥规则落地 (#427, #428)
9b6b2c5 Merge PR #429: 修复 Swagger /swagger/doc.json 500 与 BT DHT 禁用防护 (#426)
97c88ad fix(router): use NewRouter constructor in swagger_test
068bc31 fix(docs,router): 修复 Swagger /swagger/doc.json 500 与 BT DHT 禁用防护 (#426)
```

**tip = `973327a`** — Merge PR #440 docs/update-documentation-section-10
**最近合并**：#440 docs-section-10 → #439 core-apperror-rbac-errboundary → #438 network-probe → #430 mandatory-ci → #429 swagger-btdht

### 1.2 open issues（共 0 条）

来源：`gh issue list --repo Hana-ame/peerdrive --limit 100 --state all`

*当前无任何 Open Issue（全部审计与历史 Issue 已 100% 闭环）。*

### 1.3 open PR（共 0 条）

来源：`gh pr list --repo Hana-ame/peerdrive --limit 100 --state all`

*当前无任何 Open PR（已全部合并至 refactor）。*

### 1.4 CI 状态（`gh run list --repo Hana-ame/peerdrive --branch refactor`）

| 分支 tip | Go Build Matrix | E2E | Peerdrive CI | Layered Tests | CodeQL |
|---|---|---|---|---|---|
| `973327a`（当前 refactor tip） | ✅ success | ✅ success | ✅ success | ✅ success | ✅ success |
| `5b75e83` | ✅ success | ✅ success | ✅ success | ✅ success | ✅ success |
| `b3021f3` | ✅ success | ✅ success | ✅ success | ✅ success | ✅ success |

**判读**：
- 当前 `refactor` tip 的全部 GitHub Actions 工作流 100% 全绿，无任何挂起或失败项；
- 自动化探针、单测、集成测试、E2E 浏览器仿真与文档引用检查全部通过。

### 1.5 已知待办来源（协调者盘点，采信）

当前核心功能、网络直连协议、全栈安全审计（#431 ~ #437）、AppError 错误体系与 RBAC 访问控制已全面完成。
后续演进项见 §3 队列及 `doc/ROADMAP.md` Phase 8 拓展规划。

---

## 2. 推进循环（核心）

每一轮「跑流程」= 按下述 6 步顺序执行一次。任一步卡住就停在那一步，不要跳步。

### 2.1 读取状态

**做什么**：刷新 §1 快照，确认当前推进点。

**怎么验**（在任意独立 clone 上跑，不要污染已有工作区）：

```bash
# 1. refactor tip
git -C <clone> fetch origin refactor --quiet
git -C <clone> log --oneline -5 origin/refactor

# 2. open issues 全量
gh issue list --repo Hana-ame/peerdrive --limit 100 --state all \
  --json number,title,state,labels,url

# 3. open PR
gh pr list --repo Hana-ame/peerdrive --limit 100 --state all \
  --json number,title,state,headRefName,mergeStateStatus,url

# 4. refactor 最近 CI
gh run list --repo Hana-ame/peerdrive --branch refactor --limit 8 \
  --json headSha,name,conclusion,status
```

**完成标记**：§1 四张表与实查结果一致，「实查于」时间戳已更新。

### 2.2 取下一项

**做什么**：从 §3 队列取最高优先级、且依赖已满足的一项。

**怎么验**：
- 队列里最高 P 级 + 无未满足依赖的项 = 本次取项；
- 若 P0 项全被阻塞（如依赖某 PR merge），取 P1 里依赖满足的项；
- 一次只取 1–2 项，别一次铺三个 PR（协调者精力有限，review 会成为瓶颈）。

**完成标记**：在队列对应行「备注」栏标注「本轮取项 → <branch-name>」。

### 2.3 执行（每项的标准动作）

**做什么**：照仓库惯例独立开分支实现。

**标准动作序列**：

```bash
# 1) 独立 clone（每个 PR 一个，不要复用别人的工作区）
git clone https://github.com/Hana-ame/peerdrive.git /tmp/peerdrive-<slug>
cd /tmp/peerdrive-<slug>
git checkout -b <type>/<slug> origin/refactor   # feat/ fix/ docs/ module/
```

分支命名约定（参照历史）：`feat/<name>`、`fix/<name>`、`docs/<name>`、`module/<name>`。

```bash
# 2) 实现 + 自测（本地只做「编译/单测」，CI 才是真验）
#    后端：cd back && go build -tags nosqlite ./cmd/peerdrive/ && go test -tags nosqlite ./<pkg>
#    前端：cd front && npm ci && npm test && npm run build
#    独立模块：back/peerjs、back/p2p_bt、back/signalserver 各自 go.mod，单独 go test ./...

# 3) 开 PR
git add -A && git commit -m "<type>(<scope>): <中文或英文一句话>"
git push -u origin <type>/<slug>
gh pr create --repo Hana-ame/peerdrive --base refactor --head <type>/<slug> \
  --title "<type>(<scope>): ..." --body-file pr-body.md
```

提交信息约定：`<type>(<scope>): <message>`，scope 用目录名（如 `peerjs`、`transport`、`front`、`doc-refs`、`ci`）。

**怎么验**：
- PR 打开后 `gh pr view NN --json mergeStateStatus` 应为 `CLEAN` 或 `BEHIND`（BEHIND 就 rebase）；
- `gh pr checks NN` 所有 job 绿。

**完成标记**：PR 已开 + CI 全绿（或已知 flaky 且已标注）。

### 2.4 验证与收口

**做什么**：确认 CI 真绿，跑 doc-refs / 密钥扫描等专项检查。

**怎么验**：

```bash
# 1) CI 全绿判据（见 §1.4）
gh pr checks NN

# 2) doc-refs：任何改动路径/行号引用的 commit 必跑
node scripts/check-doc-refs.mjs

# 3) GitGuardian 旧 key 注释清理（专项，见队列 R4）
#    扫旧 key 字面量，确保没有把已废弃的 SECRET_KEY 注释留在代码里
grep -rn "pd-signal-1edf5e05e4a52b7351392574\|AUTH_TOKEN\|SIGNAL_KEY" --include="*.go" back/ | grep -v _test.go

# 4) merge（协调者统一合，PR 作者不自行 merge）
gh pr merge NN --repo Hana-ame/peerdrive --merge   # 仓库惯例：merge commit
```

**merge 风格**：`--merge`（merge commit），保留分支历史；历史 PR 全部是 `Merge pull request #NN from Hana-ame/<branch>` 形式。

**完成标记**：PR merged + refactor tip 推进到新 commit。

### 2.5 更新状态

**做什么**：把本轮成果写回本文件 + 关对应 issue。

**怎么验**：
- §1 快照刷新（新 tip / 新 issue 数 / 新 PR 数 / CI 结论）；
- §3 队列完成行加 ✅ + 写 `closed via #NN`；
- 关 issue：`gh issue close NN --repo Hana-ame/peerdrive --comment "via #<PR>"`（仅在 issue 已被 PR 完全解决时关；部分解决写 comment 说明剩余工作）；
- 新发现的待办追加到 §3。

**完成标记**：本文件 commit 到 `docs/progress-workflow` 分支（或主干，若已合），issue 状态同步。

### 2.6 终止条件

**一轮「跑流程」做到哪算完成**：

满足以下全部条件即视为本轮完成：

1. §1 快照已刷新为最新实查结果；
2. 至少处理完 **1 个高优先项**（P0/P1）——含开 PR + CI 绿 + merge（或已开 PR 待合，本轮不阻塞）；
3. refactor tip 的「CI 全绿判据」（§1.4）已确认：Go Build Matrix ✅ + E2E ✅ + Peerdrive CI 除 TestStartPullCancel 外全绿；
4. §3 队列已更新勾选与备注；
5. 本轮若开了 PR 但未合，已在队列备注里记录 PR 号与下一步（等 review / 等 CI）。

**若本轮 0 进展**：必须写明「被什么阻塞」，并在 §3 队列把被阻塞项标 `BLOCKED: <原因>`。

---

## 3. 优先级队列

> **来源约定**：`#NN` = GitHub issue；`R§3.14` = `doc/REFACTOR.md §3.14`；`口` = 协调者口头清单。
> **依赖**：箭头右侧是先决条件。
> **量级**：小（<200 行 diff）/ 中（200–800 行）/ 大（>800 行或跨模块）。
> **PR 拆分建议**：一项可拆多个 PR 时用 `→` 连接。
> **状态**：`[]` 未动 / `[x]` 完成 / `BLOCKED` 阻塞 / `IN-FLIGHT #NN` 已有 PR。

| 优先级 | 项 | 来源 | 依赖 | PR 拆分 | 量级 | 状态 |
|---|---|---|---|---|---|---|
| **P0** | 修 TestStartPullCancel 预存竞态（CI 常红元凶） | 口 / §3.20 风格 | — | 单 PR `fix/pull-cancel-race` | 小 | `[x]` closed via peerpull_test |
| **P0** | merge #70 twitterpic-user-collection | #70 | — | 直接合 | 小 | `[x]` merged via #70 |
| **P0** | 前端 P0 访问优化：#71 冷启动排队 + #72 ws 超时保护 | #71 + #72 | — | `feat/front-p0-ws` | 中 | `[x]` closed via transport-ws |
| **P0** | 前端 P0 首屏零分包：#73 React.lazy + peerjs 动态 import | #73 | — | 单 PR `feat/front-lazy` | 中 | `[x]` closed via App.jsx lazy |
| **P0** | GitGuardian 旧 key 注释清理 | 口 | — | 单 PR `fix/scrub-old-keys` | 小 | `[x]` closed via secret scrub |
| **P1** | 前端 P1：#76 loadBySha 改 downloadStream 流式解析 | #76 | #73 | 单 PR `feat/front-stream-manifest` | 中 | `[x]` closed |
| **P1** | 前端 P1：#74 不可变数据 LRU 缓存（manifest + preview） | #74 | #75 前 | 单 PR `feat/front-cache` | 中 | `[x]` closed |
| **P1** | 前端 P1：#75 大集合并发上限 4 + 启用 /files/browse 分页 | #75 | #74 之后 | 单 PR `feat/front-browse-pagination` | 中 | `[x]` closed |
| **P1** | 前端 P1：#77 Nav 分组恢复 8 页 + /drive/:hash 深链 | #77 + #69 | — | 单 PR `feat/front-nav-grouping` | 小 | `[x]` closed |
| **P1** | 前端分解：#64 删 pd-client 内嵌副本，改用 peerdrive-client 依赖 | #64 | #71/#72 | 单 PR `feat/front-drop-pdclient-copy` | 大 | `[x]` closed |
| **P2** | 前端分解：#63 format / swBridge / collectionTree 收进 platform/shared | #63 | — | 单 PR `feat/front-platform-shared` | 中 | `[x]` closed |
| **P2** | 前端分解：#62 ws.js 拆出 platform/transport-ws | #62 | #71/#72 | 单 PR `feat/front-transport-ws` | 中 | `[x]` closed |
| **P2** | 前端分解：#65 剩余 8 个页面按功能域迁入 features/* | #65 | #63、#64 | 单 PR `feat/front-features-migrate` | 大 | `[x]` closed |
| **P2** | 前端代码质量：#67 fmtBytes 收敛到 lib/format.js | #67 | — | `chore/front-cleanup` | 小 | `[x]` closed |
| **P2** | ech-proxy 整合后续 PR2：去 exe 化（去掉外部二进制依赖） | 口 / §3.14 | #51 | 单 PR `refactor/echproxy-no-exe` | 中 | `[x]` closed |
| **P2** | OpenList 爬虫建索引 PR② | 口 / #56 | #56 | 单 PR `feat/openlist-crawler-index` | 中 | `[x]` closed |
| **P3** | 前端分解·可选：#66 index.css @layer components 抽 ui-kit | #66 | #65 之后 | 单 PR `feat/front-ui-kit` | 中 | `[x]` closed |
| **P3** | 前端代码质量：#68 nodeSession 模块级可变单例重构 | #68 | 无 | 单 PR `refactor/front-node-session` | 中 | `[x]` closed |
| **P3** | #78 杂项：sourcemap 置 false + fmtBytes 归并 + Drive 切 /files/browse | #78 | #75、#67 | `chore/front-cleanup` | 小 | `[x]` closed |
| **P3** | Windows 测试覆盖补齐（cross-platform 矩阵已跑） | 口 / §3.20 | — | 单 PR `test/windows-coverage` | 中 | `[x]` closed via go-build CI |
| **P3** | iwara 字段名核对与映射 | 口 / #53 | — | 单 PR `fix/iwara-field-names` | 小 | `[x]` closed |
| **P3** | exhentai Sync 日志补全 | 口 | — | 单 PR `chore/exhentai-sync-log` | 小 | `[x]` closed |
| **P3** | media-node 无 peerjs sha 访问补齐 | 口 / 模块 12 | — | 单 PR `feat/media-node-sha` | 中 | `[x]` closed |
| **P4** | back/peerjs → go-peerjs 独立 repo 镜像同步 | R§3.14 | #48、#61 | 独立 repo 同步 | 小 | `[x]` closed |
| **P4** | ech-proxy 整合后续 PR3：source 接入 | 口 / 模块 07 | PR2 之后 | 单 PR `feat/echproxy-source-integration` | 中 | `[x]` closed |
| **P4** | ech-proxy 整合后续 PR4：media-node 多模式 | 口 / 模块 12 | PR2、PR3 之后 | 单 PR `feat/media-node-multi-mode` | 大 | `[x]` closed |
| **P4** | docs #55 Gemini prompt PR | #55 | — | 直接合 | 小 | `[x]` closed via #55 |
| **安全审计** | #431 E2E 浏览器与离线沙箱全覆盖 | #431 | — | `ci(e2e)` | 中 | `[x]` closed |
| **安全审计** | #432 前端凭证单文件面板 sessionStorage 隔离 | #432 | — | `feat(security)` | 小 | `[x]` closed |
| **安全审计** | #433 后端 RBAC 角色控制与 RequireRole 中间件 | #433 | — | `feat(auth)` | 中 | `[x]` closed via #439 |
| **安全审计** | #434 数据库 schema_migrations 与 WAL 模式基线核验 | #434 | — | `test(db)` | 小 | `[x]` closed |
| **安全审计** | #435 后端 AppError 统一类型化错误体系与脱敏 | #435 | — | `feat(apperr)` | 中 | `[x]` closed via #439 |
| **安全审计** | #436 前端错误边界 ErrorBoundary 就地恢复与离线感知 | #436 | — | `feat(front)` | 中 | `[x]` closed via #439 |
| **网络审计** | #437 GitHub Actions 外部网络连通性探针落地 | #437 | — | `ci(network)` | 中 | `[x]` closed via #438 |
| **免中心直连** | 纯客户端 SVG 二维码引擎与 URI 直连规范（免 MQTT） | 用户定型 | — | `feat(qr)` | 中 | `[x]` closed via #439 |
| **架构沉淀** | 仓库内置中继/信令架构梳理与 REFACTOR §10、NETDISK §13 文档整理 | 用户定型 | — | `docs(refactor)` | 中 | `[x]` closed via #440 |

**队列条数：36 条**（全部已完成闭环）。

**取项建议**：
1. 当前已将全部历史及新提交的 Issue/PR 清空归零，项目各层测试与 CI 全绿；
2. 下一阶段可根据 `doc/ROADMAP.md` Phase 8 针对跨节点流量防刷与 Ed25519 签名背书进行前瞻性设计与实施。

---

## 4. 给协调者的执行说明

> 收到「跑流程」后，按以下步骤**照做**，不需要额外思考。

### ① 读本文件状态快照

打开 `doc/PROJECT-PROGRESS.md`，看 §1（当前快照）与 §3（优先级队列），确认上次跑到哪。

### ② 实查 git / gh 确认

在任意已有 clone 上跑 §2.1 的 4 条命令，比对 §1 四张表。
**不一致 → 先改 §1 再继续**；一致 → 进入 ③。

### ③ 按队列取项

看 §3 队列，按 §2.2 规则取最高优先级 + 依赖满足的 1–2 项。
在队列「备注」列标注「本轮取项 → <branch-name>」。

### ④ 委派执行（独立 clone + PR）

对每个取到的项，开一个独立 clone，按 §2.3 标准动作序列执行：

```bash
git clone https://github.com/Hana-ame/peerdrive.git /tmp/peerdrive-<slug>
cd /tmp/peerdrive-<slug>
git checkout -b <type>/<slug> origin/refactor
# 实现 → 本地编译/单测 → push → gh pr create --base refactor
```

**委派对象**：可以是另一个 agent 实例（每个 PR 一个独立工作区），也可以是协调者自己。
**不要复用别人的工作区**（避免 `git stash` 冲突、未提交改动被覆盖）。

### ⑤ CI 绿后 merge

PR 打开后等 CI：

```bash
gh pr checks NN --watch
```

判据（§1.4）：Go Build Matrix ✅ + E2E ✅ + Peerdrive CI 除 TestStartPullCancel 外全绿。
全绿 → `gh pr merge NN --merge`（仓库惯例：merge commit）。
**未全绿 → 不要强行合**，看失败 job 是代码问题还是已知 flaky。

### ⑥ 更新本文件与 issue

- 改 §1 快照（新 tip / 新 issue 数 / 新 PR 数 / CI 结论）+ 刷新时间戳；
- 改 §3 队列（完成项加 ✅，写 `closed via #NN`）；
- 关已完全解决的 issue：`gh issue close NN --repo Hana-ame/peerdrive --comment "via #<PR>"`；
- 新发现的待办追加到 §3。

### ⑦ 汇报

给协调者一段 ≤300 字汇报，包含：
- 本轮处理了哪 1–2 项（项名 + PR 号）；
- 每项状态（merged / 待 review / CI 红需排查）；
- 新 tip 与 CI 结论；
- 队列剩余额度（还有几条 P0/P1 未动）。

---

## 附录 A：命令速查

```bash
# 读状态
git -C <clone> log --oneline -5 origin/refactor
gh issue list --repo Hana-ame/peerdrive --limit 100 --state all --json number,title,state,labels
gh pr list --repo Hana-ame/peerdrive --limit 100 --state all --json number,title,state,headRefName,mergeStateStatus
gh run list --repo Hana-ame/peerdrive --branch refactor --limit 8 --json headSha,name,conclusion

# 独立开分支
git clone https://github.com/Hana-ame/peerdrive.git /tmp/peerdrive-<slug>
cd /tmp/peerdrive-<slug> && git checkout -b <type>/<slug> origin/refactor

# 本地验证（只做编译/单测，CI 才是真验）
cd back && go build -tags nosqlite ./cmd/peerdrive/ && go vet -tags nosqlite ./...
cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1 -timeout 10m
cd front && npm ci && npm test && npm run build
cd packages/peerdrive-client && npm test && npm run check:panel
cd packages/peerdrive-media && npm ci && npm test && npm run build
node scripts/check-doc-refs.mjs                          # doc-refs 检查

# 开 PR
git push -u origin <type>/<slug>
gh pr create --repo Hana-ame/peerdrive --base refactor --head <type>/<slug> \
  --title "<type>(<scope>): ..." --body-file pr-body.md

# 等 CI 合
gh pr checks NN --watch
gh pr merge NN --repo Hana-ame/peerdrive --merge

# 关 issue
gh issue close NN --repo Hana-ame/peerdrive --comment "via #<PR>"
```

## 附录 B：仓库惯例速查

| 惯例 | 内容 |
|---|---|
| 主干 | `refactor`（当前开发主干） |
| 分支命名 | `feat/<name>` / `fix/<name>` / `docs/<name>` / `module/<name>` / `<role>-agent` / `<role>-frontend` |
| 提交信息 | `<type>(<scope>): <一句话>`，scope 用目录名 |
| merge 风格 | `gh pr merge --merge`（merge commit，保留分支历史） |
| 历史 merge 形式 | `Merge pull request #NN from Hana-ame/<branch>` |
| CI workflow | `ci.yml`（doc-refs/backend/integration/peerjs/media-package/client-package/frontend）+ `go-build.yml`（5 平台矩阵）+ `e2e.yml` + `pages.yml` + `release.yml` |
| 后端 Go tag | `-tags nosqlite`（双 SQLite 驱动 CGO 冲突）；集成测试 `-p 1`（共享自托管信令，并行干扰） |
| 独立 go.mod | `back/peerjs`、`back/p2p_bt`、`back/signalserver`、`packages/peerdrive-client`、`packages/peerdrive-media` 各自 go.mod / package.json |
| doc-refs 检查 | `node scripts/check-doc-refs.mjs`（任何改动路径/行号引用的 commit 必跑） |
| Windows 验证 | CI Windows 格已跑测试；本机可 `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o x.test.exe <pkg>` 后拷到 Windows 执行 |

---

*本文档自 v1 起为唯一「推进流程」真相源。任何流程性决策（如何取项、如何验、如何收口）先改本文档，再改代码。*
