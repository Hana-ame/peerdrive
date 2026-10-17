# 前端重设计执行承接计划规范 (Frontend Redesign Handoff Plan)

> 对应 Issue: #113  
> 状态: 承接方案确立，前置条件检查就绪  
> 关联 Issue: #110 (Gemini 产出盘点), #64 (依赖清理), #65 (页面目录重构), #66 (UI-Kit 建设), #82 (首屏分包优化)

---

## 1. 前置条件就绪核查

Issue #113 提出的核心硬约束是：**「视觉重设计必须排在 #64 / #65 之后，在稳定的结构上做」**。

经仓库实读核查，前置重构已全量落地并合入主干：
1. **数据层定型（#64 / PR #97）**：✅ 已完成。`src/pd-client` 内嵌副本已彻底删除，前端直接依赖 `packages/peerdrive-client`。
2. **目录位置定型（#65）**：✅ 已完成。所有 8 个核心页面已迁入 `front/src/features/*`（`bt`, `collection`, `drive`, `ipfs`, `iwara`, `node`, `settings`, `transfers`），旧 `pages/` 为纯导出 shim。
3. **传输与工具层解耦（#94 / #96）**：✅ 已完成。`platform/transport-ws` 与 `platform/shared` 已成为稳固的基础设施。

**结论**：**前置阻塞条件已全部解除，前端工程结构已完全具备安全承接视觉与交互重设计的条件。**

---

## 2. 承接红线与架构原则

为确保承接过程不破坏既有工程收益，设定以下三大铁律：

### 2.1 目录规范红线（严禁逆向退化）
- **禁止**在 `front/src/pages/` 重新堆砌业务实现；
- 遵循领域分层规范：
  - 基础无业务含义组件 $\rightarrow$ `src/components/ui/`（或 `ui-kit`）；
  - 领域特定视图与组件 $\rightarrow$ `front/src/features/<domain>/components/`；
  - 状态流转与 API 封装 $\rightarrow$ 维持 `platform/transport-ws` 统一长连接与帧调度。

### 2.2 包体积与性能红线（守护 #82 优化成果）
- **零庞大 UI 依赖**：严禁引入 MUI、Ant Design、Element 等重型组件库，坚守纯 CSS (Tailwind) + 原生无头组件架构；
- **首屏体积预算（Bundle Budget）**：
  - 首屏 JS gzip 体积必须保持在 $\le 60\text{ KB}$（PeerJS 仍维持动态按需 import）；
  - 禁止在首屏预加载次要功能域（BT/IPFS/Iwara）。

### 2.3 设计抽象不重复（与 #66 ui-kit 合流）
- 拒绝为 Gemini 设计稿单独起一套 Design System；
- 将 Gemini 提炼的按钮、卡片、模态框、状态标签直接沉淀至 #66 规划的 `ui-kit`。

---

## 3. 三步走承接实施路线图

```text
[阶段 1: Design Tokens & 样式基础]
   ├── 规范 Tailwind 色板与 CSS 变量 (数据面暗色 vs 内容面亮色)
   └── 收敛状态语义色 (Online/Syncing/Failed/Relayed)
           │
           ▼
[阶段 2: 原子级 UI-Kit 组件沉淀 (#66 合流)]
   ├── Button / Badge / Card / Modal / Input / EmptyState
   └── ConnectionStatus 全局连接指示器
           │
           ▼
[阶段 3: 业务页面渐进式视觉升级]
   ├── P1: Drive 文件网盘 (内容浏览器：卡片/树/预览/流式进度)
   ├── P2: NodeControl 仪表盘 (工程仪表盘：网络拓扑/资源/Peer 列表)
   └── P3: Transfers & Settings (高密度传输队列与配置项)
```

### 3.1 阶段 1：Design Tokens 与双调性主题（`index.css`）
针对 Gemini 提示词中提出的「数据面仪表盘 vs 内容面文件浏览器」双重调性，在 `front/src/index.css` 中注入标准 CSS 变量：
- **数据面（Dashboard Context）**：深灰/暗色底（`#0d1117`），等宽数字（`font-mono`），高对比度状态色（绿/蓝/黄/红）；
- **内容面（Drive Context）**：中性浅色/深色自适应，大留白，网格图片卡片与文件层级优先。

### 3.2 阶段 2：原子级组件库构建（`src/components/ui/`）
将零散的内联 Tailwind 类收敛为无状态受控组件：
1. `Button`：支持 `primary`, `secondary`, `danger`, `ghost` 变体与 loading 状态；
2. `Badge`：针对 P2P 状态展示（如 `Direct`, `Relay`, `Public`, `Private`）；
3. `Modal` / `Drawer`：具备无障碍焦点陷阱（Focus Trap）与 Escape 键盘监听；
4. `GlobalConnectionBar`：替代旧 `ConnectionStatus`，常驻顶部或侧边，直观反馈 WS 与 WebRTC 信令连通性。

### 3.3 阶段 3：特性页面承接与迁移
按优先级与风险分批重构页面：
- **批次 1 (`features/drive`)**：
  - 文件网盘是核心交互面；
  - 增强文件夹树导航，支持拖拽上传视觉反馈，对接 `collection` 的 `path + sha + preview`；
  - 保持现有 `/drive/:hash` 深链兼容。
- **批次 2 (`features/node`)**：
  - 升级节点管理页为真正的数据仪表盘；
  - 整合 NAT 状态、发现通道健康度（呼应 #147/#144）、存储占用与常驻对端清单。
- **批次 3 (`features/transfers`, `features/settings`)**：
  - 传输列表引入分页/虚拟列表优化，确保数百个分片并发拉取时不卡顿 UI。

---

## 4. 验收基准清单

| 检查项 | 验证方式 | 预期标准 |
|---|---|---|
| **代码位置** | 检查目录文件 | 新增代码仅存在于 `features/*` 与 `components/ui/`，无根目录新页面 |
| **测试套件** | `npm test` (vitest) | 前端既有单元测试与快照 100% 保持绿色 |
| **构建体积** | `npm run build` | dist 产物中首屏 entry chunk gzip 体积 $\le 60\text{ KB}$ |
| **路由兼容** | 检查 App.jsx | 既有 10 条路由与深链 100% 可达，无破坏性变更 |
| **组件复用** | 代码静态分析 | `fmtBytes` 唯一来源 `platform/shared`，零重复复制 |
