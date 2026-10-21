# Architecture Decision Record: Routing Strategy (HashRouter vs BrowserRouter)

- **Status**: Accepted
- **Deciders**: Architecture Team
- **Date**: 2026-10-10
- **Context Issue**: #168, #286

---

## 1. Context & Problem Statement

In single-page applications (SPAs), choosing between `BrowserRouter` (HTML5 History API) and `HashRouter` (`#` fragment-based routing) impacts hosting flexibility, file protocol compatibility, server-side configuration, and URL aesthetics.

Peerdrive's operator frontend (`front/`) and public consumer artifacts are distributed and hosted across multiple distinct deployment topologies:
1. **GitHub Pages / Cloudflare Pages / Static Object Storage**: Static file hosting without customized server fallback rules (where accessing `/drive` directly results in HTTP 404 unless routed via SPA 404 redirects).
2. **Local Static Files (`file://` Protocol)**: Direct opening of client artifacts (such as `panel.html` or locally built bundles) directly from the local filesystem without running an HTTP web server.
3. **Embedded Binary Console (`go:embed` at `/panel` and `/`)**: The Go backend embedding the static distribution and serving it through Gin.

## 2. Decision

We adopt **`HashRouter`** as the default routing mechanism across the frontend application.

## 3. Analysis of Trade-offs

### 3.1 Advantages of `HashRouter`
- **Zero Server-Side Rewrite Dependency**: The URL fragment (`/#/drive`, `/#/collections`) is never sent to the HTTP server in HTTP request headers. Any static file server, CDN, GitHub Pages, or S3 bucket serves `index.html` regardless of the route depth without requiring `try_files $uri /index.html` Nginx rules.
- **Local Filesystem Compatibility (`file://`)**: Under `file://`, the History API `pushState` often triggers browser security errors (CORS / origin mismatch), and refreshing a route like `file:///path/to/drive` tries to open a non-existent directory on disk. `HashRouter` works seamlessly under `file://`.
- **Panel & Cross-Origin Embedding**: When the web interface or panel is embedded inside an iframe, webview, or injected host, hash navigation avoids parent navigation hijacking and origin conflicts.

### 3.2 Drawbacks & Costs of `HashRouter`
- **URL Aesthetics**: URLs contain the `#` hash symbol (e.g. `https://domain/#/drive`).
- **Server Invisibility**: Because hash fragments are not sent in HTTP requests, server logs, reverse proxy routing rules, and server-side rendering (SSR) cannot inspect or branch on the client route path directly.
- **OpenGraph & SEO Sharing**: Social preview crawlers generally ignore URL fragments, limiting rich unfurl metadata for sub-routes.

### 3.3 The "Panel Coverage" Trade-off Evaluation
- *Question*: If the standalone single-file panel (`dist/panel.html`) already covers the pure `file://` offline consumption scenario, does the main web frontend (`front/`) still need `HashRouter`?
- *Finding*: While `panel.html` serves ad-hoc WebRTC consumers without a server, the operator console (`front/`) is frequently deployed to serverless static hosting (Cloudflare Pages, GitHub Pages, IPFS gateways). Maintaining `HashRouter` eliminates custom server redirect scripts (`404.html` hacks) and guarantees identical navigation behavior across all distribution vectors.

## 4. Reconsideration & Reversal Criteria

A transition back to `BrowserRouter` may be reconsidered if:
1. The frontend deployment topology is strictly consolidated into the embedded backend binary and custom domain hosting with dedicated reverse proxy fallback configurations.
2. Server-side deep linking, OAuth redirect callback formatting, or SEO requirements make hash fragments unacceptable.
