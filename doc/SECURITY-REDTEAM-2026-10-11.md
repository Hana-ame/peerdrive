# UI 对抗攻击（红队）审计 — 2026-10-11

基准 `refactor` @ `607e833`。修复 PR #351（`fix/security-redteam-r3`）。

扫描面：XSS（反射/存储/DOM）、路径穿越、上传侧注入、CSRF/点击劫持、
信息泄露、分享链接安全。**静态审计 + 本地起服务 + Chromium 实测**。

---

## 1. HIGH — 存储型 XSS 链（已修，Chromium 实测验证）

### 根因

`/sha256sum/:hash` 把攻击者可控的上传文件名直接交给 `http.ServeContent`，
服务端**按扩展名猜**响应 Content-Type：

| 上传文件名 | 修复前 Content-Type |
|---|---|
| `evil.html` | `text/html; charset=utf-8` |
| `evil.js` | `text/javascript; charset=utf-8` |
| `evil.svg` | `image/svg+xml` |

配 `?inline=1`（`Content-Disposition: inline`）后，上传的文件就在**本域**当文档渲染。

全局 CSP 是 `default-src 'self'` —— 它**允许同源 `<script src>`**。于是攻击者传
`evil.js` + 一个引用它的页面，载荷就在受害者浏览器里执行。

实测证据（Chromium）：`document.title` 被改写成存储载荷写入的值
`"XSS_EXECUTED_FROM_STORED_JS"`。

### 影响

同源 JS 可读取管理前端的 `localStorage` —— `peerdrive_auth_token` /
`peerdrive_auth_key`（见 `front/src/platform/shared/storageKeys.js`）。
面板与本服务**同域部署**（自托管常见形态）时即节点全权接管；分域部署时仍可
同源 fetch 全部匿名接口 + 在受信任域名上渲染钓鱼页。

### payload

```
POST /files/upload   evil.js:   document.title='XSS_EXECUTED_FROM_STORED_JS'
POST /files/upload   page.html: <script src="/sha256sum/<evil.js-hash>?inline=1"></script>
GET  /sha256sum/<page-hash>?inline=1        → 受害者 title 被改写
```

### 修复（`back/internal/controller/download.go`）

新增 `setDownloadHeaders()`，文件名不再决定 Content-Type：

1. inline 只认**预览白名单**（`image/*` + `text/plain`/`.log`/`.csv`），其余强制
   `application/octet-stream`。白名单刻意排除 `text/html`、`text/xml`、
   `application/xhtml+xml`、`image/svg+xml`、`application|text/javascript`
   —— 这些都是可执行脚本的「文档类型」。
2. 每个 blob 响应钉死严格 CSP：
   `default-src 'none'; script-src 'none'; object-src 'none'; style-src 'none';
   base-uri 'none'; form-action 'none'; frame-ancestors 'none'`
   —— 即使有类型漏网，脚本也跑不起来。
3. `filename` 进 `Content-Disposition` 前剥离 CR/LF/NUL/引号/反斜杠
   （`safeFilename`），杜绝参数逃逸与后续注入点。

### 复测

| 请求 | 修复前 | 修复后 |
|---|---|---|
| `*.html?inline=1` | `text/html` | `application/octet-stream` |
| `*.js?inline=1` | `text/javascript` | `application/octet-stream` |
| `*.svg?inline=1` | `image/svg+xml` | `application/octet-stream` |
| `*.png?inline=1` | `image/png` | `image/png`（预览保留）|
| 浏览器 title | `XSS_EXECUTED_FROM_STORED_JS` | 空（当附件下载）|

`?inline=1` 在本仓前端/面板里**无任何调用点**（grep `inline=` 于 `front/src`、
`packages/peerdrive-client` 均为空），收紧只影响第三方集成。

---

## 2. MEDIUM — 分享链接 302 Location 注入（已修）

`POST /shares` 不校验 `hash`，`GET /s/:token` 拿它直接拼 302 Location。

payload：`{"hash":"../../../../../../etc/passwd","type":"file"}`
→ `302 Location: /etc/passwd`（路径已折叠；`Location: /sha256sum/s/absolute` 等
同样任意）。虽始终以 `/` 开头出不去 origin，但未校验值仍是后续注入点。

**修复**（`share.go`）：写入时 `hashutil.IsValidSHA256` 形状校验；读取时二次校验
（历史脏行也不放行，回 404 而非重定向）；分享 `filename` 限 255 字节。
实测 8 组畸形 hash 全部 400，合法 64hex 仍签发 token。

---

## 3. MEDIUM — follow-redirects URL provider 未约束 scheme（已修）

`collection_entries.DownloadCollectionFile` 在 `FollowRedirects=true` 时把
operator 存的 URL **原样**放进 Location header。

**修复**：`isHTTPURL()` 只放行**绝对 http(s)**，拒 `javascript:`/`data:`/
`vbscript:`/`file:`/协议相对/相对路径/`ftp:`。19 个用例已覆盖。

---

## 4. LOW/INFO — 需决策，未修

| 项 | 状态 |
|---|---|
| `AllowedOrigins` 默认空 = 允许所有 Origin，且回显 Origin + `Access-Control-Allow-Credentials: true` | 当前鉴权走 Bearer header 非 Cookie，CORS 凭据无额外泄露；改默认拒绝会打断现网部署 |
| 子域通配 `*.example.com` 无 scheme 约束（会匹配 `http://sub.example.com`） | 同上决策包 |
| `/files`、`/files/browse`、`/anon/collections` 匿名可读 | 「公开读」设计；`/files/browse` 已限 storage 根且只回相对路径，无绝对路径泄露 |
| `POST /shares` 的 `filename` 是任意字符串（已限长 255） | JSON 回显安全；仅当 UI 用 innerHTML 渲染才成 XSS（本仓无此调用点）|

## 5. 扫描过但未发现问题的面

- **路径穿越**：`pathutil.Within` 边界 + `TestTraversal` 162 条矩阵。实测
  `/files/browse?path=/etc/passwd` → `path outside storage root`；
  `../`、`%2e%2e%2f`、双编码、`..../` 混编全部被拦。
- **前端 XSS**：`front/src` 无 `innerHTML`/`dangerouslySetInnerHTML`/
  `setAttribute` 拼接；面板 `app.js` 所有 `innerHTML` 均过 `esc()`（含 `&<>"'`）。
- **DOM XSS**：面板 bootstrap 注入走 `json.Marshal`（`panel.go` `jsonStr`），安全。
- **CSRF**：鉴权 Bearer token 非 Cookie，跨站 POST 无法自动携带；`SameSite` 不适用。
- **信息泄露**：access log 走 `json.Marshal`（换行被转义，无日志注入）；
  `X-Request-ID` 限 128 字节；未回显绝对路径；Swagger 默认开但启动即打印告警。
- **分享令牌**：16 字节 `crypto/rand` = 128 bit，30 天过期，不可枚举。
- **点击劫持**：`X-Frame-Options: SAMEORIGIN` + CSP `frame-ancestors 'self'` 已设。

---

## 6. 加固后新增 CI

`.github/workflows/security.yml`（push / PR / 周一定时 / 手动），3 个 job：

| job | 内容 |
|---|---|
| `redteam-regression` | `go test -run 'TestDownload_\|TestShare_\|TestSafeFilename\|TestIsHTTPURL'` —— 9 条回归用例，每处发现固化成用例 |
| `gosec` | back（`-tags nosqlite`）+ `signalserver` + `p2p_bt`（`CGO_ENABLED=0`），medium+，报告存 artifact |
| `npm-audit` | `front`、`packages/peerdrive-media`（仅列有 lockfile 的包），`--audit-level=high`，报告模式 |

回归用例：`back/internal/controller/redteam_security_test.go`。

`TestTraversal` 矩阵不重复挂 —— `ci.yml` 的 `security-probes` job 已在每个 PR 跑。

### gosec 首轮结果（137 条，均报告模式）

人工复核的高价值项：

- **G115 整数溢出**（多处）：多为分片长度/偏移换算，中置信度，需逐条看。
- **G404 弱随机 `math/rand`** ×3：`provider/ipfs.go` backoff jitter、
  `source/url.go`、`bt/remote.go` —— **均为重试抖动，非安全用途**。
  安全敏感随机（分享 token 16B、request id 8B）走的是 `crypto/rand`。
- **G402 `InsecureSkipVerify`**：`echproxy/client.go:138` —— 经
  `GetCertificate`/`DialTLSContext` **限定在 ech-proxy entry host 后缀**内，
  直连上游走系统根证书。**属有意配置**，非漏洞。
- **G704 SSRF 污点分析** ×2（`ech.go`）：`urlguard.GuardPullURL` +
  `SafeDialContext`（含 DNS rebinding 防护）已存在，污点跟踪跨函数断链，属误报。
- **G703 路径穿越污点分析** ×4：HTTP 层已被 `pathutil.Within` 拦下并由
  162 条穿透矩阵守住；源码层污点命中在 `p2p_bt`（独立模块，非 UI 面）。
- **G301/G306 文件权限**（p2p_bt 下载临时文件）：建议后续收紧到 `0600`。

---

## 本地验证命令

```bash
go vet  -tags nosqlite ./internal/... ./pkg/...
go test -tags nosqlite ./internal/controller ./internal/router \
         ./internal/pathutil ./pkg/...
```
