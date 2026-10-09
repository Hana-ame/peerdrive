# Peerdrive CI 与协作流程规范 (COLLABORATION.md)

> **关联 Issue**: [#208](https://github.com/Hana-ame/peerdrive/issues/208)  
> **文档定位**: 沉淀 Peerdrive 单仓多模块分支模式下的日常开发与 CI 门禁操作纪律，解决「红着合并」、「连坐窗口」、「`gh api` 限流拖慢」、「本地 Clone 并发污染」四大痛点。

---

## 1. 核心协作纪律 (The Four Pillars)

### 纪律一：严禁「红着合并」（Zero Tolerance for Merging on Red）
- **痛点现状**：`refactor` 分支尚未开启硬性 Branch Protection 规则限制（见 Issue #1 评估）。历史上曾出现将近半数 PR 在 CI 尚未完全通过甚至报错的情况下被强行合并，破坏主干健康。
- **硬性规则**：
  - **必须 100% 全绿合并**：每个 PR 必须在 GitHub Actions 的 30/30 项检查（含后端单元测试、跨平台构建矩阵、AOP 分层测试、E2E 测试、文档引用检查等）全部显示 `✓` 后方可合并。
  - **禁止绕过与提前合并**：无论改动多小（即使仅修改文档或一行注释），都必须等待对应的 CI 完成。CI 挂起或报错时执行 `gh pr merge` 属于严重违规。
  - **单点故障清零**：若遇到偶发网络波动或不稳定用例导致个别 Job 红灯，必须先就地排查并触发重新运行（`gh run rerun <run-id> --job <job-id>`），确认转绿后方可合入。

### 纪律二：甄别与隔离「连坐窗口」（Cascade Blame Windows）
- **痛点现状**：当某个 PR 合并后，主分支触发的 push 构建与后续其他分支提起的 PR 构建在时间窗口上重叠。若主分支此前存在一次失败，后续一小时内提交的 PR 容易被误读为「整批 PR 都有缺陷」，引发无效回滚或恐慌式排查。
- **应对纪律**：
  - **基线先行**：拉取新分支前，先确保本地 `refactor` 基于最新的远端 HEAD，确认该 HEAD 在 GitHub Actions 上的对应构建为绿。
  - **独立定责**：遇到 PR 报错时，首先核实主分支最新 commit 是否也是同一错误。若是主分支遗留问题，优先在新 PR 中单独修复或等待主干恢复，严禁盲目给自己的业务改动背锅。
  - **保持串行节奏**：在主干出现 CI 失败的窗口期内，暂停非紧急 PR 的合入，等待修复 Commit 验证成功后再恢复后续合并排队。

### 纪律三：高效利用本地协议，防范 `gh api` 频控限流（Git-Protocol-First）
- **痛点现状**：在多轮 Issue 与 PR 自动化处理中，频繁调用 `gh api`、`gh issue view` 会迅速耗尽 GitHub Token 的 API Rate Limit 配额（每小时 5000 次），导致后续查询与合并操作陷入长时阻塞。
- **应对纪律**：
  - **优先使用 Git 本地协议**：查询提交历史、Diff 差异与文件状态一律使用本地 Git 命令（`git log -n 10`、`git show`、`git diff`），禁止无谓调用 `gh api repos/.../commits`。
  - **查重采用批量检索**：查询 Issue/PR 状态时优先使用一次性过滤命令（如 `gh issue list --search "..."` 或 `gh issue list --state open`），禁止在脚本循环中逐条执行 `gh issue view <id>`。
  - **静态检查优先本地快速执行**：文档引用使用 `node scripts/check-doc-refs.mjs`，依赖检索使用 `git grep`，代码比对使用 `git diff`，避免为了小排查触发远程 API 轮询。

### 纪律四：防范本地 Clone / 工作区并发污染（Clean Workspace Discipline）
- **痛点现状**：多个会话或开发代理共用同一个临时工作区或本地克隆时，容易遗留大量未暂存或未追踪文件（曾出现遗留 57 个文件的脏工作区），导致 `git status` 失去可信度，且分支切换时引发文件覆盖冲突。
- **应对纪律**：
  - **独立路径 / Worktree 隔离**：多任务或并发开发时，每个任务使用独立的分支与工作目录，或使用 `git worktree add` 进行完全物理隔离。
  - **进入与退出双重净化**：
    - 开始任务前：执行 `git status --porcelain`，输出必须为**完全空白**；
    - 提交合并后：立即切换回主开发分支并拉取最新代码（`git checkout refactor && git pull origin refactor`），清理临时分支；
    - 工作区遗留的未追踪文件（测试临时 db、临时产物）必须在提交前彻底清理或加入 `.gitignore`。

---

## 2. PR 提交与合并自查清单 (Checklist)

在发起 PR 与执行 `gh pr merge` 之前，开发者需逐项核对并确保满足以下条件：

- [ ] **[工作区干净]** 本地 `git status --porcelain` 输出完全为空，无遗留未跟踪文件或脏改动。
- [ ] **[文档与引用一致]** 执行 `node scripts/check-doc-refs.mjs` 输出 `RESULT: 无越界与缺失`。
- [ ] **[凭据与占位串安全]** 新增测试字符串严格遵循 `doc/design/SECRET-SCANNING-DISCIPLINE-SPEC.md`，无 `secret-` / `token-` 高危前缀。
- [ ] **[测试发现背景标注]** 新增的 Go 测试用例均已显式标注「发现背景」注释。
- [ ] **[CI 100% 全绿]** GitHub Actions 对应 PR 的所有 checks（30/30）均已通过（0 failing, 0 pending, 0 cancelled）。
