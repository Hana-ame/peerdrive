# peerdrive 前端重新设计 — Gemini 提示词

> 用途：将「二、Gemini 提示词全文」整段复制给 Gemini 使用。本文件不涉及仓库代码改动。

---

## 一、设计方向摘要（≤400 字）

方向：一次「信息架构 + 视觉 + 交互模型」的三合一重设计，而非单纯换皮。

目标：把「连接 → 节点 → 文件/传输」建成清晰的心智模型；让 9 个功能面全部有归属、可发现；顺手消解已知债（pd-client 副本漂移、fmtBytes 重复、nodeSession 全局单例、Nav 残缺）。

调性：数据面（节点/传输/存储/设置）走工程仪表盘——暗色、高密度、状态色明确；内容面（Drive/合集/预览）走内容浏览器——亮色、留白、文件优先。整体强调 P2P 的「诚实感」：连接状态真实可见、不美化。

与后端呼应：统一 Source 抽象 → 前端统一「来源」入口；collection path+sha+preview → 文件夹式浏览深化；echproxy 可选模块 → 功能可插拔导航；ws.js 契约不动，前端只在其上做状态分层与消息节流。

---

## 二、Gemini 提示词全文（可直接复制粘贴）

```
# 角色与任务
你是资深前端架构师兼产品设计师，为 peerdrive（自托管 P2P 文件网络：节点、BT、IPFS、合集聚合）重新设计 React 前端。输出可直接指导实施的设计方案，暂不要求完整落地代码。

# 现状（可信输入，直接采用）
- 技术栈：React 19 + Vite + Tailwind + react-router-dom v7；vitest/happy-dom 测试。
- 页面与功能域：已按功能域组织在 `front/src/features/*`（bt, collection, drive, ipfs, iwara, node, settings, transfers），旧 `front/src/pages/*` 作为 re-export shim 保留。路由包含深链支持（如 `/drive/:hash`），Nav 已按导航分组。
- 架构分层：
  - 传输层：`front/src/platform/transport-ws/`（client.js, index.js, status.js）负责本地 WebSocket 单长连接管理与帧协议，`ws.js` 为向前兼容导出层；
  - 共享工具：`front/src/platform/shared/` 合并收敛了 format (fmtBytes 收敛)、swBridge、collectionTree；
  - 依赖管理：原 `pd-client/` 内嵌副本已彻底移除，前端直接依赖本地独立包 `packages/peerdrive-client`；
  - 性能优化：首屏动态分包与 peerjs 动态 import 已落地，支持清单流式下载与 LRU 缓存。
- 后端呼应：collection（path+sha+preview JSON）、文件索引搜索、nodeshare 共享分级、统一 Source 抽象（local/sha/peer/url/ech/openlist/webdav）。
- 当前剩余核心挑战：界面视觉层缺乏系统化 Design Tokens、缺少统一组件库抽象（`index.css` `@layer components` 待演进为 `ui-kit`）、需要完成「数据面」与「内容面」的主题与交互调优。

# 设计目标（范围与不变量）
保留（不可破坏）：React+Vite+Tailwind+react-router 技术栈；ws.js 通信契约与帧协议；全部 9 个功能面。
允许重构：信息架构与路由/Nav；视觉与设计系统；组件分层与复用；pd-client 去重方向（建议：前端只依赖 packages/peerdrive-client 权威源，或把副本收敛为薄适配层——请给出结论）。
总目标：建立「连接 → 节点 → 文件/传输」的用户心智模型，让高级功能（BT/IPFS/合集）可发现、可渐进。

# 明确要求
1. 信息架构：给出完整导航结构（连接页何时出现、主工作区如何组织节点/文件/合集/BT/IPFS/传输/设置）；区分「数据面」（节点/传输/存储/设置：高密度、状态明确）与「内容面」（Drive/合集/预览：内容优先）；连接状态（未连接/连接中/已连接/断开）如何驱动 UI 分层。
2. 视觉调性（选一或混搭，说明取舍）：a) 工程仪表盘——暗色、等宽数字、状态色，强调 P2P 可信度；b) 内容画廊——明亮、留白、文件封面优先，强调浏览；c) 克制工具——中性色、单一强调色、零装饰，强调效率。给出色彩/字体/间距的设计 token 草案。
3. 组件复用：给出组件树与分层（page → feature → shared/primitive）；明确 fmtBytes 收敛、nodeSession 注入化或受控化、ConnectionStatus 升级为全局连接指示器。
4. 响应式：桌面优先，窄屏降级策略（表格→卡片、密集传输列表→摘要）。
5. 可访问性：键盘可达、焦点管理（连接流程/模态）、对比度、aria 语义。
6. 性能：大合集/大传输列表不爆管理面（虚拟滚动或分页、预览懒加载、ws 消息节流）；说明不引入重型状态库的理由或替代方案。

# 交付物形态（推荐 A）
A（推荐）：设计文档——组件树 + 设计 token + 每页设计说明（职责/状态/关键交互）+ 从现状到目标的最小迁移路线。
B：可运行代码骨架——路由/Nav/布局/token 落地 + 关键组件示例，按现有测试策略补测。
C：A+B：文档为主，附 2-3 个关键组件（全局连接状态、合集浏览器）实现示例。
选定后自报交付清单。

# 约束
- 优先零新依赖；确需引入（如虚拟滚动）时注明取舍。
- 后端契约不可改：ws.js 帧协议、/peerjs/* 端点、collection JSON（path+sha+preview）结构。
- 测试策略：vitest/happy-dom；状态机与关键交互做组件测试，纯函数（collectionTree/format）保持单测。
- 不假设未上线的后端能力（Source 抽象未完成前，UI 给出兼容写法，不绑死未来 API）。

# 验收标准（设计完成 =）
- 9 个功能面全部有归属页面/入口，无孤儿功能；
- 新 Nav/路由与现有直链兼容，或给出重定向策略；
- 组件树中无已知重复（fmtBytes/nodeSession/pd-client 去向明确）；
- 每页有状态清单与交互说明，一名工程师可无歧义实现；
- 响应式、可访问性要求逐项可核查；ws.js 契约不破坏；测试策略可执行。
```

---

## 三、使用说明

- 将「二」中整段（含标题行）复制给 Gemini；中文为主、技术词保留英文，Gemini 一次可消化。
- 若想让 Gemini 先对齐再产出，可在末尾追加一句：「先给 5 分钟思考版要点，再输出完整方案。」
