# 前端 Lint 与 TypeScript 引入成本收益评估

> 对应 Issue: #156  
> 状态: 评估完成，制定渐进落地路线图  
> 适用范围: `front/` 模块及消费端包与主模块交互层

---

## 1. 现状盘点与问题诊断

当前 `front/` 模块的技术栈形态呈现典型的「**工具链 TS，应用代码纯 JSX**」分裂特征：

```text
front/
├── vite.config.ts                    ← 构建工具 TS
├── vitest.config.ts                  ← 测试工具 TS
├── package.json                      ← 零 eslint / prettier / typescript 依赖
│                                       scripts 仅有 dev / build / preview / test
└── src/                              ← 100% .jsx / .js，0 个 .ts / .tsx
```

### 1.1 核心痛点

1. **零类型约束与人肉契约对齐**：
   - 10 个页面（Drive、Collections、Market、Peers、PeerDetail、Transfers 等）与 3 个 feature 模块没有编译期数据类型验证。
   - 跨前后端的传输契约（如 `Entry`、`ShareSnapshot`、`dcResp`、`DirEntry`、`FileListItem`）完全依赖人肉比对。
   - 后端有 `equiv_test.go`（89 条路由黄金表与 digest 门禁）保障稳定性，而前端缺乏任何结构级守卫。

2. **零格式约束与 Diff 噪音风险**：
   - 无 Prettier 配置，不同协作者/Agent 编辑器格式化规则不一致，产生无意义的代码重排。
   - 历史事故教训（2026-10-03 提交事故）：批处理修改越界波及 16 个文件运行时行为，若缺乏严格的格式与代码分析工具，细微语法与结构变动极难一眼审核。

3. **静态分析与代码异味盲区**：
   - 缺少 React Hooks 依赖规则检查（`eslint-plugin-react-hooks`），极易写出闭包过时或无限重渲染 bug。
   - 缺少模块间循环依赖检测（例如 #68 `nodeSession` 单例跨组件引用的潜在耦合）。

---

## 2. 引入阶梯与成本收益分析

遵循「**小步快跑、收益前置、控制破坏性**」原则，评估 6 个层级的落地代价与价值：

| 阶梯 | 措施 | 成本 | 收益 | 推荐度 | 说明 |
|---|---|---|---|---|---|
| **L1** | **Prettier 统一格式化** | 极低 | 极高 | **首选即行** | 消除格式 diff 噪音；纯格式重排，不改 AST 运行时语义 |
| **L2** | **ESLint 最小基线** | 低 | 高 | **首选即行** | 引入 `react-hooks/rules-of-hooks` 与循环引用检测，拦截常见逻辑陷阱 |
| **L3** | **ESLint CI 门禁** | 低 | 高 | **同步落地** | 在 `ci.yml` 的 `frontend` job 增设 `npm run lint` 门禁，防止坏风格合入 |
| **L4** | **`// @ts-check` + JSDoc** | 中 | 极高 | **强烈推荐** | **性价比最高的中间态**：零改名（保持 .jsx/.js），纯注释即可由 VSCode/Vite 获得完整类型推导 |
| **L5** | **关键跨端结构 `.d.ts` 声明** | 中 | 高 | **推荐实施** | 单独建立 `types/contracts.d.ts`，定义 `Entry` / `ShareItem` / `PullTask` 等模型 |
| **L6** | **全量迁移至 TypeScript (`.tsx`)** | 极高 | 边际收益低 | **明确不推荐** | 涉及大量文件重命名与构建流水线调整，与当前 #64/#65 前端组件分解严重冲突 |

---

## 3. 详细方案设计

### 3.1 阶段一：Prettier 引入（独立 PR，纯格式）

- **依赖**: `prettier`
- **配置文件**: `.prettierrc.json`（单引号、2 空格、尾随逗号 es5、printWidth 100）
- **操作原则**:
  - 单独起分支 `feat/frontend-prettier`，全仓运行一次 `npx prettier --write "src/**/*.{js,jsx,css,html}"`。
  - **必须**在 #66（ui-kit 抽离）和大规模重构前完成，否则批量改样式时产生的 diff 将完全不可读。

### 3.2 阶段二 & 三：ESLint 规则与 CI 门禁（独立 PR）

- **依赖**: `eslint`, `@eslint/js`, `eslint-plugin-react`, `eslint-plugin-react-hooks`
- **配置文件**: Flat Config `eslint.config.js`
- **核心规约**:
  - 开启 `react-hooks/rules-of-hooks` (error) 与 `react-hooks/exhaustive-deps` (warn)。
  - 开启 `no-undef`, `no-unused-vars` (允许 `_` 前缀)。
  - 开启 import 循环依赖排查规则（覆盖 #68 单例风险）。
- **CI 集成**:
  - `front/package.json` 添加 `"lint": "eslint src"`。
  - `.github/workflows/ci.yml` 在 `frontend` job 中增加 `run: npm run lint`。

### 3.3 阶段四 & 五：渐进式类型体系（`@ts-check` + `.d.ts`）

**为什么不全量上 TS？**
全量将 `.jsx` 批量改为 `.tsx` 会产生巨量编译类型报错，迫使开发者大量编写 `any` 或耗费数周时间填补第三方库缺失的声明，严重拖慢网盘功能迭代与 #113 前端重设计承接。

**最优折中解法**：
1. 在 `src/types/` 下维护核心契约声明 `peerdrive.d.ts`：
   ```typescript
   export interface ShareEntry {
     path: string;
     sha?: string;
     size: number;
     type: 'file' | 'dir';
     sources?: string[];
   }

   export interface TransferProgress {
     hash: string;
     receivedSize: number;
     totalSize: number;
     status: 'pending' | 'downloading' | 'completed' | 'error';
   }
   ```
2. 在核心业务组件与 API 客户端顶部添加 `// @ts-check`，借助 JSDoc 标注函数输入输出：
   ```javascript
   // @ts-check
   /**
    * @param {string} hash
    * @returns {Promise<import('./types/peerdrive').ShareEntry>}
    */
   export async function fetchFileDetail(hash) { ... }
   ```
3. 关键页面和 Hook 逐步享受到智能补全和静态检查，无需破坏原有 JSX 构建流程。

---

## 4. 与相关 Issue 的协同关系

- **#55 / #110 / #113 (Gemini 前端重设计执行承接)**:
  - 格式规范（Prettier）是新旧设计交接的前置基石。
  - 接入 Gemini 产出的组件时，统一经由 Prettier 格式化，避免两套缩进风格混杂。
- **#66 (ui-kit 抽离)**:
  - Prettier 必须先行落地，避免抽取通用组件时造成无关 diff 污染。
- **#68 (nodeSession 单例治理)**:
  - ESLint 循环依赖检查作为防线，杜绝组件层与传输单例隐式双向绑定。
- **#64 / #65 (前端页面分解与架构解耦)**:
  - 渐进式 JSDoc 模式允许分解过程中逐文件启用检查，不阻塞解耦进度。

---

## 5. 结论与执行路线

1. **短期（建议近期立项）**：
   - 提交 PR 1：引入 Prettier，执行全量无破坏格式化。
   - 提交 PR 2：引入 ESLint Flat Config + CI 门禁。
2. **中期（配合契约重构）**：
   - 建立 `src/types/contracts.d.ts` 规范核心数据实体。
   - 核心服务层（`api.js`、`client.js`、`useDrive.js`）启用 `// @ts-check`。
3. **长期（不建议）**：
   - 维持 JSX 为主流业务展现层，不进行全量 `.tsx` 迁移，保持敏捷度与零构建阻抗。
