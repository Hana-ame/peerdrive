# doc-refs Job 触发条件与分级策略审视规范

> **关联 Issue**: [#206](https://github.com/Hana-ame/peerdrive/issues/206)  
> **文档定位**: 针对 `.github/workflows/ci.yml` 中 `doc-refs` job 的触发条件、失败阻断分级、失败信息质量及代码改动节奏反向问题展开系统性审视，并确立演进规范。

---

## 1. 现状与历史背景复盘

### 1.1 来历与初心
`doc-refs` job（由 `scripts/check-doc-refs.mjs` 驱动）诞生于 2026-10-06 两轮连续修复教训：
1. **第一轮（漏查裸文件名重命名）**：原代码文件被重命名或迁移，但文档中以裸文件名或旧路径引用的 `main.go:行号` 未同步更新；
2. **第二轮（漏查行号越界与语义漂移）**：代码被删减导致原行号越界，或行号虽然有效但代码实际内容已变为无关逻辑（例如心跳由 30s 调整为 5s，文档依然引用旧数值）。

存在性检查虽然无法替代人工理解上下文（"证明不了它说的是不是那件事"），但它能够以 **极低代价（全流程耗时 4~7 秒）** 机械化守住底线：**杜绝文档中出现 404 缺失路径与越界行号**。

### 1.2 近期运行痛点
根据 GitHub Actions 历史运行数据与 Issue #206 审计发现：
- **触发与修改节奏反向**：开发者提交纯业务代码改动时，如果改动重构了旧文件结构，会立刻触发 `doc-refs` 红灯；而未改动文档的开发者常常困惑于"我没改 markdown，为什么 doc-refs 会红"。
- **全量无差别阻断**：所有问题均以 `exit 1` 阻断 CI 流程，缺乏区分「明确损坏（路径彻底丢失）」与「弱损坏（行号轻微偏差/同名多候选）」的分级机制。
- **信息呈现缺乏交互标注**：控制台虽然打印了路径清单，但开发者必须在长日志中手动翻找，缺乏 GitHub PR 页面直接可见的 Inline Annotations（行内代码气泡标注）。

---

## 2. 触发条件深度审视 (Trigger Conditions Audit)

### 2.1 假阳性 vs 真实阻断场景辨析

| 改动类型 | 是否可能导致 doc 损坏？ | 当前表现 | 理想行为 | 评定 |
|---|---|---|---|---|
| **仅修改 `doc/**`** | 是（可能写错反引号路径） | 触发运行并检查 | 必须阻断 | 核心防线 |
| **仅修改 `back/**` 或 `front/**`**（涉及重命名/删除） | 是（直接导致已存文档指向 404 路径） | 触发运行并检查 | 必须阻断 | 核心防线 |
| **仅修改业务逻辑**（未增删文件/未大幅截断行数） | 否 | 触发运行（耗时 4s） | 快速通过（Pass） | 正常开销 |
| **仅修改 `.gitignore` / `scripts/**`（非核心）** | 极低 | 触发运行（耗时 4s） | 快速通过（Pass） | 正常开销 |

### 2.2 路径过滤 (Path Filtering) 的利弊权衡

Issue #206 建议探讨是否添加路径过滤（只在 `doc/**` 或代码变动时触发）。经深度技术评估，结论如下：

1. **不能仅对 `doc/**` 触发**：
   - 如果仅监听 `doc/**`，那么当后端开发者将 `back/internal/nodestate/` 变更或重构时，`doc-refs` 将被跳过，导致文档引用在主干静默失效，直到下一次有人修改文档才被连带爆出，造成责任错位。
2. **不能依赖 GitHub Actions workflow 级 `paths` 过滤**：
   - Peerdrive 仓库采用单仓 CI 架构，CI 门禁与分支保护要求 30/30 项检查全绿。若在 workflow 顶层加 `paths`，其他必须运行的 job 将被连锁抑制；
   - 若在 job 级别配置 `if: ...` 路径跳过，GitHub 将该 check 标记为 `Skipped`，破坏「0 cancelled, 0 failing, 30 successful, 0 skipped」的严格门禁口径。
3. **性能基准判定**：
   - `scripts/check-doc-refs.mjs` 采用一次性 O(n) 文件系统索引与正则流式扫描，扫描 155 个文档全量仅耗时 **0.3 秒**，容器镜像启动与拉取总计仅 **4~6 秒**；
   - 相比于引入复杂的变更文件比对逻辑与带来潜在跳过漏洞，保持无条件全量触发是成本最低、收益最稳健的策略。

---

## 3. 失败严重程度分级策略 (Severity Grading)

为解决「失败阻断合并」与「后一半仍需人读」的矛盾，将检查项明确划分为两个严重级别：

```mermaid
flowchart TD
    Scan[文档引用扫描] --> Check{命中类型判定}
    Check -->|路径 404 / 文件不存在| P0[阻断级 Fatal Error]
    Check -->|行号超出文件总行数| P0
    Check -->|同名文件候选模糊 (歧义)| P1[告警级 Warning]
    Check -->|反引号修饰符误用| P1
    
    P0 --> Exit1[Exit 1: 阻断 PR 合并]
    P1 --> Warn[Exit 0 + GitHub Warning Annotation]
```

### 3.1 阻断级 (Fatal Error - Exit 1)
包含不可妥协的硬性损坏：
1. **路径缺失 (Missing Path)**：反引号引用的目录或代码文件在仓库中完全不存在（`stat` 失败）；
2. **行号越界 (Line Out of Range)**：引用的行号超过了该文件的物理总行数（`line > totalLines`），证明引用的逻辑已被整段删除或文件已彻底更替。

**处置**：必须中断 CI，在 PR Checks 中报红，阻止代码合入。

### 3.2 告警级 (Warning - Exit 0)
包含需要提示人肉审视但不应挂死构建的软性问题：
1. **同名文件歧义 (Ambiguous Candidates)**：文档中使用了裸文件名（如 `app.go:10`），但仓库中存在多个同名文件且上下文权重未能 100% 裁决；
2. **类型不匹配提示 (Type Mismatch Hint)**：反引号写为目录尾斜杠（`dir/`）实际是普通文件，或写为文件名实际是目录；
3. **内容变迁猜测**：行号虽在物理范围内，但行号内容与文档描述可能已脱节。

**处置**：输出警告信息，但不返回非 0 退出码，保障正常改动流畅推进。

---

## 4. 失败信息质量与开发者体验提升 (DX Refinements)

### 4.1 GitHub Actions Workflow Commands 原生集成
当前 `check-doc-refs.mjs` 仅向控制台输出纯文本，开发者必须点开日志排查。  
**改进方案**：在输出控制台的同时，自动检测 `process.env.GITHUB_ACTIONS` 环境变量，当存在错误或警告时输出标准工作流指令：

```text
::error file=doc/NETDISK.md,line=42::文档引用失效：路径 'back/internal/old_path.go' 不存在
::warning file=doc/REFACTOR.md,line=108::文档引用存在多份同名候选：'app.go'
```

这使得 GitHub 会在 PR 的 **Files Changed** 界面对应行直接弹出气泡提示，改动者无需翻阅 CI 日志即可秒级定位。

### 4.2 错误上下文与修复建议补全
当报告路径缺失时，如果是在某个特定包下（如 `service/`），应检索是否存在同名文件被移动到相邻包（如 `transport/`），并在控制台附带输出：
`  ✗ doc/foo.md:12 back/internal/service/peerpull.go`  
`    💡 提示：检测到候选迁移路径 back/internal/transport/peerpull.go`

---

## 5. 落地改造清单与实施步骤

1. **保留 `ci.yml` 中的 `doc-refs` 独立 Job 架构**：不删除、不加跳过条件，保持 30/30 CI 门禁一致性；
2. **升级 `scripts/check-doc-refs.mjs`**：
   - 增加 GitHub Actions Workflow Commands（`::error` / `::warning`）输出；
   - 保持 Missing Path 与 Out of Range 为硬阻断（Fatal）；
   - 将同名文件歧义与软性建议保持为 Warning，仅在 `DOC_REFS_STRICT=1` 时转为阻断；
3. **更新开发者文档**：在 `doc/testing/README.md` §3 中补充 `doc-refs` 报错解读与就地修复指南。
