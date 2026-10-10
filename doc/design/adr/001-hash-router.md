# ADR-001: Frontend Router Choice — HashRouter

> **Status:** Accepted (2026-10)  
> **Supersedes:** #168 PR description (brief "Why" note)  
> **Review trigger:** Hosting model changes (see §5)

## 1. Decision

Use **HashRouter** (`react-router-dom`) as the frontend router.  
Routing is prefixed with `#` in the URL fragment, e.g. `https://host/#/drive`.

## 2. Context

Peerdrive's frontend is served in three hosting contexts:

| Context | URL shape | Notes |
|---|---|---|
| **Cloudflare Pages** (default deploy) | `https://<sub>.pages.dev/` | SPA fallback configured; BrowserRouter works |
| **GitHub Pages / static host** | `https://<user>.github.io/peerdrive/` | No SPA fallback; BrowserRouter deep-links 404 |
| **`file://` protocol** | `file:///path/to/dist/index.html` | No server at all; BrowserRouter cannot intercept navigation |
| **Panel injection** (embedded in binary) | `http://127.0.0.1:<port>/panel` | Panel serves the frontend as an embedded asset; BrowserRouter works but the URL is internal to the panel iframe |

The #168 PR (merged) switched from `BrowserRouter` to `HashRouter` with a brief note that `BrowserRouter` "fails on Pages / file:// / any static host without fallback". This ADR formalises the full tradeoff analysis.

### 2.1 The `file://` argument

`BrowserRouter` relies on the server returning `index.html` for every unknown path (SPA fallback). This fails on:

- **GitHub Pages**: no fallback configured by default; `/drive` returns 404
- **`file://`**: there is no server; the browser cannot intercept path changes at all
- **Any static file server without SPA rewrite rules**: `/drive`, `/collections`, `/collection/:hash` all 404

`HashRouter` sidesteps this entirely: the path lives in the URL fragment (`#/drive`), which is never sent to the server. Every navigation stays within the same `index.html`, which is loaded once. This makes the frontend work on **any** hosting target — Pages, GitHub Pages, a bare `python -m http.server`, or even `file://`.

### 2.2 The panel injection question

The panel (`/panel` embedded in the binary) serves the frontend as an injected asset. Two hosting modes coexist:

1. **Panel mode** (node running, user opens `http://127.0.0.1:<port>/panel`): the panel serves `index.html` at the panel's own path. BrowserRouter **would** work here because the panel's HTTP server can return `index.html` for any sub-path.
2. **Standalone mode** (user downloads `dist/` and opens directly): the frontend must work without any HTTP server. Only HashRouter works.

**Key question**: does the panel injection make the `file://` benefit moot?  
**Answer**: No — the frontend is distributed as a standalone `dist/` artifact in addition to the panel. The `file://` / static-host scenario is a legitimate use case (see `front/CF-PAGES-NOTE.md`, `doc/guide/single-binary-guide.md`). Removing HashRouter to favour BrowserRouter would break the standalone deployment path.

### 2.3 Cost of HashRouter

| Cost | Impact | Mitigation |
|---|---|---|
| **Ugly URLs** (`#/drive` instead of `/drive`) | Minor cosmetic; sharing a `#/drive` link still works (same-origin) | Acceptable for a private-network tool; not SEO-sensitive |
| **Fragment not server-visible** | Server cannot route on URL path | Frontend never relied on server-side routing; all data comes through WS (`/ws/peer`), not HTTP path |
| **SEO / share preview** | Fragment is not sent to social/preview crawlers | Not applicable — peerdrive is a private P2P network, not a public SEO target |
| **History API back/forward** | `hashchange` events instead of `popstate` | `react-router-dom` handles both transparently; no user-visible difference |

## 3. Tradeoff Matrix

| Criterion | HashRouter | BrowserRouter | Winner |
|---|---|---|---|
| Works on `file://` | ✅ | ❌ | **HashRouter** |
| Works on GitHub Pages | ✅ | ❌ (without 404 fallback) | **HashRouter** |
| Works on Cloudflare Pages | ✅ | ✅ (fallback configured) | Tie |
| Works in panel injection | ✅ | ✅ | Tie |
| Clean URLs | ❌ (`#/drive`) | ✅ (`/drive`) | BrowserRouter |
| Server-side routing possible | ❌ | ✅ | BrowserRouter |
| SEO / share preview | ❌ | ✅ | BrowserRouter |
| Implementation complexity | Low | Requires server config | **HashRouter** |
| Deployment portability | Any target | Only SPA-fallback targets | **HashRouter** |

**Conclusion**: HashRouter wins on 5 of 9 criteria, including the two hard requirements (`file://` support, zero-config static hosting). The costs are cosmetic and inapplicable to peerdrive's private-network use case.

## 4. Decision

**Accepted.** HashRouter is the correct choice for peerdrive's deployment model. The #168 PR is correct as implemented.

## 5. Review Trigger Conditions

Re-evaluate this ADR if **any** of the following become true:

1. **Panel becomes the only hosting mode** — if the standalone `dist/` artifact is dropped and the frontend is only ever served through the panel's HTTP server, the `file://` argument no longer applies and BrowserRouter becomes viable.
2. **Public SEO / discoverability is a requirement** — if peerdrive introduces a public collection directory or marketplace that should be indexed by search engines, the fragment-URL problem becomes material.
3. **Server-side rendering (SSR) is adopted** — SSR requires the server to know the route, which HashRouter fragments prevent.
4. **A new hosting target appears** that requires clean URLs (e.g. an App Store web wrapper, a native app's WebView with URL-based deep linking).

Until one of these triggers fires, HashRouter remains the default. If triggered, the migration path is straightforward: change `HashRouter` → `BrowserRouter` in `front/src/App.jsx` (single-line change), add SPA fallback config to the hosting target, and remove the `#` prefix from all existing saved links/bookmarks (breaking change for users with saved fragment URLs).

## 6. Related Issues

- **#168**: Original PR that switched to HashRouter
- **#286**: This ADR (issue #286)
- **#287**: Product narrative convergence (narrative context for frontend routing decisions)
- **`front/CF-PAGES-NOTE.md`**: Cloudflare Pages deployment notes (BrowserRouter would work here, but not on all targets)
