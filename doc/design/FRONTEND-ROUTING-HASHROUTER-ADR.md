# 前端路由形态决策记录：HashRouter（file:// 收益 vs panel 覆盖的权衡）

> 对应 Issue: #286  
> 状态: **已接受（Accepted）** — 保留 HashRouter；复核触发条件见 §6，回切条件见 §7  
> 关联 Issue: #168（BrowserRouter → HashRouter 切换）, #286（本 ADR）  
> 关联提交: `58f9ae6` `feat(front): switch BrowserRouter to HashRouter`  
> 生效范围: `front/`（完整 Web UI，节点主人 / WebRTC 访客双平面）  
> 生成日期: 2026-10-10（基于 refactor 分支 `0198a4d` 的实读代码状态；与代码冲突时以代码为准）

---

## 1. 决策（Decision）

`front/` 使用 **`HashRouter`**（`react-router-dom` v7），路由形态为
`origin + BASE_URL + #/path`，例如 `https://hana-ame.github.io/peerdrive/#/drive/<64hex>`。

不做服务端路由：`#/…` 片段不进入 HTTP 请求行，因此前端路由**不需要**任何托管侧的
SPA fallback / rewrite 配置。

---

## 2. 背景（Context）

### 2.1 当前实现（代码事实）

| 事实 | 证据 |
|---|---|
| 入口用 `HashRouter` 包裹全部 16 条路由 | `front/src/App.jsx:5`（`import { HashRouter, … }`）、`front/src/App.jsx:148-192`（`<HashRouter>…</HashRouter>`）、`front/src/App.jsx:168-185`（`<Routes>` 表） |
| 路由参数靠 `useParams` 读取，不读 `location.hash` | `front/src/features/drive/pages/Drive.jsx:55`、`front/src/features/collection/pages/CollectionView.jsx:51`、`front/src/features/iwara/pages/Iwara.jsx:149`、`front/src/features/display/pages/Display.jsx:14` |
| 深链生成已是 hash 形态 | `front/src/features/drive/pages/Drive.jsx:278-286`：`${origin}${base}#/drive/${f.hash}`，注释写明「Hash router: append #/drive/:hash (no server SPA fallback needed)」 |
| 依赖版本 | `front/package.json:15`（`react@^19.2.5`）、`front/package.json:17`（`react-router-dom@^7.14.2`） |
| 12 条路由均为 `React.lazy()` 动态加载 | `front/src/App.jsx:9-19` |

切换只动了 2 个文件（5 行插入 / 4 行删除，`58f9ae6`）：`App.jsx` 的
`BrowserRouter basename=…` → `HashRouter`，以及 `Drive.jsx` 的深链拼接。
`Link` / `navigate` 调用一处未改，react-router 把路由形态屏蔽掉了。

### 2.2 切换时的原始动机（需要更正的记录）

`58f9ae6` 的提交说明给出的理由是：

> BrowserRouter requires server-side SPA fallback for deep links … HashRouter
> (#/drive/abc123) avoids this entirely … so the same URL works on Pages, static
> hosting, and **file:// protocol (the panel.html single-file form)**.

这条理由由**两个前提**支撑。§3 与 §4 逐个核查了它们是否成立 —— 结论是：
第一个前提（SPA fallback 缺口）**已被现有部署配置覆盖**；第二个前提
（file:// 收益）在字面上**引用错了产物**。这两点都需要在本 ADR 里写清楚，
否则后人读提交记录会以为 HashRouter 换来了一份真实存在的 file:// 能力。

---

## 3. 两个前端 UI 面与托管形态（关键事实核查）

这一节回答 #286 的核心问题：**panel 到底是「注入式托管 front 的宿主」，还是
「另一个独立产物」？file:// 收益被不被 panel 覆盖？**

结论先行：**panel 不是注入式宿主，两个 UI 是彼此独立的产物；因此「panel 覆盖
front 的 file:// 场景」这个假设在仓库现状下不成立。但反向的结论同样成立 ——
`front/` 自己也用不上 file://。**

### 3.1 面 A：`front/` —— 完整 Web UI（节点主人 + WebRTC 访客）

- 部署形态 1：**GitHub Pages**。`.github/workflows/pages.yml:43` 构建
  `npx vite build --base=/peerdrive/`；`.github/workflows/pages.yml:50`
  `cp front/dist/index.html _site/404.html` —— **workflow 显式产出 SPA 404
  fallback**，`/drive`、`/market` 等前端路由刷新由 GH Pages 的 404 兜底。
- 部署形态 2：**Cloudflare Pages**。`front/CF-PAGES-NOTE.md:3-6` 记录生产配置为
  Root `front` / Build `npm ci && npm run build` / Output `dist`。该文件全文 8 行，
  只有第 5 行是构建配置，**未记录任何 rewrite / fallback 规则**。
- 产物形态：`front/index.html:38` 是 `<script type="module" src="/src/main.jsx">`，
  叠加 `front/src/App.jsx:9-19` 的 12 个 `lazy()` 路由 chunk —— 这是
  **多 chunk 的 ES module 构建**，不是一个自包含单文件。
- 与 client 包的关系：`front/vite.config.ts:12-15` 把 `peerdrive-client` alias 到
  `../packages/peerdrive-client/src/index.js`。方向是 **`front/` import client
  库的源码**，不是加载 panel 产物。
- **Go 后端从不托管 `front/`**：`back/internal/router/router.go:499-522` 只把
  panel 挂在 `/panel`；`back/internal/router/router.go:501` 的注释明确 `/` 是
  API 的 404/redirect 兜底、不能接管。`front/` 只有 Pages 系托管这一种归宿。

### 3.2 面 B：`packages/peerdrive-client/dist/panel.html` —— 公共消费面板

**实读结论：这是从 `packages/peerdrive-client/` 自身源码生成的独立单文件，
全程不加载、不引用 `front/`**（它确实有一个自己的注入点，但注入的不是
`front/`，见 §3.2.1）。

| 证据 | 说明 |
|---|---|
| `packages/peerdrive-client/scripts/build-panel.mjs:88-89` | `const ORDER = ['sha256.js', 'protocol.js', 'client.js']` —— 内联进产物的只有 `packages/peerdrive-client/src/` 的三个文件 |
| `packages/peerdrive-client/scripts/build-panel.mjs:118-131` | 产物 = `panel/template.html` 模板 + 内联 bundle + `panel/app.js`，三处替换，没有任何 `front/` 输入 |
| `packages/peerdrive-client/scripts/build-panel.mjs:1-9` | 文件头注释写明它存在的目的：`demo/consumer.html` 用 `<script type="module">` + `import '../src/index.js'`，「`file://` 会因为 CORS 直接失败」，所以改成把三个模块拼成 IIFE 塞进单个 HTML |
| `packages/peerdrive-client/panel/app.js:1` | 「普通脚本，不是 module：产物要在 `file://` 下能跑」—— 面板是普通 `<script>`，没有挂载/注入其他应用 bundle 的机制 |
| `packages/peerdrive-client/panel/template.html:8-13` | 「形态：单个 HTML 文件，双击（`file://`）或丢到任意静态托管都能用，不需要本地起 http 服务、不需要自己的后端」；唯一外部依赖是 CDN 上的 peerjs（同目录 vendored 副本 → jsdelivr → unpkg 回退） |
| `packages/peerdrive-client/demo/format.js:1-5` | 「**Intentionally does not reuse** *front/src/components/netdisk/format.js*: this package needs to be published/opened standalone」—— 对 `front/` 代码是**刻意不复用**，与「panel 托管 front」相反。（注：该注释里引用的 `front/` 路径早已不存在，属陈旧注释；方向性结论不受影响。） |
| 全仓 grep `iframe` / `customElements` / `attachShadow` | `front/` 唯一的 `<iframe>` 是 `front/src/components/netdisk/FilePreviewModal.jsx:278`（PDF 预览），与托管无关；无 Web Component / shadow DOM 注入点 |
| `front/` 内 grep `panel.html` | **零命中** —— `front/` 从未引用 panel 产物 |
| 构建/校验闭环独立 | panel 走 `npm run build:panel` + CI `check:panel` 漂移闸门（`packages/peerdrive-client/package.json`），与 `front/` 的 `npm run build` 是两条互不依赖的流水线 |

### 3.2.1 panel 确实有「注入式托管」，但注入的不是 `front/`

#286 说的「注入式宿主」在仓库里**不是空指** —— 只是注入的目标不是 `front/`。

| 事实 | 证据 |
|---|---|
| panel 有第三种托管形态：内嵌进 Go 二进制 | `back/internal/panel/panel.go:22` `//go:embed panel.html`；`back/internal/router/router.go:499-522` 把 `panel.Handler("/panel")` 挂在 `/panel` 与 `/panel/` |
| 内嵌文件与 client 包的 dist 产物**逐字节相同** | 两份文件 sha256 均为 `ba069397ed1696c7…f32327e6`；`.github/workflows/ci.yml:158-171` 的 `client-package` job 用 `cmp` 强制守卫这条一致性 |
| 内嵌产物里没有任何 `front/` 代码 | `back/internal/panel/panel.html` 中 `react-dom` / `createRoot` **零命中** |
| **注入点确实存在，注入的是 bootstrap 脚本** | `back/internal/panel/panel.go:91-120` 的 `withBootstrap()`：服务端在面板 HTML 第一个 `<script>` 之前插一段脚本，同源反查本节点 ID 与信令参数并自动填进 URL；signal key 由 `panel.SetSignalKey(cfg.PeerJSKey)` 在服务端注入（`back/internal/router/router.go:516-517`） |

**这才是 #286 那个权衡的准确形态**：注入式宿主确实存在（`/panel` + `withBootstrap`），
但它注入的是一个**独立、自包含、只含消费端逻辑**的 `panel.html`，不是 `front/`。
所以：

- 「panel 覆盖了 `front/` 的 file:// 场景」→ **不成立**（两者没有加载关系，§3.4）；
- 「panel 的托管让 `front/` 的 file:// 需求消失」→ **也不成立**（`front/` 从来
  就不需要 file://，§4.1）；
- 真正被 `/panel` 解决的是**另一个问题**：`panel.html` 要用户自己手填
  node/host/port/key 四项（`back/internal/panel/panel.go:80-90`），内嵌托管把它
  变成「浏览器打开 `http://127.0.0.1:3000/panel` 就能用」。**这与 `front/` 用
  哪种 router 无关。**

### 3.3 功能面差异：panel 是独立消费端，不是 `front/` 的子集宿主

功能面差异也印证了「两个独立 UI」：panel 只有净盘消费能力（多节点并存、共享清单、
按 hash 拉取、任务列表、预览），没有 `front/` 的节点管理面（`/node`、`/settings`、
`/collections` 管理、`/transfers`、`/bt`、`/ipfs`）。panel **不是** `front/` 的
子集宿主，而是一个独立的、更小得多的消费端。

### 3.4 因此 #286 的假设需要修正

#286 的顾虑是：「若面板以注入方式托管前端，`file://` 直开场景不成立，则
HashRouter 的代价未必划算」。

- 事实是 panel **根本没有托管 `front/`**，所以「panel 覆盖 `front/` 的
  file:// 收益」这条推理链的前提不存在 —— panel 覆盖不了，也覆盖不了任何
  `front/` 场景，因为两者没有加载关系。
- 但这**不等于** `file://` 收益对 `front/` 成立，见 §4.1：真正能 file:// 直开
  的只有 `panel.html`，而它不含 `front/` 的代码。

---

## 4. 权衡（Tradeoffs）

### 4.1 file:// 收益：对 `front/` 实际不成立（提交记录里引用错了产物）

`58f9ae6` 把「file:// 协议」列为 HashRouter 的收益，并注明「the panel.html
single-file form」。核查后：

- `panel.html` 不含 `front/` 的任何代码（§3.2），所以它的 file:// 能力
  **无法转移**给 `front/`。panel 用 IIFE + 普通 `<script>` 实现 file:// 兼容，
  与 `front/` 用哪种 router 完全无关。
- `front/` 的构建产物是多 chunk 的 ES module 构建（`front/index.html:38` +
  `front/src/App.jsx:9-19`；`vite build` 后生成，见 `front/CF-PAGES-NOTE.md` 的 Output `dist` 配置）。ES module 脚本在 `file://` 下 origin 为 `null`，
  Chrome 系浏览器以 CORS 拒绝加载 —— 这正是
  `packages/peerdrive-client/scripts/build-panel.mjs:1-9` 记录
  `demo/consumer.html` 「`file://` 会因为 CORS 直接失败」的同一条约束，
  而 `front/index.html` 与 `demo/consumer.html` 是**同一种脚本形态**
  （`<script type="module">` + 跨文件 import）。
- 仓库内也没有任何脚本/测试/文档用 file:// 打开 `front/` 的构建产物：
  `doc/testing/README.md:158` 的 `verify-panel.mjs` 明确打开的是 **panel**
  产物（`file://` 断言 8 项）。

**结论：`file://` 是 `panel.html` 的能力，不是 `front/` 的能力。HashRouter
并没有为 `front/` 换来 file:// 直开。** 这条不是「收益被 panel 覆盖」，而是
「收益从一开始就不在 `front/` 这一侧」。

### 4.2 SPA fallback 收益：已被当前两个托管形态覆盖

HashRouter 换掉 BrowserRouter 的唯一实质动机是「深链刷新不 404」。而当前
部署链路已经各自解决了这个问题：

| 托管 | fallback 怎么来的 | 是否依赖 HashRouter |
|---|---|---|
| GitHub Pages（`.github/workflows/pages.yml`） | `cp front/dist/index.html _site/404.html`（第 50 行）显式兜底 | **否** —— BrowserRouter 在这里也能工作 |
| Cloudflare Pages（`front/CF-PAGES-NOTE.md`） | 说明文件未记录 rewrite 规则；仅记录 root/build/output | 无法从仓库判定，属**未落档**状态 |

也就是说：对已经落档的 GH Pages 形态，HashRouter 省下的那份「配 SPA fallback」
成本**已经由 workflow 一次配掉并固化在 CI 里了**。HashRouter 今天还在发挥作用的
只是**防御性价值**：换到任何不支持 rewrite 的静态空间（OSS/S3/CDN 直传 dist/）
时不用改代码。

### 4.3 fragment 代价：真实、持久，但对本产品可接受

| 代价 | 影响 | 本产品是否承受 |
|---|---|---|
| URL 带 `#`，形态丑 | `https://host/peerdrive/#/drive/<64hex>` | 承受。受众是节点主人和 WebRTC 访客，不是公开内容站 |
| 片段不进请求行 → **无服务端路由** | 无法用 nginx/CDN 做按路径的缓存、灰度、按路径鉴权；也无法 SSR/SSG | 承受。`front/` 是纯客户端 SPA，数据全走本地 WS admin 面与 WebRTC |
| SEO / 分享预览 / OG 卡片 | 抓取器一般不跟 hash，`<title>` 也不会按路由变 | 承受（事实上不适用）—— 产品没有 SEO 目标，分享靠的是 §2.1 那条可复现的深链整串，而非抓取预览 |
| 服务端重定向 / 按路径跳转无法配置 | 如 `/` → `/drive` 的 301 做不了 | 承受 |
| 深链失效风险 | 若日后回切 BrowserRouter，所有已分享出去的 `#/drive/<hash>` 链接全部失效 | 不承受 —— 这是 §7 回切前必须评估的用户可见代价 |

**净结论**：HashRouter 换来的实质收益（file://）**不成立**，剩下的收益
（免 fallback 配置）在当前托管形态下**已被覆盖**，属于防御性冗余；而代价是
永久性的。但它仍然值得保留，因为代价在本产品的受众与分发方式下**不痛**
（无 SEO 目标、无服务端路由需求、URL 只用于人肉复制粘贴），而**回切的迁移
成本（已分享深链全废）是真实且不可逆的**。

---

## 5. 后果（Consequences）

1. `front/` 的路由 URL 形态固定为 `…/#/<path>`。任何人写深链、写分享文案、写
   测试断言 URL 时都要带上 `#`。目前唯一生成深链的地方是
   `front/src/features/drive/pages/Drive.jsx:278-286`。
2. 托管侧**不需要**为前端路由配置 fallback；但**也不能**依赖托管侧做任何
   按路径的路由/鉴权/缓存，因为请求行里永远只有 `BASE_URL` 那一段。
3. **不要**把「file:// 直开」当 `front/` 的能力去承诺。仓库里 file:// 直开
   的唯一合法目标是 `packages/peerdrive-client/dist/panel.html`，且它有自己
   的独立构建（`npm run build:panel`）与 CI 闸门（`check:panel`）。
   要改消费端 netdisk UI → 改 `packages/peerdrive-client/`；要改节点管理面
   → 改 `front/`。两者不是同一个东西，改错一侧会让另一个 UI 静默不更新。
4. 若有人发现「`front/` 构建产物双击打不开」——那不是 bug，也不是回归，是
   §4.1 里那条 ES module + `file://` 的 CORS 约束。正确的 file:// 交付物是 panel。
5. `doc/ARCHITECTURE.md` §7「路由」原表述为「无 SPA fallback 依赖，支持
   `file://` 协议和任何静态空间部署」——本次已改为区分两种形态并指向本 ADR。

---

## 6. 复核触发条件（Review triggers）

出现以下任一情况，本 ADR 必须重审（`front/` 的 hosting 形态或产物形态一旦变化，
§4 的权衡表就要重算）：

| 触发条件 | 需要重估的问题 |
|---|---|
| **新增或更换托管形态**（GH Pages → CF Pages 独占；迁到 OSS/S3；自建 nginx；离线包） | 新形态是否有 SPA fallback 且已落档？`front/CF-PAGES-NOTE.md` 目前未记录 rewrite 规则，这条是欠账 |
| **`front/` 引入 SSR / SSG**（vite-plugin-ssr、Remix、Astro 等） | fragment URL 与服务端路由**直接冲突**，必须回切（§7） |
| **`panel.html` 与 `front/` 合并为单一 UI 形态**（把 `front/` 内联进 panel，或反之让 panel 托管 `front/`） | §3.2 的「panel 不托管 front」结论失效，§4.1 的 file:// 收益**重新成立**，本 ADR 的净结论翻转 |
| **出现 SEO / 分享预览 / 公开内容分发**需求 | fragment 代价从「不痛」变成「痛」，§4.3 需重算 |
| **服务 worker 拦截范围变化** | `front/index.html:22-34` 已记录一次「SW 注册两次 + 绝对路径 404」的教训；`registerSW()` 的 scope 依赖 `BASE_URL`，路由形态再变要一起复核 |
| **深链消费方增加**（第三方系统要解析 peerdrive 的深链） | hash 形态对机器解析不友好，需要评估 |

---

## 7. 回切 BrowserRouter 的条件

本 ADR 的净结论是「收益边际但代价可接受」，因此**默认保留 HashRouter**。要回切
`BrowserRouter`，以下条件应**同时**满足：

1. **所有生产托管形态的 SPA fallback 都已落档**，而不只是「恰好配过」：
   - GH Pages：`.github/workflows/pages.yml` 继续产出 `_site/404.html`（若部署
     形态改为只上传 `front/` 构建产物而没有 404 兜底，先补上再回切）；
   - CF Pages：把 SPA rewrite 规则**写进** `front/CF-PAGES-NOTE.md`
     （当前该文件全文 8 行，仅第 5 行构建配置，无 rewrite 记录）；
   - 其他 host（OSS/S3/自建 nginx）：等价于
     `try_files $uri $uri/ /index.html` 的配置已存在并落档。
2. **接受放弃 file:// 直开 `front/` 构建产物** —— 依 §4.1 这条本来就不成立，
   因此**不构成阻碍**（这是本 ADR 最重要的结论：回切不需要为 file:// 付代价，
   因为那份收益从来不在这一侧）。
3. **深链格式变更已公告**：`front/src/features/drive/pages/Drive.jsx:278-286`
   的 `onCopyDeepLink` 需从 `${origin}${base}#/drive/${f.hash}` 改回
   `${origin}${base}/drive/${f.hash}`。**已分享出去的 `#/drive/<hash>` 链接会
   全部失效** —— 这是回切唯一不可逆的用户可见代价，必须写进 changelog 并给出
   迁移说明（`#/drive/x` → `/drive/x`，人工替换 `#` 即可）。
4. **服务 worker 复核**：确认 `registerSW()`（`front/src/lib/swBridge.js` 与
   `front/src/platform/shared/swBridge.js`）的 scope 在 BrowserRouter 形态下不变。
5. **本 ADR 处置**：状态改为 `Superseded by <新 ADR>`，新 ADR 说明回切时哪些
   §6 触发条件已被满足。

### 7.1 一句话版本

> **回切的条件不是「file:// 收益被 panel 覆盖」——那条不成立（panel 不托管
> `front/`，file:// 也不是 `front/` 的能力）。回切的真实条件是：所有托管形态的
> SPA fallback 都已落档，且愿意让已分享的 hash 深链失效。**

---

## 8. 相关文档

- [doc/design/modules/13-frontend.md](modules/13-frontend.md) — `front/` 模块设计（双平面、页面清单、admin 面与消费面）
- [doc/ARCHITECTURE.md](../ARCHITECTURE.md) §7 — 前端技术栈与路由现状（本 ADR 的对外摘要）
- `packages/peerdrive-client/README.md`「Public panel」一节 — `dist/panel.html` 的形态、URL 参数、CORS 与 mixed-content 坑
- `packages/peerdrive-client/scripts/build-panel.mjs` — panel 单文件构建实现（file:// 兼容的实现依据）
- `back/internal/panel/panel.go` — 面板内嵌进二进制并托管在 `/panel`，含 `withBootstrap()` 注入逻辑（§3.2.1）
- [doc/design/README.md](README.md) — 本目录索引
- [doc/testing/README.md](../testing/README.md) — `verify-panel.mjs` / `check:panel` 的手测与 CI 闸门

**代码证据索引**：`front/src/App.jsx:5,148-192` · `front/src/App.jsx:168-185` ·
`front/src/App.jsx:9-19` · `front/src/features/drive/pages/Drive.jsx:278-286` ·
`front/index.html:38` · `front/vite.config.ts:12-15` · `front/CF-PAGES-NOTE.md` ·
`.github/workflows/pages.yml:43,50` · `packages/peerdrive-client/scripts/build-panel.mjs:1-9,88-89,118-131` ·
`packages/peerdrive-client/panel/template.html:8-13` · `packages/peerdrive-client/panel/app.js:1` ·
`packages/peerdrive-client/demo/format.js:1-5` · `back/internal/panel/panel.go:22,80-91` · `back/internal/router/router.go:499-522,516-517` · `.github/workflows/ci.yml:158-171`
