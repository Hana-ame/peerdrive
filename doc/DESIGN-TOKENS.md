# Frontend Visual Hierarchy & Design Token Specification

> Context: Issue #148 (视觉统一：设计 token 补齐 + 单位系统 px/rem 混用修正).
> Baseline: Monorepo front/ with 10 primary pages, dark glass aesthetic (`#101014` backdrop, Indigo brand accent, `#191920` card surface).

---

## 1. 10 页面视觉分级表 (Page Hierarchy & Visual Weight)

| 等级 | 页面 / 模块 | 角色与定位 | 视觉重量与排版特征 |
|---|---|---|---|
| **L1: 主网盘核心** | `Drive.jsx`<br>`CollectionView.jsx`<br>`Collections.jsx` | 用户的核心文件管理、浏览、多维筛选 (网盘中枢) | **主重视角**：全宽大画布，卡片/表格可切换视图，Category/Tag 过滤条，内联全功能预览弹窗 (`FilePreviewModal`)，DataState 骨架加载。 |
| **L2: 发现与对端** | `NodeControl.jsx`<br>`Connect.jsx`<br>`Transfers.jsx` | 节点互联、对端远程文件拉取、实时传输看板 | **交互看板视角**：仪表盘式分栏，速率 EMA 平滑曲线，实时进度条，对端状态连接指示器。 |
| **L3: 工具与扩展** | `BT.jsx`<br>`IPFS.jsx`<br>`Iwara.jsx` | 协议桥接下载器、第三方媒体流 | **工具条视角**：输入框驱动，参数/分片解析，单列/双列列表展示。 |
| **L4: 系统配置** | `Settings.jsx` | 节点认证、密钥管理、信令与代理配置 | **表单设置视角**：居中限制宽度容器 (`max-w-3xl`)，模块化分组卡片，单选与操作按钮明确。 |

---

## 2. 单位系统决策 (Unit System Standard)

- **基准字号**：`html { font-size: 22px; }` (保留 22px 放大基数，对大屏操作与可读性优化)。
- **统一规范**：
  - **排版字号与内边距**：组件间距一律采用标准 Tailwind rem 类（如 `p-4`, `gap-3`, `text-sm`, `text-xs`）。
  - **微小修饰与边框**：细线描边 (`border`, `1px`)、微小圆角或滚动条槽宽 (`6px`) 保留硬像素以保证渲染清晰度，不随 rem 缩放模糊。
  - **CSS 变量标准**：在 `:root` 提供语义化 token 变量以支持全局一致引用。

---

## 3. 设计 Token 扩展规范 (Design Tokens)

### 3.1 语义色彩体系 (Color Scales)
```css
:root {
  /* 品牌色阶 */
  --color-brand-primary: #6366f1; /* indigo-500 */
  --color-brand-hover: #4f46e5;   /* indigo-600 */
  --color-brand-light: #818cf8;   /* indigo-400 */

  /* 状态反馈色彩 */
  --color-success: #10b981;       /* emerald-500 */
  --color-warning: #f59e0b;       /* amber-500 */
  --color-danger: #ef4444;        /* red-500 */
  --color-info: #06b6d4;          /* cyan-500 */

  /* 表面分层 (Dark Glass) */
  --color-bg-base: #101014;
  --color-bg-card: #191920;
  --color-bg-raised: #21212a;
  --color-bg-hover: #2a2a34;
  --color-border-subtle: rgba(255, 255, 255, 0.06);
  --color-border-card: #2b2b34;

  /* 间距与圆角 */
  --radius-sm: 0.375rem;          /* 6px (rounded-md) */
  --radius-md: 0.5rem;            /* 8px (rounded-lg) */
  --radius-lg: 0.875rem;          /* 14px (rounded-card) */
  --radius-full: 9999px;          /* pill */

  /* 投影系统 */
  --shadow-card: inset 0 1px 0 0 rgba(255, 255, 255, 0.04), 0 10px 28px -14px rgba(0, 0, 0, 0.55);
  --shadow-pop: inset 0 1px 0 0 rgba(255, 255, 255, 0.06), 0 18px 44px -14px rgba(0, 0, 0, 0.65);
  --shadow-glow: 0 0 0 1px rgba(99, 102, 241, 0.35), 0 10px 34px -10px rgba(99, 102, 241, 0.4);
}
```

---

## 4. 响应式视口断点策略 (Responsive Breakpoints)

- **Mobile (最小支持视口: 360px - 639px)**:
  - 侧边栏/导航底栏或抽屉化折叠
  - 网盘主表格在小屏时自动以卡片视图 (`viewMode='grid'` 或单列卡片) 展现，避免水平超宽溢出
- **Tablet (640px - 1023px)**:
  - 2 列网格展示，搜索栏与筛选标签折行自适应
- **Desktop (>= 1024px)**:
  - 3-4 列网格或全功能宽表格，固定侧栏导航
