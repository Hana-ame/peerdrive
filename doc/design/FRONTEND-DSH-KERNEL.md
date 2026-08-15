# Peerdrive 前端参考 dsh 前端内核的设计

> 日期：2026-08-16
> 本文专门参考 DeepSeek Harness（dsh）**Web 前端本身的架构**：
> `dsh-client-web`（shell kernel）、`dsh-client-modules`（浏览器模块系统）、
> `dsh-client-ui-slots`（slot 注册表）、`dsh-client-connection`（/api 传输桥）、
> `dsh-host-frontend-static`（SPA 静态服务）。
> 目标是让 Peerdrive 前端也采用 **host 推送 boot graph + 浏览器 shell 两阶段启动 + 插件按需加载 + slot 组合 UI** 的机制。
> 更上层的 bundle/profile/patch 组织见 `FRONTEND-DSH-INSPIRED.md`。

---

## 1. dsh 前端的关键机制

### 1.1 Host 推送 boot graph

dsh 不是让前端自己 import 所有模块，而是由 **Node/Host 端** 扫描启用的插件行（`dsh.client` 行），
把浏览器插件清单编译成一张启动图，注入页面：

```js
window.__DSH_BOOT__ = {
  entries: [
    { id: 'core', src: '/plugins/core/client.js', immediate: true },
    { id: 'connection', src: '/plugins/connection/client.js', immediate: true },
    { id: 'ui-layout', src: '/plugins/ui-layout/client.js', immediate: true },
    // ... 其余按需
  ]
}
```

前端 shell 不自己决定“要加载哪些插件”，只负责执行 host 给的这张图。

### 1.2 两阶段启动（web2）

1. **Stage 1 - module face**：
   - 创建浏览器端模块系统（`ClientModuleLoader`）。
   - 并行 prefetch `immediately` 档的 bundle 脚本。
   - 执行 bundle 脚本时 **只注册 factory**，不执行组件/副作用；
     样式等副作用放到 factory 被 materialize 时才执行。

2. **Stage 2 - plugin face**：
   - 把模块系统注入 Loader。
   - 为 boot graph 每一行创建一个 loader entry。
   - 等 loader 静默 + 所有 entry fiber ACTIVE 后，一次性渲染完整 UI。

这样做的价值：
- 插件之间不需要外部排序，依赖通过模块系统按需 materialize。
- 某个插件加载失败不会让核心 shell 崩溃。
- 页面首屏可以只加载 immediate 档，其他路由/面板懒加载。

### 1.3 Slot 注册表

dsh 的 UI 组合核心不是“Route 数组”，而是 **SlotRegistry**：

- `SlotCore` 启动时只有 `root` 一个预置 slot。
- 每个插件通过 `apply(ctx)` 拿到 `ctx.slots`。
- 插件可以 `ctx.slots.inject(name, callback)` 等待某个 slot 被声明。
- 插件用 `slots.register({ name, children?, store?, inject? }, Component)` 把自己的组件贡献进 slot，并同时声明子 slot。

例如布局插件声明：

```ts
ctx.slots.register({ name: 'root', children: ['sidebar', 'conversation', 'details'] }, AppFrame)
```

业务插件贡献：

```ts
ctx.slots.inject('sidebar', () =>
  ctx.slots.register({ name: 'p2p', ... }, P2PStatus)
)
```

好处：
- UI 不再靠“手动排列 Navbar/页面”；
- 每个插件只声明自己贡献到哪个插槽；
- 最终布局由 slot 树组合出来。

### 1.4 /api 浏览器传输桥

dsh 的浏览器不是直接 fetch 一堆 REST URL，而是统一走 `/api` 桥：

- **Unary / respond**：HTTP POST。
- **事件/下行**：`/api/events.mux`、`/api/events.host` 各一条 WebSocket，只下行。
- **Host 描述**：ready 后通过 `host.describe` 告诉浏览器当前 Host 是什么。
- **Trust fence**：所有 `/api` 请求先校验 Host 头，只允许 loopback 或 `trustedHosts`，
  否则 403。这是防 DNS-rebinding / CSWSH 的浏览器信任边界。

### 1.5 SPA 静态服务

`dsh-host-frontend-static` 作为 webserver 的 fallback seat：

- 只服务构建好的 `dist/`。
- 路径穿越返回 403。
- 所有 miss 回退到 `index.html`（SPA routing）。
- 每次 index 响应会执行 index taps，注入 boot graph。

---

## 2. Peerdrive 前端映射

| dsh 前端 | Peerdrive 前端 |
|---|---|
| `window.__DSH_BOOT__` | `window.__PEERDRIVE_BOOT__` |
| `dsh-client-web` shell kernel | `front/src/shell/` |
| `dsh-client-modules` 模块系统 | `front/src/modules/` |
| `dsh-client-ui-slots` slot 注册表 | `front/src/slots/` |
| `dsh-client-runtime` 运行时对象 | `front/src/runtime/` |
| `dsh-client-connection` /api 桥 | `front/src/transport/` + `back/bundles/web` |
| `dsh-host-frontend-static` | `back/bundles/web` 的静态服务组件 |
| `/plugins/<id>/client.js` | `/plugins/<name>/client.js` |
| `dsh.client` 行 | `peerdrive.client` 行 |
| `ctx.slots` | `ctx.slots`（同名概念） |
| `ctx.connection` | `ctx.api` / `ctx.connection` |

---

## 3. 目标前端架构

```
浏览器
│
├─ shell（front/src/shell）
│   ├─ AppWebEntry.js          # 读取 __PEERDRIVE_BOOT__，两阶段启动
│   ├─ boot-status.js          # 加载状态，不依赖插件
│   └─ module-loader.js        # ClientModuleLoader
│
├─ modules（front/src/modules）
│   ├─ load.js                 # prefetch/import/invalidate
│   ├─ graph.js                # 由 boot entries 构建模块表
│   └─ registry.js             # 静态注册表（shell 自有行）
│
├─ slots（front/src/slots）
│   ├─ SlotCore.js             # 声明/注册/销毁/epoch
│   ├─ runtime.js              # inject/register 的 React 绑定
│   └─ renderer.js             # SlotRenderer 安装合约
│
├─ runtime（front/src/runtime）
│   ├─ BackendRuntime.js       # 当前后端节点/连接状态
│   ├─ PeerRuntime.js          # 对端列表/传输状态
│   ├─ StorageRuntime.js       # 本地/远端文件状态
│   └─ AuthRuntime.js          # 用户/权限状态
│
├─ bundles/*/client.js         # 每个前端插件的浏览器入口
│
└─ transport（front/src/transport）
    ├─ api-client.js           # HTTP POST unary
    ├─ events.js               # WebSocket downlink
    └─ trust.js                # Host/Origin 校验
```

### 3.1 后端生成 boot graph

`back/bundles/web` 在启动时扫描“已启用的 `peerdrive.client` 行”，生成：

```html
<script>
window.__PEERDRIVE_BOOT__ = {
  entries: [
    { id: 'core', src: '/plugins/core/client.js', immediate: true },
    { id: 'storage', src: '/plugins/storage/client.js', immediate: true },
    { id: 'transport', src: '/plugins/transport/client.js', immediate: false },
    { id: 'legacy', src: '/plugins/legacy/client.js', immediate: false },
  ]
}
</script>
```

每个 `front/bundles/<name>/client.js` 是由 Vite 构建出的独立 bundle，后端在 `/plugins/<name>/client.js` 提供并带 source map。

### 3.2 前端两阶段启动

```js
// front/src/shell/AppWebEntry.js
export async function run(root) {
  const boot = window.__PEERDRIVE_BOOT__

  // Stage 1: module face
  const loader = createClientModuleLoader()
  await Promise.all(boot.entries.filter(e => e.immediate).map(e => loader.prefetch(e)))

  // Stage 2: plugin face
  const entries = boot.entries.map(e => createEntry(e, loader))
  await settle(entries)

  renderRoot(root, entries)
}
```

### 3.3 模块系统

```js
// front/src/modules/load.js
export function load(specifier) {
  if (seedTable[specifier]) return seedTable[specifier]
  const cached = loadCache.get(specifier)
  if (cached) return cached

  // 从 boot graph 找到对应 entry，加载外部 script，再 materialize
  const entry = graph.get(specifier)
  const factory = await prefetch(entry)
  const exports = factory(createRequire())
  loadCache.set(specifier, exports)
  return exports
}
```

一个 bundle 的 `client.js` 只做：

```js
// front/src/bundles/transport/client.js
import { registerBundle } from '../modules/registry.js'

registerBundle('transport', (require) => {
  const { apply } = require('./apply.js')
  return { apply }
})
```

真正组件/API/副作用都在 `apply` factory 内部，加载时不执行。

### 3.4 Slot 设计

Peerdrive 初始 slot：

```text
root
├── topbar
│   ├── nav
│   ├── status
│   └── user
├── content
│   ├── routes          # 路由容器
│   └── dashboard       # Dashboard 卡片
├── sidebar
├── mobileNav
└── settings
    ├── general
    ├── backends
    ├── transport
    └── discovery
```

业务插件示例：

```js
export function apply(ctx) {
  ctx.slots.inject('routes', () => [
    ctx.slots.register(
      { name: 'transport.panel', route: '/p2p', ... },
      P2PPanel
    )
  ])

  ctx.slots.inject('topbar', () => [
    ctx.slots.register({ name: 'transport.status', order: 20 }, P2PStatus)
  ])

  ctx.api.register('transport', transportApi)
  ctx.runtime.register('peer', createPeerRuntime(ctx.connection))
}
```

slot 的 `register` 仍然是“一个组件 + 子 slot + store + inject 依赖”的组合声明，
与 dsh `SlotCore` 语义一致。

### 3.5 路由不是全局数组，而是 slot 贡献

- `routes` 只是一个 slot。
- 每个 bundle 往 `routes` 插槽注册自己的路由组件。
- shell/layout 的 AppFrame 渲染 `routes` slot。
- 这样“路由表”不是手工维护的数组，而是 slot 树的一部分；
  但为了调试和 profile patch，可以再投影一份扁平路由清单。

---

## 4. /api 与连接层

Peerdrive 前端可以复刻 dsh 的 `/api` 桥模式：

| 能力 | 设计 |
|---|---|
| Unary API | `POST /api/{method}`，JSON body，统一鉴权/错误封装 |
| 文件下载大流 | 仍可用普通 HTTP GET（浏览器原生下载），不走 JSON bridge |
| 事件推送 | `WS /api/events` 只下行，推送 peer 状态/同步进度/任务状态 |
| 本地 WS 会话 | 保留 `WS /ws/peer` 作为本地节点直连帧协议通道 |
| Trust fence | 对所有 `/api` 和 WS 升级校验 Host/Origin，只允许 loopback + `trustedHosts` |

这样浏览器端不是散装 fetch 一堆 REST，而是：

```js
const result = await ctx.api.call('file.get', { hash })
const events = ctx.connection.events('peer')
```

dsh 用 `/api/events.mux` + `/api/events.host` 两条 downlink；Peerdrive 可以先合并成一条 `/api/events`，
后续再按需拆分。

---

## 5. 静态服务与 PWA

`back/bundles/web` 的静态服务组件负责：

- 只服务 `front/dist/`。
- `/plugins/<name>/client.js` 提供各前端插件 bundle。
- 任意 SPA miss 回退 `index.html` 并注入 `window.__PEERDRIVE_BOOT__`。
- 路径穿越 403。
- 支持 PWA manifest / service-worker 已有资源。

---

## 6. 与之前“Manifest+Registry”设计的关系

`FRONTEND-DSH-INSPIRED.md` 描述的是“如何把业务按 bundle 组织”；
本文描述的是“浏览器运行时如何加载这些 bundle”。

两者可以分阶段落地：

1. **MVP**：用 Vite `import.meta.glob` + profile 在构建期合成 manifest，
   `harness/compose.js` 生成路由/导航/Provider。实现快，能先拆模块。
2. **目标态**：改为 dsh 式 host 推送 boot graph + 浏览器模块系统 + slot 注入，
   前后端插件完全解耦，后端可独立决定哪些前端插件被启用。

---

## 7. 测试策略

| 层次 | 测试 |
|---|---|
| Shell | 加载 boot graph、prefetch 失败显示 loading、settle 后才渲染 |
| Module loader | factory 注册/materialize 缓存/invalidate/循环依赖报错 |
| SlotCore | 声明/注册/销毁/重复注册错误/epoch 递增 |
| Runtime | 各 bundle Provider 订阅与事件分发 |
| Frontend bundle | 组件测试 + `apply(ctx)` 的 slot 注入测试 |
| Backend web bundle | `/plugins/*/client.js` 正确输出、SPA fallback、trust fence |

---

## 8. 实施顺序

1. 先实现 `front/src/shell` + `front/src/modules`，能消费静态 `window.__PEERDRIVE_BOOT__`。
2. 把 `front/dist` 构建拆成每 bundle 一个 `client.js`，后端 `/plugins/` 提供。
3. 实现 `SlotCore` 最小版：`root` + `routes` + `topbar` + `sidebar` + `settings`。
4. 把现有页面逐步改成 `apply(ctx)` 插件，而不是直接 import 到 `App.jsx`。
5. 后端 web bundle 扫描 `peerdrive.client` 行生成 boot graph。
6. 最后接入 `/api` 桥和 trust fence，统一浏览器到后端的传输层。
