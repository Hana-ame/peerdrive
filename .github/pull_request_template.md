### 变更概述 (Summary)
<!-- 请简要说明本次改动的背景、核心实现与目标 -->

### 关联 Issue (Related Issues)
<!-- 例如：Closes #123 -->

### 协作与 CI 自查清单 (Pre-Merge Checklist)
> 详情参考 [doc/COLLABORATION.md](../doc/COLLABORATION.md) 协作规范。

- [ ] **[工作区干净]** 本地 `git status --porcelain` 输出完全为空，无遗留未跟踪文件或脏改动。
- [ ] **[文档与引用一致]** 已执行并通过 `node scripts/check-doc-refs.mjs`（无失效引用/越界行号）。
- [ ] **[凭据与占位串安全]** 新增测试字符串严格遵循 `doc/design/SECRET-SCANNING-DISCIPLINE-SPEC.md`，无敏感前缀占位串。
- [ ] **[测试发现背景标注]** 新增的 Go 测试用例均已显式标注「发现背景」注释。
- [ ] **[CI 100% 全绿]** GitHub Actions 对应 PR 的所有 checks（30/30）均已通过（0 failing, 0 pending, 0 cancelled）。
