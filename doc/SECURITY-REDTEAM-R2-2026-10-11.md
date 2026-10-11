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
| 信令侧限速忽略 `X-Forwarded-For`（`ratelimit.go` 只用 `RemoteAddr`） | 代码 bug | 反代后所有客户端共享一个桶；代码注释已自认 |
| 信令 CORS 默认 `*`（`cors.go`） | 代码 bug | allow-list 是 opt-in（`WithCORSOrigins`），默认通配 |

**未修以上 7 项**，全部是「修了会打断线上信令/发现部署」或协议固有取舍。
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
