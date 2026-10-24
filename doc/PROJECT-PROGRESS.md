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

**实查于：2026-10-09 16:0x UTC**（下次跑流程请刷新此时间戳）

### 1.1 refactor 分支 tip（`git -C <clone> log --oneline -15 origin/refactor`）

```
e652ab4 Merge pull request #61 from Hana-ame/feat/peerjs-xor
79f764a feat(peerjs): XOR-encrypt DataChannel data plane (default off)
717dbe8 Merge pull request #54 from Hana-ame/feat/ws-split
0729721 Merge pull request #52 from Hana-ame/feat/http-split
eb493fe fix(ws): 修掉 /ws/peer 会话注册用例的两头竞态
98a3cac chore(http): rebase onto refactor, keep the imports the tracker/source work added
0e44ad4 test(serverapp): drop the DB-backed probes from the endpoint digest
36056a3 test(httpd): pin the port-conflict invariant to the listen OpError shape
b8ec1da test(httpd): assert the port-conflict errno, not the message
098a54e test(httpd): gate the self-signalling tests off Windows
d118c45 test(serverapp): make the endpoint digest golden platform-portable
1b500ce refactor(http): extract HTTP listen/serve shell into internal/httpd
004172d fix(ws): golden 比对在 Windows 上把内核丢帧当成拆分漂移
edb5d89 Merge pull request #60 from Hana-ame/feat/tracker
1cdb037 feat(tracker): BitTorrent HTTP tracker server + client-side public tracker injection
```

**tip = `e652ab48`** — Merge PR #61 feat/peerjs-xor（XOR 加密 DataChannel 数据面，默认关闭，见 §3.14 相关）
**最近合并**：#61 XOR → #54 ws-split → #52 http-split → #60 tracker → #59 source-interface → #58 mktorrent → #57 doc-refs-echcore → #56 openlist-source

### 1.2 open issues（共 16 条，全为前端）

来源：`gh issue list --repo Hana-ame/peerdrive --limit 100 --state all`

| # | 标题 | 优先级 | 标签 |
|---|---|---|---|
| 78 | 【优化·P2】杂项：生产 sourcemap 置 false / fmtBytes 归并 / Drive 切 /files/browse | low | tech-debt |
| 77 | 【优化·P1】入口残缺：Nav 按连接状态分组恢复 8 页 + Drive 加 /drive/:hash 深链 | medium | ux |
| 76 | 【优化·P1】大 manifest 爆内存：loadBySha 改 downloadStream 流式解析 | medium | ux |
| 75 | 【优化·P1】大集合挤单连接：preview 按需拉 + 并发上限 4 + 启用已有 /files/browse 分页 | medium | ux |
| 74 | 【优化·P1】不可变数据零缓存：manifest 按 sha 模块级 LRU + preview 缓存升模块级 | medium | ux |
| 73 | 【优化·P0】首屏零分包：App.jsx 9 页 React.lazy + peerjs 动态 import（gzip -47%） | high | ux |
| 72 | 【优化·P0】ws 请求无超时保护：丢帧永久挂起并污染后续二进制帧 | high | ux |
| 71 | 【优化·P0】冷启动首个请求必失败：admin/download 在 CONNECTING 排队等 open 而非 reject | high | ux |
| 69 | 【体验】Nav 只暴露 2 条入口，8 条路由靠直链，导航可发现性差 | medium | ux |
| 68 | 【代码质量】nodeSession 模块级可变单例连接页间交接 | low | tech-debt |
| 67 | 【代码质量】fmtBytes 有 4 份重复实现，收敛到 lib/format.js | medium | tech-debt |
| 66 | 【前端分解·可选】index.css @layer components 段抽成独立 ui-kit | low | decomp-plan |
| 65 | 【前端分解】剩余 8 个页面按功能域迁入 features/* | medium | decomp-plan |
| 64 | 【前端分解·最高风险】删除 front/src/lib/pd-client 内嵌副本，改用 peerdrive-client 依赖 | high | decomp-plan |
| 63 | 【前端分解】format / swBridge / collectionTree 收进 platform/shared | medium | decomp-plan |
| 62 | 【前端分解】ws.js 拆出独立 platform/transport-ws 传输模块 | medium | decomp-plan |

### 1.3 open PR

来源：`gh pr list --repo Hana-ame/peerdrive --limit 100 --state all`

| # | 标题 | head → base | merge 状态 | 备注 |
|---|---|---|---|---|
| 70 | feat(twitterpic): 整合 twitter-pic 图库为 user→collection | `feat/twitterpic-user-collection` → refactor | CLEAN（可合） | 3 commits，待 review；head SHA `c4c3ae57` |
| 55 | docs(design): add Gemini prompt for peerdrive frontend redesign | `docs/frontend-redesign-gemini-prompt` → refactor | — | 纯文档 PR，可独立合 |

### 1.4 CI 状态（`gh run list --repo Hana-ame/peerdrive --branch refactor`）

| 分支 tip | Go Build Matrix | E2E | Peerdrive CI |
|---|---|---|---|
| `e652ab48`（当前 refactor tip） | ✅ success | ✅ success | ❌ failure |
| `717dbe81`（上一 tip） | ✅ success | ❌ failure | ✅ success |
| `07297212` | ✅ success | ✅ success | ✅ success |

**判读**：
- refactor 当前 tip 的 `Peerdrive CI` 失败 = **预存 TestStartPullCancel 竞态**（协调者已确认，见 §3 队列 R2），**非本 tip 引入**；
- 「CI 全绿」的实操判据：**Go Build Matrix + E2E 双绿** + `Peerdrive CI` 中除 `TestStartPullCancel` 外的所有 job 绿（doc-refs / backend / integration / peerjs / media-package / client-package / frontend）。
- PR #70 head `c4c3ae57`：Go Build Matrix ✅ / E2E ✅ / Peerdrive CI ❌（同源预存竞态）→ 视为可合。

### 1.5 已知待办来源（协调者盘点，采信）

后端待办**不在 GitHub issue 中**，全部来自 `doc/REFACTOR.md §3.14` / `doc/analyses/` / 协调者口头清单。
完整映射见 §3 优先级队列，每条都标注了来源。

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
| **P0** | 修 TestStartPullCancel 预存竞态（CI 常红元凶） | 口 / §3.20 风格 | — | 单 PR `fix/pull-cancel-race` | 小 | `[]` |
| **P0** | merge #70 twitterpic-user-collection（已 CLEAN，仅待 review） | #70 | — | 直接合 | 小 | `IN-FLIGHT #70` |
| **P0** | 前端 P0 访问优化：#71 冷启动排队 + #72 ws 超时保护（**同 PR**） | #71 + #72 | — | **`feat/front-p0-ws`（合并 #71+#72，一个 PR 解决两个根因）** | 中 | `[]` |
| **P0** | 前端 P0 首屏零分包：#73 React.lazy + peerjs 动态 import | #73 | — | 单 PR `feat/front-lazy` | 中 | `[]` |
| **P0** | GitGuardian 旧 key 注释清理（扫 SECRET_KEY/AUTH_TOKEN 残留注释） | 口 | — | 单 PR `fix/scrub-old-keys` | 小 | `[]` |
| **P1** | 前端 P1：#76 loadBySha 改 downloadStream 流式解析（防大 manifest 爆内存） | #76 | #73 完成后前端已拆包 | 单 PR `feat/front-stream-manifest` | 中 | `[]` |
| **P1** | 前端 P1：#74 不可变数据 LRU 缓存（manifest 按 sha + preview） | #74 | 无强依赖，但建议 #75 前 | 单 PR `feat/front-cache` | 中 | `[]` |
| **P1** | 前端 P1：#75 大集合并发上限 4 + 启用 /files/browse 分页 | #75 | #74 之后（缓存先行，分页才有意义） | 单 PR `feat/front-browse-pagination` | 中 | `[]` |
| **P1** | 前端 P1：#77 Nav 分组恢复 8 页 + /drive/:hash 深链（与 #69 高度重叠） | #77 + #69 | — | 单 PR `feat/front-nav-grouping` | 小 | `[]` |
| **P1** | 前端分解·最高风险：#64 删 pd-client 内嵌副本，改用 peerdrive-client 依赖 | #64 | **#71/#72 先合**（避免两个根因叠加） | 单 PR `feat/front-drop-pdclient-copy` | 大 | `[]` |
| **P2** | 前端分解：#63 format / swBridge / collectionTree 收进 platform/shared | #63 | — | 单 PR `feat/front-platform-shared` | 中 | `[]` |
| **P2** | 前端分解：#62 ws.js 拆出 platform/transport-ws | #62 | #71/#72 先合（ws 改动收敛后） | 单 PR `feat/front-transport-ws` | 中 | `[]` |
| **P2** | 前端分解：#65 剩余 8 个页面按功能域迁入 features/* | #65 | #63、#64 之后 | 单 PR `feat/front-features-migrate` | 大 | `[]` |
| **P2** | 前端代码质量：#67 fmtBytes 收敛到 lib/format.js | #67 | — | 可与 #78 合并成 `chore/front-cleanup` | 小 | `[]` |
| **P2** | ech-proxy 整合后续 PR2：去 exe 化（去掉外部二进制依赖） | 口 / §3.14 | #51 echproxy 已合 | 单 PR `refactor/echproxy-no-exe` | 中 | `[]` |
| **P2** | OpenList 爬虫建索引 PR②（#56 已合 OpenList source，PR② 是爬虫建索引） | 口 / #56 | #56 | 单 PR `feat/openlist-crawler-index` | 中 | `[]` |
| **P3** | 前端分解·可选：#66 index.css @layer components 抽 ui-kit | #66 | #65 之后 | 单 PR `feat/front-ui-kit` | 中 | `[]` |
| **P3** | 前端代码质量：#68 nodeSession 模块级可变单例重构 | #68 | 无 | 单 PR `refactor/front-node-session` | 中 | `[]` |
| **P3** | #78 杂项：sourcemap 置 false + fmtBytes 归并 + Drive 切 /files/browse | #78 | #75、#67 之后 | 可与 #67 合并 | 小 | `[]` |
| **P3** | Windows 测试覆盖补齐（cross-platform 矩阵已跑，覆盖缺口仍在） | 口 / §3.20 | — | 单 PR `test/windows-coverage` | 中 | `[]` |
| **P3** | iwara 字段名核对（#53 合入后字段映射待实查上游 API） | 口 / #53 | — | 单 PR `fix/iwara-field-names` | 小 | `[]` |
| **P3** | exhentai Sync 日志补全（便于诊断同步失败） | 口 | — | 单 PR `chore/exhentai-sync-log` | 小 | `[]` |
| **P3** | media-node 无 peerjs sha 访问（media-node 只走 url，补 sha 直取） | 口 / 模块 12 | — | 单 PR `feat/media-node-sha` | 中 | `[]` |
| **P4** | back/peerjs → go-peerjs 独立 repo 镜像同步（含 #48 拆分 + #61 XOR 的改动镜像） | R§3.14 | #48、#61 已合 | 单 PR 到 `github.com/Hana-ame/go-peerjs`（非本仓） | 小 | `[]` |
| **P4** | ech-proxy 整合后续 PR3：source 接入（URLSource 走 ech-proxy 出口） | 口 / 模块 07 | PR2 去 exe 化之后 | 单 PR `feat/echproxy-source-integration` | 中 | `[]` |
| **P4** | ech-proxy 整合后续 PR4：media-node 多模式（不只 twimg） | 口 / 模块 12 | PR2、PR3 之后 | 单 PR `feat/media-node-multi-mode` | 大 | `[]` |
| **P4** | docs #55 Gemini prompt PR（纯文档，可独立合） | #55 | — | 直接合 | 小 | `IN-FLIGHT #55` |

**队列条数：27 条**（P0×5、P1×5、P2×7、P3×7、P4×3、IN-FLIGHT×2 已计入各自优先级）。

**取项顺序建议**（协调者按此推进，除非有更紧急的临时需求）：

1. **本轮**：修 TestStartPullCancel 竞态（P0，解锁 CI 全绿判据）→ merge #70（P0，已 CLEAN）
2. **次轮**：#71+#72 同 PR（P0，前端 P0 访问优化第一个）+ #73（P0，前端 P0 首屏分包）
3. **三轮**：#76 → #74 → #75（P1，前端 P1 三连）
4. **四轮**：#77（P1，Nav 入口）+ #64（P1，最高风险前端分解，#71/#72 之后）
5. **五轮**：#63 → #62 → #65（P2，前端分解收尾）+ ech-proxy PR2 + OpenList PR②
6. **六轮**：P3 杂项批处理（#66/#68/#78 + Windows 覆盖 + iwara 字段 + exhentai 日志 + media-node sha）
7. **七轮**：P4 镜像同步 + ech-proxy PR3/PR4

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
