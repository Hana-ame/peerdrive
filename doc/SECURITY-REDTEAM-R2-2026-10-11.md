# 全栈对抗攻击（红队 R2）审计 — 2026-10-11

基准 `refactor` @ `f0b0357`，已含 R1 修复（PR #351，`fix/security-redteam-r3`）。
分支 `fix/security-redteam-r2`。

**范围扩大**：不再只看 UI。本轮覆盖
后端 API 认证授权、输入校验与错误处理、存储层、Range 边界、
P2P / PeerJS 信令协议、基础设施与部署、供应链（Go + npm）。

方法：静态审计 + 绕过 harness + 本地起服务实测 + `npm audit` /
`go version -m` / `strings` 取证。**R1 已修项全部回归验证，无一可绕过。**

---

## 0. R1 修复的绕过验证（全部通过）

| R1 修复点 | 验证方式 | 结果 |
|---|---|---|
| `isHTTPURL` scheme 白名单 | 20 组 payload（`//evil.com`、`http:evil`、`\\evil.com`、`javascript:`、`data:`、`file:`、`ftp:`、`\x00`、`\t`、CRLF、`\@`、`%2f..`） | **全部拒绝** |
| inline 白名单 / `blobCSP` | 27 组 payload（`.html/.js/.svg` 变体、`evil.html.png`、CRLF 注入、路径分隔符、NUL） | **全部降为 `application/octet-stream`** |
| `safeFilename` 头注入剥离 | CR/LF/NUL/单引号/双引号 | **全部剥离** |
| `IsValidSHA256` 双重校验 | 大小写混合 / 长度 / 非 hex | **大小写不敏感、写入与读取都校验** |

---

## 1. CRITICAL — 授权层不存在（水平越权）

`authRequired` 只验证 Bearer token 并写入 `username`/`role`，
**没有任何 controller 读取它做授权**。actor 全部来自
`:username` 路径参数、`body.Username`，或 `nodestate.GetOperator()`。

后者在代码里**没有任何写入方**，恒返回 `""`
（`internal/nodestate/nodestate.go:29-39` 注释自证）。

### 攻击链（多用户部署，A、B 都是合法 token）

```
A → POST /collections {"username":"B","collection_name":"secret"}   # 用 B 的名字建
A → POST /collections/B/secret/entries                              # 写 B 的集合
A → DELETE /collections/B/secret/entries/x                          # 删 B 的文件
A → POST /collections/B/secret/visibility {"visibility":"public"}   # 公开 B 的集合
A → POST /collections/B/secret/rollback                             # 回滚 B 的历史
A → POST /actions/fork {"source_username":"B","source_coll_name":"secret"}  # 偷整份
```

DB 层也无 actor 过滤：`repository.SetCollectionVisibility(username, ...)`
的 `username` 就是攻击者输入。同类问题覆盖 `anon.go` 的 11 个调用点
（匿名集合权限被一个**永久的空字符串**把关 → 有主集合永久锁死、
遗留集合任何人可改）。

### 判定

**需决策项，不在本轮修。** 修它要把认证身份贯穿到所有 handler 与 service，
属于 `doc/ROADMAP.md` 明确排在最后的「身份管理」阶段。
单机单运营者部署（文档默认形态）下此问题不可见。

---

## 2. HIGH — 已修（4 项）

### 2.1 JSON body 无上限 → 内存 DoS

~30 个 `c.ShouldBindJSON` 调用点，全部无 body 上限。
`ShouldBindJSON` 会把整个 body 读进内存，一条巨型 POST 即可 OOM。
multipart 上传有 `MaxUploadBytes`，唯独 JSON 面没设。

**修**：新增 `LimitJSONBody(maxBytes)` 中间件，全局挂载，
只截断 `Content-Type` 含 `json` 的请求，不动 multipart / 二进制流。
默认 1 MiB，`PEERDRIVE_MAX_JSON_BODY_BYTES` 可调，`0` = 不设限。

### 2.2 BT torrent 上传：`make([]byte, header.Size)` → 分配炸弹

`header.Size` 直接来自客户端可控的 multipart Content-Length。
声明 `Content-Length: 10G` 就在读到第一个字节前分配 10 GiB。
且单次 `file.Read` 不保证填满缓冲。

同问题：`/sources/bt/torrent` 用 `io.ReadAll(file)` **完全无上限**。

**修**：新增 `controller.ReadCappedBody(r, max)`（16 MiB 上限，
超限与恰好等于上限都报错，避免静默截断 torrent），两处调用点全部替换。

### 2.3 `os.Remove(fi.Path)` 读 DB 存路径 → 任意删除

`RejectInboxFile` 从 `file_index` 表读 `path` 后直接 `os.Remove`。
`os.Remove` 会沿父目录跟随符号链接，把「删 storage 里的文件」
变成「删宿主上任意文件」。

**修**：改走 `pathutil.SafeRemoveAny([]string{storageDir}, fi.Path)`，
与项目「写路径和读路径同等对待」的硬要求对齐。

### 2.4 `/download/:hash` 出口绕过统一响应头策略

`UniversalDownload` / `UniversalDownloadRefresh` 直接 `c.Data(octet-stream)`，
无 `Content-Disposition`、无 CSP、无 Range、无 gzip 处理，
`/sha256sum` 的同内容响应反而更安全。
另外 `err.Error()` 原文回给客户端（泄露 fetcher 名、网关 URL、上游状态码）。

**修**：两个出口都改走 `setDownloadHeaders`（白名单 + `blobCSP` +
`safeFilename`）+ `handleRangeRequest` + `Content-Encoding: gzip`；
错误改为通用 `"file not found"`，原文进日志。

---

## 3. MEDIUM — 已修（4 项）

| 问题 | 修 |
|---|---|
| CORS 把 `Access-Control-Request-Headers` 原样回显，等于把客户端想要的任意 header 全部声明为跨域可用；配合 origin 回显，泄露的 token 可从任意来源远程调用 | 改为固定白名单 `Authorization, Content-Type, Accept, X-Request-ID, X-Protocol, X-PSK` |
| 带 scheme 的通配符不校验 scheme：`https://*.pages.dev` 匹配 `http://x.pages.dev` | scheme 非空时强制前缀匹配；不带 scheme 的 `*.x.com` 保持任意 scheme（向后兼容） |
| 节点 PeerJS id 只有 32 位熵（`peerdrive-` + `randHex8`），而信令平面把 id 当身份，且离线时对方可注册同一 id 劫持身份 | 提到 128 位（`randHexN(16)`）；下游 `http_discovery` 上限 128 字符，长度增加安全 |
| `PEERDRIVE_ADMIN_TOKEN` 弱令牌可远程爆破（常量时间比较 + 30 rps 限速对低熵无效） | 启动期拒绝：非回环 + 令牌 <16 字符或 <9 种字符 → 拒绝启动，`PEERDRIVE_ALLOW_WEAK_ADMIN_TOKEN=1` 显式放行；回环绑定豁免 |

### 3.5 二进制信息泄露（已修）

`go version -m /tmp/pd-bin` 显示：

```
build vcs.revision=607e83361e6687798d646e77418e1396601afa59
build vcs.time=2026-10-11T12:16:43Z
build vcs.modified=true
```

72 MB 未 strip、含完整 debug_info。攻击者可把二进制精确对应到源码提交，
再定位该提交的其他 CVE。

**修**：三个 workflow（`ci.yml` / `go-build.yml` / `release.yml`）
全部加 `-buildvcs=false`；保留原有 `-s -w`。

---

## 4. HIGH — 供应链已修

`front/package-lock.json` 锁定 `react-router-dom@7.14.2`，
**7 条 advisory（2 HIGH）**，且 `react-router-dom` 是 13 个 src 文件的直接依赖：

| Advisory | 严重度 | 可达性 |
|---|---|---|
| GHSA-8x6r-g9mw-2r78 `__manifest` 无界路径展开 DoS | HIGH 7.5 | 可达 |
| GHSA-chx6-hx7r-mcp5 路由匹配低效 DoS | HIGH | 可达 |
| GHSA-qwww-vcr4-c8h2 RSC Mode CSRF 绕过 | HIGH | SPA+HashRouter，不可达 |
| GHSA-wrjc-x8rr-h8h6 反斜杠 open redirect | moderate | `Link`/`useNavigate` 广泛使用，可达 |
| GHSA-h8fp-f39c-q6mh RSCErrorHandler XSS | moderate | SSR-only，不可达 |
| GHSA-337j-9hxr-rhxg `deserializeErrors` 构造器注入 | moderate | SSR-only，不可达 |
| GHSA-84g9-w2xq-vcv6 PUT/PATCH/DELETE CSRF | low | 无 SSR |

**修**：升到 `7.18.4`（7.x 无破坏性变更），7 条全部覆盖。
`front` 163 个测试 + build 全绿。

`packages/peerdrive-media` 与 `packages/peerdrive-client`：0 漏洞
（后者零运行时依赖、无 lockfile）。

### 4.1 未修（需决策）

- **Go 传输库过旧**：`pion/dtls/v3` v3.1.2（落后 8 个 patch）、
  `pion/webrtc/v4` v4.1.2、`quic-go` v0.59.0。安全关键库
  （`x/crypto`、`x/net`、`gin`、`websocket`、`sqlite`）已是最新。
  无针对这些确切版本的已确认 CVE，建议下个安全更新统一升。
- **`front` 仍有 11 HIGH / 4 moderate**，全部在**开发依赖**
  （tailwindcss / postcss / chokidar / vite / braces / nanoid / ws …），
  不进产物；修复需 tailwindcss v4 major bump。

---

## 5. P2P / 信令协议层

| 发现 | 类型 | 说明 |
|---|---|---|
| 信令 key 硬编码且公开（`pd-signal-<redacted-24hex>`，
  `signalserver/defaults.go:24` + 前端 + AGENTS.md 明文） | **协议取舍** | PeerJS 协议本身就是共享 key；WS 升级只校验 `key == s.key`。
  任何能读到 key 的人可注册任意 peer id。运营者应以 `PEERSIGNAL_KEY` 轮换 |
| 信令消息零认证（OFFER/ANSWER/CANDIDATE/LEAVE 覆盖 `m.Src = cl.id` 后直接 route） | **协议取舍** | 信任客户端是 PeerJS 设计；每 OFFER 创建一个 pion PC → DoS 面 |
| TURN 静态凭据在匿名 `/p2p/webrtc/info` 返回 | **设计（注释自证）** | 代码注释明确说明 TURN 静态凭据本就交给不可信对端，
  因此暴露不构成新增泄露 |
| discovery `/discover/announce`、`/discover/nodes` 匿名 | **协议取舍** | 发现服务本应公开；可投毒、可枚举 |
| XOR 派生用 `SHA256(secret + ":" + connID)`，connID 由 offerer 在 OFFER 中指定 | 代码 bug（低影响） | XOR 注释已声明「轻量混淆非强加密」；connID 另有 128 位随机值，
  数据面默认由 DTLS-SRTP 加密，XOR 是叠加层 |
| 信令侧限速忽略 `X-Forwarded-For`（`ratelimit.go` 只用 `RemoteAddr`） | **有意取舍（代码注释自证）** | 初判为代码 bug，复核后不是：`clientIP` 的注释明确写了理由——`X-Forwarded-For` 是客户端可伪造的，信任它等于让攻击者每次请求换一个新桶（比不限速还糟，因为看起来像有保护）。代价是反代后所有客户端共享一个桶；注释给出的部署建议是「需要在代理层限速」。**未改** |
| 信令 REST CORS 默认 `*`（`cors.go`） | **有意取舍（代码注释自证）** | 初判为代码 bug，复核后不是：`allowCORS` 的注释写了完整理由——公共面板是单文件 `panel.html`，可以 `file://` 双击打开（Origin `null`）或托管到任意静态空间，对信令服务器而言全部跨域；不下发 `Access-Control-Allow-Origin` 时浏览器静默丢弃响应，客户端只看到一句含糊的 `Could not get an ID from the server`。`WithCORSOrigins` 是 opt-in 收紧开关。**未改**（REST 侧） |
| **信令 WS 升级 `CheckOrigin` 硬编码 `true`**（`ws.go`） | **代码 bug — 已修** | 上面那条 CORS 收紧开关只管 REST 端点，**信令 socket 完全不受约束**。结果：运营者花力气把发现 API 从「一 fetch 就全网络普查」收成「只允许自己的面板读」，但任何网页仍能开信令 socket 向任意 peer id 发 OFFER——每个 OFFER 在目标节点上产生一次 WebRTC 分配，且完全未认证（PSK 是 DataChannel 层门禁，不在信令层）。修复：`CheckOrigin: s.originAllowed`，复用同一份 `corsOrigins`。空列表仍全放行（`file://` 面板依赖此行为），配了白名单才拦；无 Origin 头（curl、本仓库自己的 Go dialer）放行 |

**以上 8 项：7 项「修了会打断线上信令/发现部署」或协议固有取舍（含 2 项经复核确认为有意取舍，非 bug），1 项真实代码 bug 已修。**
主服务侧的限速、`TrustedProxies`、`AllowedOrigins` 白名单都已存在。

### 协议层确认安全

- 文件分片 SHA-256 全量校验（`outbound.go:468-478`，range 请求按设计跳过）
- 连接 id 128 位随机；PSK 门禁常量时间比较、30 秒超时
- WebRTC 数据面默认 DTLS-SRTP 加密
- 消息上限：WS 40 KB、announce body 8 KB、文件拉取 8 GB
- `reservedPeerIDs` 阻止 `local` 经信令注册（封死旧 A-9/A-12 admin id 伪造）
- 离线队列每目标上限 100

---

## 6. 需决策项（本轮未改）

| # | 事项 | 现状 | 建议 |
|---|---|---|---|
| 1 | 授权层缺失（§1） | `authRequired` 只认人不查权 | 走 ROADMAP「身份管理」阶段，把认证身份贯穿到 service |
| 2 | `AllowedOrigins` 为空 = 全放行 + `Allow-Credentials: true` | 默认值是显式白名单，只有显式配空才触发 | 可考虑空值改为拒绝并告警 |
| 3 | `/files`、`/files/browse`、`/anon/collections`、`/tags/*` GET、`/peerjs/nodes` 匿名可读 | 公共读设计；tag 读取可枚举已索引文件名 | 若做身份层，一并分级 |
| 4 | `GET /download/:hash`、`/download/:hash/sources` 匿名触发节点外拉取 | 有 30 rps 限速；SHA-256 校验完整性；下载全量进内存 | 加并发上限或按 `MaxUploadBytes` 截断 |
| 5 | `GET /ipfs/:cid` 匿名触发外拉取并落盘 | 内容寻址，落盘是设计 | 需决策 |
| 6 | gin 默认 debug mode | 已在二进制入口改 `ReleaseMode`；测试仍用 `TestMode` | 已完成 |

---

## 7. 防御状态清单（blue-team）

| 层 | 状态 |
|---|---|
| CSP | `default-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'self'`；`/swagger/*`、`/panel/*` 放宽 |
| 下载出口 CSP | 每个 blob 固定 `blobCSP`（`default-src 'none'` 全禁） |
| `Permissions-Policy` | **新增**：`camera=(), microphone=(), geolocation=(), payment=(), usb=(), fullscreen=(self)` |
| `X-Content-Type-Options` / `X-Frame-Options` / `Referrer-Policy` / `Cross-Origin-Opener-Policy` | 已有 |
| CORS | **新增** header 白名单；origin 白名单；无 origin 不下发 CORS 头 |
| 请求体 | **新增** JSON 1 MiB 上限；上传 100 MB / 匿名 10 MB；BT torrent 16 MiB |
| 限速 | 30 rps/IP（回环豁免、OPTIONS 豁免）；信令侧独立限速 |
| 认证 | Bearer + 常量时间比较；无注册服务 + 非回环 → 拒绝启动；**新增** 弱令牌拒绝 |
| 访问日志 | JSON 结构化，刻意不含 Authorization/token/body；UA 截 200 |
| 请求 ID | `crypto/rand` 8 字节，上游透传截断 128 字节 |
| 错误响应 | 已改统一通用文案（下载出口）；**仍存 ~80 处 `err.Error()` 直回**，含 SQLite 错误串与路径 |
| 路径边界 | `pathutil`（`Within`/`WithinAny`/`Safe*Any`，`os.Root` 语义），穿透矩阵 162 条 |
| 文件删除 | **新增** 走 `SafeRemoveAny`，不再裸 `os.Remove` |
| 构建 | **新增** `-buildvcs=false`；`-s -w`；跨平台矩阵 |
| 会话 | Bearer in `Authorization` header（无 cookie → 经典 CSRF 不适用）；token 存 localStorage，风险面见 R1 |
| 默认配置安全 | 无默认凭据；非回环无认证后端 → 拒绝启动；`PEERDRIVE_ALLOW_UNSAFE_ROOT` 拒绝卷根 |
| gin mode | **新增** 二进制入口 `ReleaseMode` |

### 仍需决策/待办

1. **统一错误响应**：~80 处 `err.Error()` 直接回给客户端
   （`collection*.go`、`display.go`、`file.go`、`peer_pull.go`、
   `tag.go`、`stream.go`、`source_routes.go` …），SQLite 错误串会泄露
   表名、约束名、锁状态。需要一个统一 `writeError(c, status, err)`
   + 日志侧记录原文。**改动面大，建议单独 PR。**
2. 面板产物硬编码信令 key（`packages/peerdrive-client/dist/panel.html`）
   —— 与 `signalserver/defaults.go` 同一把公开 key，属协议取舍；
   若要收敛需改面板构建流程。
3. Go 传输库升级（§4.1）。
4. 信令侧三项（scheme 校验、XFF、CORS 默认）在独立 go.mod，
   需同步到 `github.com/Hana-ame/go-peersignal` tag。

---

## 8. 本轮提交内容

新增/修改（`back/`）：

- `internal/config/config.go` — `MaxJSONBodyBytes`；通配符 scheme 校验
- `internal/router/middleware.go` — `LimitJSONBody`；`Permissions-Policy`
- `internal/router/router.go` — 挂 `LimitJSONBody`；抽出 `corsMiddleware`；
  header 白名单常量
- `internal/controller/download.go` — 两个通用下载出口统一走
  `setDownloadHeaders` + Range + gzip；错误不外泄
- `internal/controller/p2p_bt.go` — `MaxTorrentBytes` + `ReadCappedBody`；
  BT torrent 读取加上限
- `internal/router/source_routes.go` — 同上
- `internal/controller/file_inbox.go` — `os.Remove` → `pathutil.SafeRemoveAny`
- `internal/transport/peerjs_dialer.go` — `randHexN`；peer id 128 位
- `internal/transport/peerjs_service.go` — 用 `randHexN(16)`
- `internal/serverapp/app.go` — 弱 AdminToken 启动期拒绝
- `cmd/peerdrive/main.go` — `gin.SetMode(gin.ReleaseMode)`

新增测试：`internal/router/redteam_r2_test.go`（8 组，13 个用例）、
`internal/serverapp/security_status_test.go`（+3 组，含边界单测）。

前端：`front/package.json` + `package-lock.json`（`react-router-dom` → 7.18.4）。

CI：`ci.yml` / `go-build.yml` / `release.yml` 加 `-buildvcs=false`。

---

## 9. 验证证据

### 9.1 单元 / 集成

- `go test -tags nosqlite ./internal/... ./pkg/...` 全绿
- `go test -tags nosqlite -run TestTraversal ./internal/...` 穿透矩阵 162 条全绿
- `go vet -tags nosqlite ./internal/... ./cmd/...` 干净
- `npm test`（front）163/163；`npm run build` 通过；`npm audit` 中
  react-router 相关 7 条清零

### 9.2 本地起服务实测（`-buildvcs=false -ldflags="-s -w"`）

| 探针 | 结果 |
|---|---|
| 非回环绑定 + `PEERDRIVE_ADMIN_TOKEN=123456` | **拒绝启动**：`refusing to serve host "0.0.0.0": PEERDRIVE_ADMIN_TOKEN is too weak (only 6 chars)` |
| 非回环绑定 + 强令牌 | 正常启动，`/ping` 返回 `pong` |
| 回环绑定 + `123456` | **豁免**，正常启动 |
| 带 token，2 MiB JSON body → `POST /collections` | 请求被拒（bind 错误），服务端只读了 ~1 MiB |
| 带 token，**100 MiB** JSON body | **0.34s** 内返回拒绝，未读完 body |
| OPTIONS preflight，`Access-Control-Request-Headers: Authorization, Cookie, X-Internal-Admin, X-PSK` | 回 `Authorization, Content-Type, Accept, X-Request-ID, X-Protocol, X-PSK` —— `Cookie` / `X-Internal-Admin` 不在回包里 |
| `Origin: https://evil.example.com` | 无 `Access-Control-Allow-Origin` |
| 响应头 | 有 `Permissions-Policy: camera=(), microphone=(), geolocation=(), payment=(), usb=(), fullscreen=(self)` |
| `go version -m` 二进制 | **0 行** `vcs.*` / `debug_info` |

> 说明：无 token 的巨型 JSON 请求会先被认证挡下（401），
> 此时 body 根本没被读，内存零消耗；`MaxBytesReader` 是对**已认证**
> 调用方的兜底。

### 9.3 本轮自己踩的两个坑（已修）

1. `-buildvcs=false` 写进了 `-ldflags` 里 —— 它是 `go build` 的开关，
   不是链接器开关，5 个平台交叉构建全部 `flag provided but not defined: -buildvcs` 失败。
   已挪到 `-tags` 旁边。
2. 报告里把线上信令 key 原样引用了一遍，触发了
   `test/deployconsistency/TestSignalKeyIsConsistentAcrossRepo`：
   该用例把仓库里所有字面量跟 `config.DefaultSignalKey` 逐个比对，
   不一致就报漂移（key 写错正是客户端连不上信令的常见原因）。
   文档引用同样会触发，已改成 `pd-signal-<redacted-24hex>`。

### 9.4 顺手修掉的一个 CI 配置

`e2e.yml` 的管理面冒烟节点用的是 `PEERDRIVE_ADMIN_TOKEN=ci-test-token`
（13 字符 / 6 种字符），正是新加的那道弱令牌闸门要拦的东西。
**没有放宽闸门**，而是把 CI 的令牌换成合规的
`ci-test-token-strong-7x9`（24 字符 / 14 种字符），
三处（env、`E2E_ADMIN_TOKEN`、零配置面板节点）同步更新。

---

## 10. R2 续 — 依赖链 triage 完成（front）

前一轮只修了 `react-router-dom`。本轮把 `npm audit` 的 **15 项（11 high + 4 moderate）全部逐项裁决**，
结果是 **10 项已修、1 项以「无兼容修复」归档、其余为其连锁项**。

### 10.1 已修（10 项）

| 包 | 版本变化 | 关闭的 advisory |
|---|---|---|
| `postcss` | 8.5.10 → **8.5.29** | GHSA-6g55-p6wh-862q / GHSA-fxqj-rqcc-2cmp / GHSA-r28c-9q8g-f849（sourceMappingURL 任意文件读） |
| `vite` | 8.0.10 → **8.3.4** | GHSA-v6wh-96g9-6wx3（launch-editor UNC NTLMv2）/ GHSA-fx2h-pf6j-xcff（`server.fs.deny` Windows 旁路） |
| `vitest` | 4.1.5 → **4.1.11** | GHSA-82fw-gwwq-j7x9（`@vitest/mocker` 重定向 mock 路径穿越） |
| `@vitest/mocker` | 4.1.5 → **4.1.11**（随 vitest） | 同上 |
| `browserslist` | 4.28.2 → **4.29.3** | GHSA-c83g-rgw3-j3cx（无缓存淘汰 OOM）/ GHSA-73wf-gq98-2v4g（untrusted stats prototype write） |
| `nanoid` | 3.3.11 → **3.3.20** | GHSA-28wg-ghj8-5hjv / GHSA-2v37-7h3g-55p8（无限循环）/ GHSA-xwg4-73v4-xw9w（整数溢出） |
| `source-map-js` | 1.2.1 → **1.2.2** | GHSA-68fv-2mgg-jv7q（event-loop DoS） |
| `ws` | 8.20.0 → **8.22.0** | GHSA-58qx-3vcg-4xpx（未初始化内存泄露）/ GHSA-96hv-2xvq-fx4p（分片耗尽 DoS） |
| `baseline-browser-mapping` | 2.10.21 → **2.11.28** | GHSA-w5vr-8v7q-w6rv（非法输入进程终止） |
| `postcss-selector-parser` | 6.1.2 → **7.1.6** | GHSA-w9m9-85wc-3x92（AST 递归 DoS）/ GHSA-rj75-hqrm-r3gf（二次复杂度） |

前三项走 `devDependencies` 直接升级；其余七项是传递依赖，走 `package.json` 的 `overrides` 强制锁定
（前三项升级后 `nanoid`/`source-map-js` 自然带上，另外三项与 psp 需显式 override）。
**验证**：`npm ci` + `npm test` 19 文件 / **163 用例全绿** + `npm run build` 通过。
`@testing-library/dom`（10.4.1）在锁文件里保留完好——第一次用 `--legacy-peer-deps` 装会把 peer 依赖整个丢掉，
表现为 19 个测试文件全部 `Cannot find module '@testing-library/dom'`，**已弃用该 workaround**。

### 10.2 无兼容修复（1 项根因，连锁 5 项计数）

`npm audit` 现在只剩 5 项 high，**全部是同一个根因**：

```
braces 3.0.3  ← GHSA-vfj7-8cjw-p6xm（深层嵌套模式导致栈耗尽）
  ← micromatch 4.0.8（所有版本都依赖 braces）
      ← fast-glob 3.3.3（所有版本都依赖 micromatch）
          ← chokidar 3.6.0
              ← tailwindcss 3.4.19
```

**`braces` 最新发布版就是 3.0.3，本身即漏洞版本——上游没有修复版本。**
`npm audit fix --force` 声称「会装 tailwindcss@4.3.3」，那是**主版本升级**，不是补丁：
Tailwind v4 改了配置形态（`tailwind.config.js` → CSS `@theme`）、入口从
`@tailwind base/components/utilities` 改成 `@import "tailwindcss"`、postcss 插件从
`tailwindcss` 换成 `@tailwindcss/postcss`。`front/` 目前 `@apply` 出现在 6 处、
`@layer base/components` 各有一段、自定义颜色变量 10+ 个，迁移是独立工作流。

**裁决：接受风险，不做主版本迁移。** 理由：
1. 全部 5 项都是 **dev-only 构建期**依赖，不在任何产物里；`dist/` 里没有任何 braces 代码。
2. 攻击面是「构建时能控制 Tailwind 配置或 CSS 源内容的攻击者」，即攻击者已经能改前端源码。
3. 利用方式是栈耗尽 DoS（构建机崩），不是数据泄露或 RCE。

**决策留给运营者**：若要清零，走 Tailwind v4 迁移（估计 0.5–1 人天，需重做 `tailwind.config.js` 与
`src/index.css` 的全部 `@apply`/`@layer`）。

### 10.3 踩到的 npm 工具链坑

`npm@10.9.8` 在此仓库上**任何**需要重算 ideal-tree 的安装都会崩：
`TypeError: Cannot read properties of null (reading 'edgesOut')`
（栈在 `arborist/build-ideal-tree.js #loadPeerSet`，触发点在取 `happy-dom@*` 之后）。
`npm ci` 与 `npm install --package-lock-only`（锁文件未变时）能过，一旦 `package.json` 变动即崩。
`--legacy-peer-deps` 能绕过崩溃但**会丢掉 peer 依赖**，后果是 `@testing-library/dom` 消失、
19 个测试文件全挂。**解决：`npx npm@12 install`**（12.2.0 正常）。
建议 CI 的 front job 显式固定 npm 12，否则依赖升级会卡在工具链而不是代码上。

---

## 11. R2 续 — token 存储方案评估

用户要求评估 `localStorage` → `httponly cookie` 的可行性。**结论：不可行，且不是取舍问题，是传输形态决定。**

### 11.1 现状

| 项 | 值 |
|---|---|
| 存储位置 | `localStorage['peerdrive_auth_token']`（JWT）/ `['peerdrive_auth_key']`（节点 PSK），见 `front/src/platform/shared/storageKeys.js` |
| 传输方式 | **WebSocket 帧内带 `token` 字段**（`transport-ws/client.js:536/562`：`{type:'admin', method, path, body, token, reqId}`） |
| HTTP `Authorization` 头 | **前端从不发送**（全仓 `grep Authorization` 零命中） |
| 消费端 | `/ws/peer` 本地管理会话，内部转发 gin engine 复用 HTTP controller |

### 11.2 为什么 httponly 在这里不可行

1. **httponly 只对 HTTP 请求生效。** 浏览器发 WebSocket 握手时**不发送 cookie**
   （`fetch` 的 `credentials` 对 WS 不适用，握手是纯 GET 升级）。把 token 换成 cookie 后，
   WS 帧里的 `token` 字段必须从别处取——而「别处」只能是 JS 可读的位置，即绕回 `localStorage`。
   净收益为零。
2. **前端本来就没在用 HTTP Bearer。** 后端的 `authRequired` 中间件（Bearer 头）只服务 API 调用方；
   管理面板走的是 WS 帧内 token，`authRequired` 在 `/ws/peer` 这条路上根本没参与。
   所以「改成 httponly cookie 让 HTTP 调用更安全」这个方向在管理面**没有对应物**。
3. **CSRF 本来就不适用。** 后端认证是纯 Bearer，不读 cookie（`grep Set-Cookie` 后端零命中），
   浏览器不会因为同站页面发个请求就带上身份。CSRF 的前提条件（cookie 自动附带）不存在。

### 11.3 localStorage 的真实风险与已有缓解

风险：任意同源 XSS 可读 `localStorage` → 拿到 JWT/PSK。这是 localStorage 的固有属性，与本项目无关。

已有缓解：
- **前端现已加 CSP**（见 §12）：`default-src 'self'` 且**无 `'unsafe-inline'`**。
  这意味着注入 `<img onerror=...>` 或内联 `<script>` 都不执行——
  这正是把「stored-XSS 原语」变成「可读 token」的那一步，也是 CSP 在此处唯一有意义的价值。
- 构建产物 `dist/index.html` 经核验：**0 个可执行内联脚本**
  （唯一 `<script>` 是 `type="module" src="/peerdrive/assets/..."` 外部模块；
  另有一个 `<script>` 出现在 HTML 注释里，不执行）。所以 `script-src 'self'` 不会打断应用。
- 前端部署在 `peerdrive.pages.dev`，管理面节点是另一个源，
  攻击者要读到 token 必须先在 pages.dev 拿到 XSS。

### 11.4 token 可预测性

JWT 由注册服务签发（`STORAGE_KEY_REG_SERVER_URL`，默认 `https://account.moonchan.xyz`），
不在本仓库；本仓库无法评估其随机源。节点侧 PSK 是可配明文（`PEERDRIVE_PSK`），
运营者自控强度。前端对两者都是透明传递，**不做任何派生或变换**，
因此不存在前端引入的可预测性问题。
唯一的前端可控熵源是 PeerJS peer id（`peerdrive-` + 16 hex = 128 位，§2 已修）。

### 11.5 建议

不做迁移。真正能降低 XSS→token 泄露链路的动作是 §12 的 CSP（已完成）+
前端产物保持零内联脚本（已核验，建议在 CI 加一条断言防止回归）。

---

## 12. R2 续 — CSP 收紧（两处）

### 12.1 后端 `/panel`、`/swagger`：从「完全不给 CSP」降级为「只放宽 inline」

原实现是 `if !disableCSP && !inlineHTML` ——单文件页面**完全不带 CSP**，
等于同时放弃了 `object-src 'none'`、`base-uri 'self'`、`frame-ancestors 'self'` 三条
与 inline 无关的硬保护。改为下发 `inlineHTMLCSP`：

```
default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline';
object-src 'none'; base-uri 'self'; frame-ancestors 'self'
```

### 12.2 顺手修了一个前缀匹配 bug

原判定是 `strings.HasPrefix(path, "/panel")`，会让 `/panels`、`/swaggerish`
这类无关路径也拿到放宽策略。测试先暴露了它（`/panels` 断言严格策略，实际收到放宽）。
改为按路径段匹配：`p == "/panel" || strings.HasPrefix(p, "/panel/")`。
**与 AGENTS.md「别用 `strings.HasPrefix(a+"/")` 做路径检查」同一类问题——那条针对文件系统，
这里针对 URL 路径，但错误模式完全一致。**

### 12.3 前端 Pages 部署：从「零安全头」到全套

`pages.yml` 部署的完整 UI **之前没有任何安全头**——无 CSP、无 nosniff、无 X-Frame-Options。
`public/_headers` 让 vite 原样拷进 `dist/`，再由 `cp -r front/dist/* _site/` 带上线，
**无需改 workflow**。已下发：

```
X-Content-Type-Options: nosniff
X-Frame-Options: SAMEORIGIN
Referrer-Policy: no-referrer
Permissions-Policy: camera=(), microphone=(), geolocation=(), payment=(), usb=(), fullscreen=(self)
Content-Security-Policy: default-src 'self'; style-src 'self' 'unsafe-inline';
  img-src 'self' data: blob:; media-src 'self' data: blob:; font-src 'self' data:;
  object-src 'none'; base-uri 'self'; frame-ancestors 'self'; form-action 'self';
  connect-src 'self' ws: wss: http: https:
```

`connect-src` 故意放宽（§11.3 解释了为什么）：UI 连的是运营者自选节点
（`peerdrive_api_base`，默认 `https://wsl-3000.moonchan.xyz`），锁成 `'self'` 会把每个
非同源部署全部打断。CSP 在此的真实价值是其余指令 + **无 `'unsafe-inline'`**。

### 12.4 测试

`internal/router/redteam_r2_test.go` 新增 3 组：
- `TestSecurityHeaders_CSP` — 11 个路径表驱动，覆盖严格/放宽/前缀不匹配
- `TestSecurityHeaders_CSPStillHardenedOnRelaxed` — 放宽策略必须保留三条硬保护、且绝不出现 `unsafe-eval`
- `TestSecurityHeaders_DisabledCSP` — `PEERDRIVE_CSP=off` 仍完全关闭，其余头不受影响

---

## 13. R2 续 — 错误信息控制（blue-team，最大单项）

### 13.1 发现

全仓扫描 `internal/controller/*.go`，找到 **75 处** 5xx 响应直回驱动原文：

```go
c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
```

本仓库 `err.Error()` 的组成以 SQLite 消息为主：
`UNIQUE constraint failed: collections.collection_name`、`database is locked`、
`no such table: file_index`、`attempt to write a readonly database`、
`open /srv/peerdrive/storage/ab/abcdef: permission denied`。
也就是说**任意未认证调用方都能反推出表名、约束名，以及区分「不存在」和「被锁」**。
这是 R1 报告里 `{"error":"file not found"}` 泛化没做到位的完整版。

### 13.2 修复

新增 `internal/controller/errors.go`：

```go
func writeInternalError(c *gin.Context, err error) {
	if err == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	log.LogError("ctrl: %s %s failed: %v", c.Request.Method, c.FullPath(), err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
}
```

75 处全部收敛到该函数：客户端拿到固定字符串，真实错误进结构化日志。
覆盖 15 个文件（`anon.go`/`collection*.go`/`file.go`/`file_inbox.go`/`fork.go`/`merge.go`/
`node_market.go`/`p2p*.go`/`share.go`/`sync.go`/`tag.go`），
其中 `sync.go` 的接收者名是 `ctx` 不是 `c`，`anon.go` 有 4 处是
「先用 `strings.Contains` 归类成 4xx、归类失败落 5xx」的兜底分支。

### 13.3 刻意不动的

- **4xx 校验错误不动**：`{"error":"passcode required"}`、`{"error":"invalid visibility"}`
  等是面向用户的提示，本身是设计的一部分，不是泄露。
- **`strings.Contains(err.Error(), ...)` 的归类逻辑不动**：那是错误分类，不是回传给客户端。
- **JSON schema 不动**：`error` 字段仍是字符串，客户端无需改动。

### 13.4 测试

`internal/controller/redteam_r2_errors_test.go`（新增）3 组：
- `TestWriteInternalError_NeverLeaksDriverText` — 6 个真实驱动错误（含 SQLite 四种 +
  绝对路径 + TCP 超时），断言 body **逐字节等于** `{"error":"internal error"}`，
  且不含 `collections.collection_name` / `database is locked` / `file_index` /
  `readonly` / `/srv/peerdrive` / `i/o timeout` / `UNIQUE` 任一片段
- `TestWriteInternalError_NilErrorStill500` — nil 也是 500，不可能是 200 或空 body
- `TestWriteInternalError_ResponseIsIdenticalAcrossFailures` — 4 种不同故障的响应**字节相同**
  （调用方无法从响应区分具体失败原因，这正是目的）

---

## 14. R2 续 — 本轮提交与验证

| 项 | 状态 |
|---|---|
| `back/internal/controller/errors.go` | 新增 |
| 75 处 5xx 收敛到 `writeInternalError` | 已改（15 文件） |
| `back/internal/controller/redteam_r2_errors_test.go` | 新增，3 组 |
| `back/internal/router/middleware.go` | CSP 降级 + 前缀段匹配修复 |
| `back/internal/router/redteam_r2_test.go` | +3 组 CSP 用例（11 路径表驱动） |
| `back/signalserver/cors.go` | 新增 `originAllowed` |
| `back/signalserver/ws.go` | `CheckOrigin: s.originAllowed` |
| `back/signalserver/cors_test.go` | +1 组 12 用例 |
| `front/package.json` + lock | 3 个 devDep 升级 + 4 项 overrides（含 `postcss-selector-parser` 7.1.6） |
| `front/public/_headers` | 新增，Pages 安全头 |

**验证**（本地全绿）：
- `go vet -tags nosqlite ./...` → 0
- `bash scripts/check-layer-deps.sh` → 0 violations
- `go test -tags nosqlite ./internal/... ./pkg/... ./test/... -count=1` → 全绿
- `go test -tags nosqlite -run TestTraversal ./internal/...` → 全绿（162 条穿透矩阵）
- `go build -tags nosqlite -buildvcs=false -ldflags="-s -w"` → 52 607 048 bytes
- `cd back/signalserver && go vet ./... && go test ./... -count=1` → 全绿
- `cd front && npm ci && npm test` → 19 文件 / 163 用例全绿
- `cd front && npm run build` → 通过；`dist/_headers` 已进产物
- `cd packages/peerdrive-client && npm run build:panel` → `dist/panel.html` 120.8 KB，与内嵌副本逐字节一致

**未决**（需运营者决策，见 §6）：授权层缺失、`/p2p/forward/create`+`/connect` 无界出站 TCP、
文件级 IDOR、`/tags/*` 匿名全量索引、admin 面经 `/ws/peer` + `https://*.pages.dev` 白名单、
匿名下载/IPFS 出口放大、Go 传输库陈旧（`pion/dtls/v3` v3.1.2 / `pion/webrtc/v4` v4.1.2 /
`quic-go` v0.59.0，具体版本未见确认 CVE）、面板内嵌信令 key、
**Tailwind v4 迁移以清零 braces 链（§10.2）**。
