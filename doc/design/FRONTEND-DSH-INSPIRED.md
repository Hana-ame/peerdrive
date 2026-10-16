
# Peerdrive Frontend Modular Composition Inspired by dsh

> Date: 2026-08-16
> Scope: Specifically designs how Peerdrive's frontend (React + Vite) should adopt DeepSeek Harness (`dsh`)'s **profile / bundle / patch / plugin-row** model for modular composition.
> See `doc/design/PEERDRIVE-DSH-INSPIRED.md` for the backend composition design.


> ⚠️ **The paths in this document are design proposals, not current state** (written 2026-08-16,
> paths such as `bundles/` `profiles/` `harness/` `front/src/modules` have **not been implemented to this day**;
> currently `back/` has no `bundles/` or `harness/`, and `front/src/` has only `pages/` `lib/` `api.js` `ws.js`).
> This document records "what the target structure looks like"; to see the actual structure, see
> [`doc/FILE-REFERENCE.md`](../FILE-REFERENCE.md). Paths were deliberately **not** modified —
> changing them would misrepresent the original proposal.
---

## 1. Current Frontend Problems

The current `front/` is a typical monolith:

```text
src/
├── App.jsx              # Manually imports all pages, routes are all centralized here
├── api.js               # All HTTP APIs in a single file
├── components/          # Cross-page components, but boundaries are actually vague
├── pages/               # Page directory, but pages import each other
└── storage/             # Local localStorage sync logic
```

Problems:

| Problem | Symptom |
|---|---|
| Routes are a manually maintained global list | Adding a module requires editing `App.jsx` |
| API is a single-file monolith | `api.js` simultaneously contains files, collections, P2P, BT, IPFS, settings, etc. |
| No slot mechanism for menu/sidebar/bottom bar | Every new page requires editing `Navbar` / `MobileNav` |
| Context state is centralized | `AppContext` crams username/nodeInfo; module state has no place to live |
| Frontend modules cannot be developed in independent branches | A single feature change touches `App.jsx`, `api.js`, and multiple pages |
| No profile concept | Cannot create trimmed variants like "pure web console", "pure P2P panel", "read-only browser" |

---

## 2. Design Goals

Referring to dsh's plugin composition mental model, turn the frontend into:

```text
One frontend kernel (kernel/harness)
  + Multiple ordered front bundles (core/auth/storage/sync/transport/discovery/web)
  + One profile (selects which bundles to enable)
  + Optional patch layer (overrides routes, menus, config, disables modules)
```

Key capabilities:

- Each feature module has its own `front/bundles/<name>/`, with routes, components, API, and tests.
- `App.jsx` no longer manually imports; `harness/boot()` synthesizes it based on profile.
- Routes, nav items, settings, Dashboard cards are all composed via "slots + registries".
- Each bundle has independent tests; profiles have integration tests after composition.
- Corresponds one-to-one with backend `back/bundles/<name>`, enabling paired frontend/backend development/merging.

---

## 3. Target Directory Structure

```
front/
├── profiles/
│   ├── web.js              # Full web form: all business bundles + web UI
│   ├── lite.js             # Lightweight form: core + storage + browsing
│   └── dev.js              # Dev form: all bundles + HMR/debugging
├── src/
│   ├── main.jsx            # Entry: reads profile -> boot()
│   ├── harness/            # Frontend composition kernel (similar to dsh's client kernel)
│   │   ├── boot.js
│   │   ├── compose.js
│   │   ├── RouteRegistry.js
│   │   ├── NavRegistry.js
│   │   ├── SlotRegistry.js
│   │   ├── ApiRegistry.js
│   │   ├── ProviderRegistry.js
│   │   └── patch.js
│   ├── core/               # Frontend base, corresponds to back/bundles/base
│   │   ├── api/            # request/auth/backend management
│   │   ├── context/        # Basic Context
│   │   ├── components/     # Layout, theme, common components
│   │   ├── pages/          # Basic pages (home/empty state/settings shell)
│   │   └── storage/        # localStorage base wrapper
│   ├── bundles/            # Feature bundles (same names as back/bundles)
│   │   ├── core/           # Core UI: Navbar/MobileNav/route container
│   │   ├── auth/           # Login, user selection, permission UI
│   │   ├── storage/        # File management, upload/download, collections
│   │   ├── sync/           # Sync status, local/remote sync UI
│   │   ├── transport/      # P2P panel, node status, WebRTC transport
│   │   ├── discovery/      # Discovery/signaling config, node scan UI
│   │   ├── web/            # Web-specific shell: settings, PWA, trust config
│   │   └── legacy/         # Legacy BT/IPFS/DHT module UI, disabled by default
│   └── index.css
└── tests/                  # Cross-bundle integration tests
```

> Note: `src/bundles/` is the actual business code; `src/core/` is the kernel and base.
> Each bundle does not directly modify other bundles; it only exposes its manifest to the harness.

---

## 4. Frontend Kernel (`src/harness`)

### 4.1 Bundle Manifest

Each frontend bundle root directory contains a `manifest.js`:

```js
// front/src/bundles/transport/manifest.js
export default {
  id: 'transport',
  version: '1.0.0',

  // Dependencies: startup order hint, not strict load order
  deps: ['core', 'storage'],

  // Route registration: keyed by id, later layers can override/disable
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
      nav: null, // Not shown in main navigation
    },
  ],

  // Layout slots: insert components into navbar/settings/dashboard etc.
  slots: {
    navbar: () => import('./components/TransportNavBadge.jsx'),
    dashboard: () => import('./components/TransportDashboardCard.jsx'),
    settings: () => import('./pages/TransportSettings.jsx'),
  },

  // API endpoint registry
  api: {
    webrtcInfo: 'GET /p2p/webrtc/info',
    nodes: 'GET /peerjs/nodes',
    pull: 'POST /p2p/pull',
    pullStatus: 'GET /p2p/pull/:id',
  },

  // React Provider (optional)
  providers: ['TransportProvider'],
}
```

### 4.2 Profile Definition

A profile is just a JS file that lists enabled bundles and their order:

```js
// front/profiles/web.js
import { defineProfile } from '../src/harness/profile'

export default defineProfile({
  id: 'web',
  name: 'Full Web Console',
  description: 'All modules + Web UI',
  bundles: ['core', 'auth', 'storage', 'sync', 'transport', 'discovery', 'web'],
  excluded: ['legacy'],

  patch: {
    // Disable specific routes
    'transport.legacyDashboard': false,

    // Customize navigation order
    nav: [
      { from: 'storage.files', order: 10 },
      { from: 'storage.collections', order: 20 },
      { from: 'transport.panel', order: 30 },
      { from: 'sync.status', order: 40 },
    ],
  },
})
```

### 4.3 Boot Flow

```
main.jsx
  → loadProfile()          // Read profile from localStorage / query / default
  → collectManifests()     // import.meta.glob for all bundle manifests
  → compose(profile, manifests)  // Filter, sort, override, disable
  → renderBootgraph        // Render <Root> with composed routes/slots/providers
  → patch.apply()          // Apply overrides/disables
  → App (React render)
```

Key implementation (pseudo-code):

```js
// front/src/harness/compose.js
export function compose(profile, manifests) {
  const enabled = new Set(profile.bundles)
  const excluded = new Set(profile.excluded || [])

  // 1. Filter manifests
  const active = manifests
    .filter(m => enabled.has(m.id) && !excluded.has(m.id))
    .sort((a, b) => depOrder(a, b, enabled))  // Topological sort by deps

  // 2. Merge routes
  const routes = new Map()
  for (const m of active) {
    for (const r of (m.routes || [])) {
      // Later bundles can override earlier ones
      routes.set(r.id, { ...r, bundle: m.id })
    }
  }
  // Apply patch overrides
  if (profile.patch) {
    for (const [id, override] of Object.entries(profile.patch)) {
      if (override === false) routes.delete(id)
      else if (routes.has(id)) routes.set(id, { ...routes.get(id), ...override })
    }
  }

  return { routes: [...routes.values()], bundles: active }
}
```

### 4.4 Registry System

The kernel provides 5 registries, all keyed by string id:

| Registry | Role | Consumer |
|---|---|---|
| `RouteRegistry` | Route tree generation | `<Routes>` |
| `NavRegistry` | Sidebar/top nav items | `<Navbar/>`, `<MobileNav/>` |
| `SlotRegistry` | Named UI slots (navbar/dashboard/settings) | Layout components |
| `ApiRegistry` | HTTP endpoint definitions | `api/request.js` |
| `ProviderRegistry` | React Context Provider chain | Root component |

### 4.5 Frontend Patch Layer

`patch.js` allows a profile to:

- Disable specific routes or slots (`false`)
- Override route paths or nav labels
- Customize bundle-specific config (merged via `deepmerge`)
- Force-disable entire bundles (higher priority than `excluded`)

Patch format:

```js
patch: {
  // Route level
  'storage.fileListing': false,         // Disable entire route
  'transport.panel': { path: '/transport' },  // Only override path

  // Nav level
  nav: [ /* as above */ ],

  // Slot level
  slots: {
    navbar: { extra: [/* additional badges */] },
  },

  // Config level
  config: {
    storage: { maxUploadSize: 500 * 1024 * 1024 },
    transport: { defaultProtocol: 'webrtc' },
  },
}
```

---

## 5. Frontend Bundle Internal Structure

Each bundle follows a unified convention:

```
front/src/bundles/transport/
├── manifest.js                  # Bundle manifest (required)
├── index.js                     # Side-effect-free export
├── pages/                       # React pages
│   ├── P2PPanel.jsx
│   └── P2PDashboard.jsx
├── components/                  # Bundle-private components
│   ├── TransportNavBadge.jsx
│   └── TransportDashboardCard.jsx
├── api.js                       # Bundle-specific API calls (registered via manifest)
├── context.jsx                  # Optional React Provider
├── hooks.js                     # Optional bundle-specific hooks
├── utils.js                     # Bundle-specific utilities
└── __tests__/                   # Bundle-level tests
    └── manifest.test.js
```

Convention checklist:

- [ ] `manifest.js` must define `id`, `routes`, `api` (minimum); other fields optional
- [ ] Page/component filenames use PascalCase
- [ ] No cross-bundle imports (`import from '../storage/...'` is forbidden)
- [ ] Shared code goes in `src/core/` or `src/shared/`, not inside bundles
- [ ] `__tests__/` at minimum includes a `manifest.test.js` (id, deps, routes validity)

---

## 6. Core Bundle (`src/core`)

`core` is the mandatory base of every profile, providing:

```
src/core/
├── api/
│   ├── request.js        # Unified fetch wrapper (auth, error handling, timeout)
│   └── backend.js        # Backend address management, health check
├── context/
│   └── AppContext.jsx    # Username, nodeInfo, theme, language
├── components/
│   ├── layout/           # AppFrame, PageHeader, EmptyState, ErrorBoundary
│   ├── ui/               # Button, Card, Modal, Toast, Table, Form
│   └── icons/            # SVG icon set
├── pages/
│   ├── HomePage.jsx      # Landing / empty state page
│   └── SettingsShell.jsx # Settings page shell (shell only, content injected by bundles)
├── storage/
│   └── localStorage.js   # Encrypted storage, key-value wrapper
└── index.js              # Exports all public APIs of core
```

`core` does not register business routes or nav items — those are injected by bundles. `core` provides "skeleton + infrastructure".

---

## 7. Profile Composition Examples

| Profile | Enabled bundles | Purpose |
|---|---|---|
| `web` | core, auth, storage, sync, transport, discovery, web | Full console |
| `lite` | core, storage | Lightweight browsing (no login, no P2P) |
| `transport-only` | core, auth, transport, discovery | Pure P2P panel |
| `dev` | all (including legacy) + HMR | Development |
| `embedded` | core, storage, sync | Embedded third-party apps |

Building a specific profile:

```bash
# Full Web (default)
pnpm build --profile web

# Lightweight version
pnpm build --profile lite
```

Vite plugin `vite-plugin-dsh-profile` reads `--profile` and filters bundles at build time, enabling tree-shaking of unselected modules.

---

## 8. Correspondence with Backend

| Frontend | Backend |
|---|---|
| `front/profiles/web.js` | `back/profiles/web.yaml` |
| `front/src/bundles/storage/` | `back/bundles/storage/` |
| `front/src/bundles/transport/` | `back/bundles/transport/` |
| `front/src/harness/` | `back/internal/harness/` (Go-side composition kernel) |
| `front/src/bundles/<name>/manifest.js` | `back/bundles/<name>/manifest.yaml` |
| `front/src/core/` | `back/bundles/base/` |

Benefits:

- Feature parity: if a backend bundle exists, the corresponding frontend bundle does too
- Independent versioning: each bundle has its own version, decoupled from main version
- Branch isolation: frontend and backend bundles with the same name can be developed together on the same branch
- Test alignment: each module's branch has both frontend and backend green

---

## 9. Migration Strategy

The current `front/` is a single monolith that cannot be converted in one shot. Migration in three phases:

### Phase 1: Layer Separation (No Behavior Change)

1. Create `src/core/` and move all shared components (layout, ui, icons) there
2. Split `api.js` into `src/core/api/request.js` + per-module api files
3. Create `src/bundles/storage/`, move file/collection/anonymous collection pages in
4. Create `src/bundles/transport/`, move P2P-related pages in
5. Create `src/bundles/sync/`, move sync status pages in
6. Keep `App.jsx` routing unchanged, but change `import` sources from `../pages/` to `../bundles/`

**Output**: All original functionality works; new `bundles/` structure is ready.

### Phase 2: Introduce Harness + Manifests

1. Implement `src/harness/boot.js`, `compose.js`, `RouteRegistry`
2. Create `manifest.js` for each bundle (first only containing routes + nav)
3. Refactor `App.jsx` to read routes from `RouteRegistry` instead of manually writing `<Routes>`
4. Implement `NavRegistry`, refactor `Navbar` / `MobileNav` to read from registry
5. Introduce `profile` concept; default profile is `web`
6. Introduce `import.meta.glob` for dynamic manifest collection

**Output**: `App.jsx` becomes pure composition output; routes/nav are all manifest-driven; profile system is available.

### Phase 3: Introduce Slots + Patch + Profiles

1. Implement `SlotRegistry`, extract navbar/dashboard/settings into slots
2. Implement `patch.js`, define patch format
3. Create `front/profiles/{web,lite,dev}.js`
4. Implement `vite-plugin-dsh-profile` for build-time filtering
5. Introduce `ApiRegistry`, `ProviderRegistry`
6. Create `front/bundles/legacy/` and move BT/IPFS/DHT there, not enabled by default
7. Add cross-bundle integration tests

**Output**: Complete composition system; can build different forms per profile; patch layer can override/disable.

---

## 10. Test Strategy

| Layer | Test |
|---|---|
| Bundle manifest | `manifest.test.js` validates id uniqueness, deps closure, routes legality, api format |
| Single bundle | `front/src/bundles/<name>/__tests__/` component + logic tests |
| Kernel | `compose.test.js` validates topological sort, override, disable semantics |
| Patch | `patch.test.js` validates override/disable/priority |
| Profile | `profiles/*.test.js` validates enabled bundles are installable |
| Integration | `front/tests/integration/` verifies full page rendering with composed profile |
| E2E | `front/tests/e2e/` (Playwright) validates each profile's critical paths |
| CI alignment | Each module branch has both frontend and backend green |

---

## 11. Key Decisions and Rationale

1. **Frontend does not replicate dsh's runtime dynamic loading**
   - Vite is build-time bundling, cannot load arbitrary packages at runtime like Node does.
   - Use `import.meta.glob` + profile filtering for "build-time composition" — predictable behavior, tree-shakeable.

2. **Routes centralized in registry, but page code stays in bundles**
   - Avoids "all routes written in one file" while preventing "routes scattered and hard to audit".
   - The registry is the single source of truth for routes; patches can override/disable.

3. **Navigation/slots not written directly in pages**
   - Pages only register their own components; final layout is determined by profile.
   - This allows mobile/desktop/embedded to share the same set of bundles.

4. **Frontend and backend bundles share the same name**
   - `front/bundles/transport` ↔ `back/bundles/transport`.
   - A module's API, UI, tests, and CI all live on the same branch, aligning with the "develop frontend and backend per module, then merge" requirement.

---

## 12. TODO / Future Refinement

- [ ] Define the complete `manifest.js` schema (routes/slots/providers/api/deps)
- [ ] Implement `harness/compose.js` id override and disable semantics
- [ ] Implement `NavRegistry` / `SlotRegistry` rendering API
- [ ] Split existing `api.js` into `core/api/request.js` + per-bundle api
- [ ] Refactor `App.jsx` to pure composition output, remove manual imports
- [ ] Generate `manifest.test.js` and CI jobs for each bundle
