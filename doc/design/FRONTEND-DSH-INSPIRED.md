# Peerdrive 前端参考 dsh 的组合式设计

> 日期：2026-08-16
> 范围：专门设计 Peerdrive 前端（React + Vite）如何参照 DeepSeek Harness（`dsh`）的
> **profile / bundle / patch / plugin-row** 模型进行模块化组合。
> 后端组合设计见 `doc/design/PEERDRIVE-DSH-INSPIRED.md`。

---

## 1. 当前前端的问题

当前 `front/` 是典型单体：

```text
src/
├── App.jsx              # 手工 import 所有页面，路由全集中在这里
├── api.js               # 所有 HTTP API 全在单个文件
├── components/          # 跨页面组件，实际边界模糊
├── pages/               # 页面目录，但页面之间互相 import
└── storage/             # 本地 localStorage 同步逻辑
```

问题：

| 问题 | 表现 |
|---|---|
| 路由是手工维护的全局清单 | 每加一个模块都要改 `App.jsx` |
| API 是单文件巨石 | `api.js` 同时包含文件、合集、P2P、BT、IPFS、设置等 |
| 菜单/侧栏/底部栏没有插槽机制 | 每加一个页面都要改 `Navbar` / `MobileNav` |
| 上下文状态集中 | `AppContext` 里塞了 username/nodeInfo，模块状态无处安放 |
| 前端模块无法独立分支开发 | 一个功能改动会触碰 `App.jsx`、`api.js`、多个页面 |
| 没有 profile 概念 | 无法做“纯 Web 控制台”“纯 P2P 面板”“只读浏览”等裁剪 |

---

## 2. 设计目标

参考 dsh 的插件组合心智模型，让前端变成：

```text
一个前端内核（kernel/harness）
  + 多个有序 front bundle（core/auth/storage/sync/transport/discovery/web）
  + 一个 profile（选择启用的 bundle）
  + 可选 patch 层（覆盖路由、菜单、配置、禁用模块）
```

关键能力：

- 每个功能模块有独立的 `front/bundles/<name>/`，自带路由、组件、API、测试。
- `App.jsx` 不再手工 import；由 `harness/boot()` 根据 profile 合成。
- 路由、导航项、设置项、Dashboard 卡片都通过“插槽 + 注册表”组合。
- 每个 bundle 有独立测试；profile 组合后仍有集成测试。
- 与后端的 `back/bundles/<name>` 一一对应，实现前后端成对开发/合并。

---

## 3. 目标目录结构

```
front/
├── profiles/
│   ├── web.js              # 完整 Web 形态：所有业务 bundle + web UI
│   ├── lite.js             # 轻量形态：core + storage + 浏览
│   └── dev.js              # 开发形态：所有 bundle + HMR/调试
├── src/
│   ├── main.jsx            # 入口：读取 profile -> boot()
│   ├── harness/            # 前端组合内核（类似 dsh 的 client kernel）
│   │   ├── boot.js
│   │   ├── compose.js
│   │   ├── RouteRegistry.js
│   │   ├── NavRegistry.js
│   │   ├── SlotRegistry.js
│   │   ├── ApiRegistry.js
│   │   ├── ProviderRegistry.js
│   │   └── patch.js
│   ├── core/               # 前端基座，对应 back/bundles/base
│   │   ├── api/            # request/auth/backend 管理
│   │   ├── context/        # 基础 Context
│   │   ├── components/     # 布局、主题、通用组件
│   │   ├── pages/          # 基础页面（首页/空态/设置壳）
│   │   └── storage/        # localStorage 基础封装
│   ├── bundles/            # 功能 bundle（与 back/bundles 同名）
│   │   ├── core/           # 核心 UI：Navbar/MobileNav/路由容器
│   │   ├── auth/           # 登录、用户选择、权限 UI
│   │   ├── storage/        # 文件管理、上传/下载、合集
│   │   ├── sync/           # 同步状态、本地/远端同步 UI
│   │   ├── transport/      # P2P 面板、节点状态、WebRTC 传输
│   │   ├── discovery/      # 发现/信令配置、节点扫描 UI
│   │   ├── web/            # Web 专属外壳：设置、PWA、信任配置
│   │   └── legacy/         # BT/IPFS/DHT 等旧模块 UI，默认不启用
│   └── index.css
└── tests/                  # 跨 bundle 集成测试
```

> 说明：`src/bundles/` 是实际业务代码，`src/core/` 是内核和基座。
> 每个 bundle 不直接修改其他 bundle；只是把自己的 manifest 暴露给 harness。

---

## 4. 前端内核（`src/harness`）

### 4.1 Bundle Manifest

每个前端 bundle 根目录放一个 `manifest.js`：

```js
// front/src/bundles/transport/manifest.js
export default {
  id: 'transport',
  version: '1.0.0',

  // 依赖：启动顺序提示，不是强加载顺序
  deps: ['core', 'storage'],

  // 路由注册：以 id 为 key，后层可覆盖/禁用
  routes: [
    {
      id: 'transport.panel',
      path: '/p2p',
      element: () => import('./pages/P2PPanel.jsx'),
      nav: { label: 'P2P', icon: 'network', order: 30 },
    },
    {
      id: 'transport.dashboard',
      path: '/p2p/dashboard',
      element: () => import('./pages/P2PDashboard.jsx'),
      nav: null, // 不在主导航显示
    },
  ],

  // 布局插槽：往 navbar/settings/dashboard 等位置插入组件
  slots: {
    navbar: [
      { id: 'transport.status', component: () => import('./components/P2PStatus.jsx'), order: 20 },
    ],
    settings: [
      { id: 'transport.settings', component: () => import('./components/TransportSettings.jsx'), order: 30 },
    ],
  },

  // API 模块：按命名空间合并
  api: () => import('./api.js'),

  // 状态 Provider：包在应用外层
  providers: [
    { id: 'transport.provider', component: () => import('./state/TransportProvider.jsx') },
  ],
}
```

### 4.2 Profile

```js
// front/profiles/web.js
export default {
  name: 'web',
  bundles: [
    'core',
    'auth',
    'storage',
    'sync',
    'transport',
    'discovery',
    'web',
  ],
  patch: [
    // 覆盖某个已有行：整行配置替换
    { id: 'transport.panel', config: { enabled: false } },
    // 或插入自定义路由
    { insert: {
        id: 'local.filepicker',
        path: '/files',
        element: () => import('@/custom/FilePicker.jsx'),
        nav: { label: '文件', order: 10 },
    } },
  ],
}
```

### 4.3 Boot 流程

```js
// src/main.jsx
import profile from '../profiles/web.js'
import { boot } from './harness/boot.js'
import './index.css'

boot(profile)
```

`boot()` 内部：

1. 收集 profile 里启用的 bundle manifests（Vite `import.meta.glob` + profile 过滤）。
2. 按 `compose.js` 合并每层的 routes / slots / providers / api。
3. 应用 profile.patch 与 `import.meta.env` 环境 patch。
4. 生成：
   - `RouteRegistry` → 渲染 `<Routes>`
   - `NavRegistry` → 渲染 `Navbar` / `MobileNav` 的菜单
   - `ProviderRegistry` → 包成 `<Providers>`
   - `ApiRegistry` → 给每个页面提供 `api.<namespace>`
5. 渲染 `<App />`。

---

## 5. 关键注册表设计

### 5.1 RouteRegistry

- 每个路由有唯一 `id`。
- 后层 patch 可以用 `{ id: 'auth.login', disabled: true }` 禁用路由。
- 可以用 `{ id: 'auth.login', path: '...', element: ... }` 覆盖整行。

```js
const routes = compose(
  bundle('core').routes,
  bundle('storage').routes,
  bundle('transport').routes,
  profile.patch,
)
```

合并结果：

```js
[
  { id: 'core.home', path: '/', element: <Plaza/> },
  { id: 'storage.manager', path: '/files', element: <FileManager/> },
  { id: 'transport.panel', path: '/p2p', element: <P2PPanel/> },
  ...
]
```

### 5.2 NavRegistry / SlotRegistry

- 导航项不是“页面自己声明”，而是路由的 `nav` 字段或显式 slot 组件。
- 组件只往 slot 里插入，不决定最终位置；由 profile patch 调整 `order`/`visible`。
- 例如移动端导航 `MobileNav` 只是读取 `NavRegistry.slots.mobile`。

### 5.3 ApiRegistry

每个 bundle 的 `api.js` 只描述本模块的 API：

```js
// front/src/bundles/storage/api.js
import { request } from '../../core/api/request.js'

export default {
  upload: (file) => request('POST', '/files/upload', file),
  list: () => request('GET', '/files'),
  createCollection: (entries) => request('POST', '/collections', { entries }),
}
```

`ApiRegistry` 最后暴露：

```js
const api = {
  core: ...,
  storage: ...,
  transport: ...,
  auth: ...,
}
```

页面不再直接 import 巨石 `api.js`，而是通过 `useApi()` 获取自己 bundle 的 API。

### 5.4 ProviderRegistry

- 基础 Provider：`AppProvider`（core）、`BackendProvider`（core）、`AuthProvider`（auth）。
- 功能 Provider：`TransportProvider`、`SyncProvider`、`StorageProvider`。
- harness 按 bundle 顺序叠加，profile patch 可禁用某个 Provider。

---

## 6. 前端 bundle 职责拆分

| bundle | 主要页面/组件 | API 命名空间 |
|---|---|---|
| `core` | `Navbar`、`MobileNav`、首页、布局、主题、API client、多后端管理 | `core` |
| `auth` | 登录、用户选择、权限/可见性、访问列表 | `auth` |
| `storage` | 文件管理、上传/下载、合集创建/浏览 | `storage` |
| `sync` | 同步状态、本地缓存、与节点同步 | `sync` |
| `transport` | P2P 面板、节点状态、WebRTC 传输、拓扑 | `transport` |
| `discovery` | 信令/发现配置、节点扫描 | `discovery` |
| `web` | 设置页、PWA、浏览器信任配置 | `web` |
| `legacy` | BT DHT、IPFS、DHT Explorer 等旧面板 | `legacy` |

每个 bundle 内部结构建议：

```
front/src/bundles/<name>/
├── manifest.js
├── api.js
├── pages/           # 页面组件
├── components/      # 该模块的组件
├── state/           # Provider / hooks
├── index.js         # 可选：供其他 bundle import 的公共 API
└── __tests__/       # Vitest 测试
```

---

## 7. Patch 覆盖示例

### 7.1 禁用旧模块

```js
// front/profiles/web.js patch
[
  { id: 'legacy.bt', disabled: true },
  { id: 'legacy.ipfs', disabled: true },
]
```

### 7.2 部署环境覆盖

```js
// front/profiles/dev.js
export default {
  name: 'dev',
  bundles: ['core', 'auth', 'storage', 'sync', 'transport', 'discovery', 'web', 'legacy'],
  patch: [
    { id: 'core.backends', config: { defaults: [{ id: 'local', name: 'Local', url: 'http://localhost:3000' }] } },
  ],
}
```

### 7.3 自定义导航

```js
patch: [
  { id: 'core.navbar', config: { order: ['home', 'files', 'p2p', 'settings'], hidden: ['legacy.bt'] } },
]
```

---

## 8. 与后端 bundle 的对应关系

一个功能分支同时包含：

```text
feat/transport
├── back/bundles/transport/     # Go 后端：PeerJS/WS/帧协议
├── front/bundles/transport/    # React 前端：P2P 面板/节点状态
├── profiles/web/peerdrive.yml  # 后端启 transport
└── front/profiles/web.js        # 前端启 transport
```

后端 profile 决定“有哪些 API”，前端 profile 决定“有哪些 UI”。
两者通过同一份 `bundles` 列表保持同步，避免前后端模块错位。

---

## 9. 测试策略

每个前端 bundle 必须包含：

- **manifest 测试**：routes/slots/providers id 唯一、deps 存在。
- **组件测试**：Vitest + Happy DOM + Testing Library，只测本 bundle。
- **API mock**：`front/src/__mocks__/api.js` 可以按 bundle 拆分，不再全局 mock。
- **profile 集成测试**：用 `compose(profile)` 快照断言最终路由/导航/Provider 树。

CI 中：

- 每个模块分支跑自己的 bundle 测试。
- 合并到 main 后跑所有 bundle + profile 集成测试。
- 构建 `front/dist` 作为后端 `web` bundle 的静态资源。

---

## 10. 迁移路径

| 阶段 | 动作 | 验收 |
|---|---|---|
| F0 | 建 `front/src/harness/`，先支持 `core` bundle，`App.jsx` 改为读 manifest | 现有 UI 不变，路由仍全绿 |
| F1 | 把 `api.js` 按命名空间拆到各 bundle，保留 `core/api/request.js` 公共封装 | 所有页面 API 调用正常 |
| F2 | 把页面/组件按 storage/auth/transport/discovery/legacy 移入 `front/src/bundles/` | 路由/导航由 harness 合成，不再手工改 `App.jsx` |
| F3 | 按需引入 `profiles/web.js`、`profiles/lite.js`，支持裁剪 | 不同 profile 可构建不同 UI |
| F4 | 前端 bundle 的 CI 与后端 bundle 对齐 | 每个模块分支前后端一起绿 |

---

## 11. 关键决策与理由

1. **前端不照搬 dsh 的运行时动态加载**
   - Vite 是构建期打包，无法像 Node 一样运行时加载任意包。
   - 用 `import.meta.glob` + profile 过滤实现“构建期组合”，行为可预测、可 tree-shake。

2. **路由集中到注册表，但页面代码留在 bundle**
   - 避免“所有路由写在一个文件”，同时防止“路由分散到难以审计”。
   - 注册表是唯一的路由事实来源，patch 能覆盖/禁用。

3. **导航/插槽不直接写在页面里**
   - 页面只自己注册组件，最终布局由 profile 决定。
   - 这样移动端/桌面端/嵌入式可以共享同一套 bundle。

4. **前后端 bundle 同名**
   - `front/bundles/transport` ↔ `back/bundles/transport`。
   - 一个模块的 API、UI、测试、CI 全在同一个分支，符合“按模块开发前后端再合并”的要求。

---

## 12. 待办 / 后续细化

- [ ] 定义 `manifest.js` 的完整 schema（routes/slots/providers/api/deps）
- [ ] 实现 `harness/compose.js` 的 id 覆盖与禁用语义
- [ ] 实现 `NavRegistry` / `SlotRegistry` 的渲染 API
- [ ] 把现有 `api.js` 拆成 `core/api/request.js` + 各 bundle api
- [ ] 把 `App.jsx` 改造成纯组合结果，删除手工 import
- [ ] 为每个 bundle 生成 `manifest.test.js` 与 CI job
