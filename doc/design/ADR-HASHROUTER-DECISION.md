# ADR: Frontend Routing — HashRouter vs BrowserRouter

> **Decision ID**: ADR-001  
> **Status**: Accepted (2026-09-20, PR #168)  
> **Issue**: #286 (补决策记录)  
> **Related**: #168 (implementation), #73 (React.lazy route splitting)

---

## Context

peerdrive's frontend is a single-page application (SPA) built with React + react-router-dom v7 + Vite. The routing library offers two server-agnostic options:

- **BrowserRouter**: uses the History API with absolute URL paths (`/drive`, `/collection/:hash`). Deep-link refresh requires the server to serve `index.html` for unknown paths (SPA fallback).
- **HashRouter**: uses the URL hash fragment (`/#/drive`, `/#/collection/:hash`). The hash fragment is never sent to the server, so the same URL works on any static host.

### Hosting scenarios

| Hosting mode | BrowserRouter | HashRouter |
|---|---|---|
| GitHub Pages | Needs `.nojekyll` + 404.html SPA fallback | ✅ Works out of the box |
| file:// protocol | ❌ Broken (no server to serve index.html) | ✅ Works |
| Static CDN (S3, Vercel, Netlify) | Needs rewrite rules | ✅ Works |
| panel.html (injected single-file) | n/a — panel uses no router | n/a |
| Self-hosted nginx/caddy | Needs `try_files $uri /index.html` | ✅ Works |

### The file:// vs panel.html tradeoff

**file:// benefit of HashRouter**: When `dist/index.html` is opened directly via `file://`, BrowserRouter routes would fail because the browser requests `/drive` as a file path — which doesn't exist. HashRouter sends `#/drive` which the browser treats as the same local file.

**panel.html bypasses this**: The public-facing panel (`packages/peerdrive-client/dist/panel.html`) is a single-file bundle injected into arbitrary hosts. It does **not** use react-router at all — it has its own internal state management. Therefore, the file:// benefit of HashRouter is **only relevant for the full SPA (`dist/index.html`)**, not the panel.

**Current hosting reality** (as of 2026-10-10):
- Full SPA is deployed to GitHub Pages (`https://hana-ame.github.io/peerdrive/`) — needs SPA fallback config (`.nojekyll` + `404.html` copy of `index.html`)
- Panel is distributed as a single `panel.html` file — no router dependency
- Developers may open `dist/index.html` locally via `file://` during development

### Costs of HashRouter

1. **URL aesthetics**: `/#/drive` instead of `/drive` — harder to share, no server-side routing, no SEO value
2. **Fragment is invisible to server**: No server-side rendering, no SSR-friendly crawlers
3. **Deep linking**: Shareable but not "clean" — some users find `#` in URLs suspicious
4. **Service Worker navigation**: `navigate` events from the service worker need hash-aware handling

## Decision

**Use HashRouter** for the full SPA (`front/src/App.jsx`).

Rationale:
1. **File:// compatibility**: The `dist/index.html` should work when opened directly — this is a real dev workflow
2. **Zero-config static hosting**: Any CDN or static host works without rewrite rules
3. **GitHub Pages simplicity**: No `404.html` SPA fallback needed (though the repo currently has one for robustness)
4. **Low cost**: The URL aesthetics penalty is minor for a P2P tool used by enthusiasts, not by end users browsing content

## Consequences

### Positive
- `dist/index.html` works via `file://` without a local HTTP server
- Any static host (GitHub Pages, S3, Vercel) needs no special configuration
- No server-side fallback config in deployment

### Negative
- URLs contain `#` fragment (cosmetic penalty)
- Server-side routing not possible (no SSR)
- Some users may not realize hash-based navigation is "normal"

### Monitoring / re-evaluation trigger

**Re-evaluate this decision if**:
1. peerdrive transitions to a fully server-rendered architecture (e.g., Next.js)
2. The SPA is fully replaced by the panel (no `dist/index.html` deployment)
3. SEO becomes a priority (file index pages, public collections)
4. All deployment targets support SPA fallback natively (e.g., only nginx with `try_files`)

At that point, switching to BrowserRouter would be a one-line change in `App.jsx`.

## Implementation

```jsx
// front/src/App.jsx
import { HashRouter } from 'react-router-dom';

<HashRouter>
  <Routes>...</Routes>
</HashRouter>
```

No `basename` prop — Vite's `BASE_URL` handles the base path separately.

## References

- PR #168: "Switch to HashRouter for file:// and Pages compatibility"
- Issue #73: Route-level code splitting via React.lazy (complementary, not conflicting)
- Issue #284: vendor code splitting via manualChunks (further optimization)

---

*Generated 2026-10-10 as part of Issue #286 (decision record backfill).*