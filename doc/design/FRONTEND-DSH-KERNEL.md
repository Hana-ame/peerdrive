
# Peerdrive Frontend Architecture Inspired by dsh's Frontend Kernel

> Date: 2026-08-16
> This document specifically references DeepSeek Harness (dsh)'s **web frontend architecture itself**:
> `dsh-client-web` (shell kernel), `dsh-client-modules` (browser module system),
> `dsh-client-ui-slots` (slot registry), `dsh-client-connection` (/api transport bridge),
> `dsh-host-frontend-static` (SPA static serving).
> The goal is to have Peerdrive's frontend also adopt the **host-pushed boot graph + browser shell two-stage startup + plugin lazy loading + slot-composed UI** mechanism.
> See `FRONTEND-DSH-INSPIRED.md` for the higher-level bundle/profile/patch organization.


> ⚠️ **The paths in this document are design proposals, not current state** (written 2026-08-16,
> paths such as `bundles/` `profiles/` `harness/` `front/src/modules` have **not been implemented to this day**;
> currently `back/` has no `bundles/` or `harness/`, and `front/src/` has only `pages/` `lib/` `api.js` `ws.js`).
> This document records "what the target structure looks like"; to see the actual structure, see
> [`doc/FILE-REFERENCE.md`](../FILE-REFERENCE.md). Paths were deliberately **not** modified —
> changing them would misrepresent the original proposal.
---

## 1. Key Mechanisms of dsh's Frontend

### 1.1 Host-Pushed Boot Graph

dsh does not let the frontend import all modules itself. Instead, the **Node/Host side** scans enabled plugin rows (`dsh.client` rows),
compiles the browser plugin manifest into a boot graph, and injects it into the page:

```js
window.__DSH_BOOT__ = {
  entries: [
    { id: 'core', src: '/plugins/core/client.js', immediate: true },
    { id: 'connection', src: '/plugins/connection/client.js', immediate: true },
    { id: 'ui-layout', src: '/plugins/ui-layout/client.js', immediate: true },
    // ... rest loaded on demand
  ]
}
```

The frontend shell does not decide "which plugins to load" — it only executes the graph provided by the host.

### 1.2 Two-Stage Startup (web2)

1. **Stage 1 - module face**:
   - Create the browser-side module system (`ClientModuleLoader`).
   - Parallel-prefetch `immediate`-tier bundle scripts.
   - When executing bundle scripts, **only register factories**, do not execute components/side effects;
     styles and other side effects are executed only when the factory is materialized.

2. **Stage 2 - plugin face**:
   - Inject the module system into the Loader.
   - Create a loader entry for each row in the boot graph.
   - Wait for loader to go silent + all entry fibers ACTIVE, then render the complete UI at once.

Benefits of this approach:
- Plugins don't need external ordering; dependencies are materialized on demand through the module system.
- A plugin loading failure doesn't crash the core shell.
- First paint can load only the immediate tier; other routes/panels are lazy-loaded.

### 1.3 Slot Registry

dsh's UI composition core is not a "Route array" but the **SlotRegistry**:

- `SlotCore` starts with only `root` as a preset slot.
- Each plugin gets `ctx.slots` via `apply(ctx)`.
- Plugins can use `ctx.slots.inject(name, callback)` to wait for a slot to be declared.
- Plugins use `slots.register({ name, children?, store?, inject? }, Component)` to contribute their component into a slot and simultaneously declare child slots.

Example: a layout plugin declares:

```ts
ctx.slots.register({ name: 'root', children: ['sidebar', 'conversation', 'details'] }, AppFrame)
```

A business plugin contributes:

```ts
ctx.slots.inject('sidebar', () =>
  ctx.slots.register({ name: 'p2p', ... }, P2PStatus)
)
```

Benefits:
- UI is no longer built by "manually arranging Navbar/pages";
- Each plugin only declares which slot it contributes to;
- The final layout is composed from the slot tree.

### 1.4 /api Browser Transport Bridge

dsh's browser does not directly fetch a bunch of REST URLs but goes through a unified `/api` bridge:

- **Unary / respond**: HTTP POST.
- **Events/downlink**: `/api/events.mux`, `/api/events.host` — one WebSocket each, downlink only.
- **Host description**: After ready, `host.describe` tells the browser what the current Host is.
- **Trust fence**: All `/api` requests first verify the Host header, allowing only loopback or `trustedHosts`,
  otherwise 403. This is the browser trust boundary against DNS-rebinding / CSWSH.

### 1.5 SPA Static Serving

`dsh-host-frontend-static` acts as the webserver's fallback seat:

- Only serves the built `dist/`.
- Path traversal returns 403.
- All misses fall back to `index.html` (SPA routing).
- Every index response executes index taps, injecting the boot graph.

---

## 2. Peerdrive Frontend Mapping

| dsh Frontend | Peerdrive Frontend |
|---|---|
| `window.__DSH_BOOT__` | `window.__PEERDRIVE_BOOT__` |
| `dsh-client-web` shell kernel | `front/src/shell/` |
| `dsh-client-modules` module system | `front/src/modules/` |
| `dsh-client-ui-slots` slot registry | `front/src/slots/` |
| `dsh-client-runtime` runtime object | `front/src/runtime/` |
| `dsh-client-connection` /api bridge | `front/src/transport/` + `back/bundles/web` |
| `dsh-host-frontend-static` | `back/bundles/web` static serving component |
| `/plugins/<id>/client.js` | `/plugins/<name>/client.js` |
| `dsh.client` row | `peerdrive.client` row |
| `ctx.slots` | `ctx.slots` (same concept) |
| `ctx.connection` | `ctx.api` / `ctx.connection` |

---

## 3. Target Frontend Architecture

```
Browser
│
├─ shell (front/src/shell)
│   ├─ AppWebEntry.js          # Reads __PEERDRIVE_BOOT__, two-stage startup
│   ├─ boot-status.js          # Loading status, does not depend on plugins
│   └─ module-loader.js        # ClientModuleLoader
│
├─ modules (front/src/modules)
│   ├─ load.js                 # Importer registry
│   ├─ cache.js                # Module cache
│   └─ factory.js              # Factory materialization
│
├─ slots (front/src/slots)
│   ├─ SlotCore.js             # Root slot, declare/inject/register
│   ├─ layout.js               # Preset layout slots (sidebar/topbar/details)
│   └─ inject.js               # Slot injection API
│
├─ runtime (front/src/runtime)
│   ├─ ProviderRegistry.js     # Bundle Provider subscription
│   ├─ store.js                # Cross-plugin state store
│   └─ events.js               # Event bus
│
├─ transport (front/src/transport)
│   ├─ api-bridge.js           # /api bridge (mirrors dsh-client-connection)
│   ├─ ws-events.js            # /api/events.mux / /api/events.host
│   └─ trust.js                # Host header trust fence
│
└─ bundles/ (per bundle: client.js, manifest.js, pages, components)
```

---

## 4. Boot Graph Protocol

### 4.1 Boot Graph Structure

```js
window.__PEERDRIVE_BOOT__ = {
  version: '2.0',
  host: {
    name: 'peerdrive-node-01',
    version: '1.2.3',
    trust: { allowedOrigins: ['localhost', '127.0.0.1'] },
  },
  entries: [
    { id: 'core', src: '/plugins/core/client.js', immediate: true },
    { id: 'connection', src: '/plugins/connection/client.js', immediate: true },
    { id: 'ui-layout', src: '/plugins/ui-layout/client.js', immediate: true },
    { id: 'transport', src: '/plugins/transport/client.js', immediate: false },
    { id: 'storage', src: '/plugins/storage/client.js', immediate: false },
    { id: 'discovery', src: '/plugins/discovery/client.js', immediate: false },
  ],
}
```

### 4.2 Plugin Contract

Each plugin's `client.js` exports an `apply` function:

```ts
// front/src/bundles/transport/client.js
import P2PPanel from './pages/P2PPanel.jsx'
import TransportNavBadge from './components/TransportNavBadge.jsx'

export default {
  id: 'transport',
  version: '1.0.0',

  apply(ctx) {
    // Register routes
    ctx.routes.register('transport.panel', {
      path: '/p2p',
      element: P2PPanel,
      nav: { label: 'P2P', icon: 'network', order: 30 },
    })

    // Inject into slots
    ctx.slots.inject('sidebar', () => {
      ctx.slots.register('p2p-status', TransportNavBadge)
    })

    // Register API endpoints
    ctx.api.register({
      webrtcInfo: 'GET /p2p/webrtc/info',
      nodes: 'GET /peerjs/nodes',
    })

    // Register Provider
    ctx.providers.register('TransportProvider', { component: TransportProvider })
  },
}
```

### 4.3 Host-Side Generation

The backend web bundle scans `peerdrive.client` rows and generates the boot graph:

```go
// back/bundles/web/bootgraph.go
func GenerateBootGraph(configs []*clientConfig) map[string]interface{} {
    entries := make([]map[string]interface{}, 0)
    for _, cfg := range configs {
        entries = append(entries, map[string]interface{}{
            "id":        cfg.ID,
            "src":       fmt.Sprintf("/plugins/%s/client.js", cfg.ID),
            "immediate": cfg.Immediate,
        })
    }
    return map[string]interface{}{
        "version": "2.0",
        "host":    map[string]interface{}{"name": hostname, "version": version},
        "entries": entries,
    }
}
```

The host injects the boot graph into `index.html` via index taps before serving the SPA.

---

## 5. Two-Stage Startup Implementation

### 5.1 Stage 1: Module Face

```js
// front/src/shell/module-loader.js
export class ClientModuleLoader {
  constructor() {
    this.modules = new Map()    // id -> { factory, status }
    this.importers = new Map()  // specifier -> module
    this.factories = new Map()  // id -> factory function
  }

  registerModule(id, factory) {
    // Stage 1 only registers factories, does NOT execute them
    this.factories.set(id, { factory, status: 'registered' })
  }

  materialize(id) {
    const entry = this.factories.get(id)
    if (!entry) throw new Error(`Module ${id} not registered`)
    if (entry.status === 'materialized') return entry.module

    entry.status = 'materializing'
    const module = entry.factory(this)
    entry.module = module
    entry.status = 'materialized'
    return module
  }

  async prefetch(urls) {
    // Parallel import of immediate-tier scripts
    await Promise.all(urls.map(url => import(url)))
  }
}
```

### 5.2 Stage 2: Plugin Face

```js
// front/src/shell/AppWebEntry.js
export async function boot() {
  const boot = window.__PEERDRIVE_BOOT__
  if (!boot) {
    document.getElementById('app').innerHTML = '<h1>No boot graph found</h1>'
    return
  }

  // Show loading status
  showBootStatus('Loading core modules...')

  // Stage 1: Prefetch immediate modules
  const loader = new ClientModuleLoader()
  await loader.prefetch(boot.entries.filter(e => e.immediate).map(e => e.src))
  showBootStatus('Initializing plugins...')

  // Stage 2: Create loader entries and materialize
  const ctx = createRuntimeContext(loader)
  for (const entry of boot.entries) {
    // Each entry already imported in Stage 1; now materialize
    const mod = loader.materialize(entry.id)
    if (mod && mod.default && typeof mod.default.apply === 'function') {
      await mod.default.apply(ctx)
    }
  }

  // All entries active → render
  showBootStatus('Rendering UI...')
  renderApp(ctx)
}
```

---

## 6. Slot System Design

### 6.1 SlotCore

```js
// front/src/slots/SlotCore.js
export class SlotCore {
  constructor() {
    this.slots = new Map()       // name -> { component, children, store, injectors }
    this.injectors = new Map()   // name -> Set<callback>
    this.epoch = 0               // Increment on every structural change
  }

  register(name, component, opts = {}) {
    this.slots.set(name, {
      component,
      children: opts.children || [],
      store: opts.store || null,
      injectors: this.injectors.get(name) || new Set(),
    })
    this.epoch++
    return this
  }

  inject(name, callback) {
    if (!this.injectors.has(name)) this.injectors.set(name, new Set())
    this.injectors.get(name).add(callback)

    // If slot already registered, invoke immediately
    const slot = this.slots.get(name)
    if (slot) callback(slot)
    return this
  }

  unregister(name) {
    this.slots.delete(name)
    this.epoch++
    return this
  }

  get(name) {
    return this.slots.get(name)
  }
}
```

### 6.2 Layout Slot Tree

Preset slots form a tree that defines the app frame:

```
root
├── topbar
├── sidebar
│   ├── nav
│   └── p2p-status (injected by transport bundle)
├── conversation (main content area)
│   └── routes (injected by RouteRegistry)
└── details (right panel, optional)
```

Layout plugin registers:

```ts
ctx.slots.register({
  name: 'root',
  children: ['topbar', 'sidebar', 'conversation', 'details'],
}, AppFrame)
```

Business plugins contribute into leaf slots via `inject`.

---

## 7. Comparison: dsh Web Kernel vs Peerdrive Frontend

| dsh Web Kernel | Peerdrive Frontend |
|---|---|
| `window.__DSH_BOOT__` | `window.__PEERDRIVE_BOOT__` |
| Host pushes boot graph | Backend web bundle generates boot graph |
| `ClientModuleLoader` | `front/src/shell/module-loader.js` |
| Two-stage: module face → plugin face | Same two-stage approach |
| `SlotCore` with root preset | Same slot registry pattern |
| Plugin `apply(ctx)` | Same plugin contract |
| `ctx.slots.inject/register` | Same slot API |
| `ctx.connection` for /api bridge | `ctx.api` / `ctx.connection` |
| `/api/events.mux` WebSocket | Same event bus pattern |
| Trust fence on Host header | Same trust fence in `/api` bridge |
| `dsh-host-frontend-static` fallback | `back/bundles/web` static serving |

---

## 8. From Monolith to Composable

The key architectural shift:

**Monolith approach**:
```
App.jsx → import all pages → hard-coded routes → hard-coded nav → hard-coded slots
```

**Composable approach**:
```
Backend decides which plugins to enable
  → Generates boot graph with entries + immediate flags
  → Injects into window.__PEERDRIVE_BOOT__
  → Frontend shell reads boot graph
  → Two-stage startup:
      Stage 1: Prefetch immediate modules, register factories only
      Stage 2: Materialize plugins via apply(ctx), slot composition
  → Final UI composed from slot tree
```

**Key principle**: The frontend shell does not decide what to load — the host does.
This separates "what is available" (backend decision) from "how to compose" (frontend shell).

---

## 9. Implementation Order

1. Implement `front/src/shell` + `front/src/modules`, able to consume static `window.__PEERDRIVE_BOOT__`.
2. Split `front/dist` build into per-bundle `client.js`, served by backend `/plugins/`.
3. Implement minimal `SlotCore`: `root` + `routes` + `topbar` + `sidebar` + `settings`.
4. Gradually convert existing pages from direct `App.jsx` imports to `apply(ctx)` plugins.
5. Backend web bundle scans `peerdrive.client` rows to generate boot graph.
6. Finally wire up `/api` bridge and trust fence, unifying the browser-to-backend transport layer.
