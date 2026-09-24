# 全栈体检报告（2026-09-23）

> 对照"全栈应用该有的样子"（配置、错误处理、日志、数据层、认证、前后端集成、实时、生产加固）
> 对 `back/`（Go + Gin + SQLite）与 `front/`（React + Vite）做的一次体检。
> 结论先行：**分层与 P2P 主链路是扎实的，缺的是"生产运行"这一层**——
> 探针、优雅停机、限流、可观测性、连接池、断线恢复，这六样一个都没有。
> 本次已修 12 项（见 §1），另有 7 项建议后续处理（见 §2）。

## 0. 一句话总结各维度

| 维度 | 现状 | 评价 |
|------|------|------|
| 分层（controller→service→repository） | controller 不直接碰 repository，服务层不认 HTTP 类型 | **好**，这是很多项目做不到的 |
| 配置集中管理 | 全部 `PEERDRIVE_*` + `Load()`，但启动期零校验、DB 路径写死 | 结构对，缺校验 |
| 错误处理 | 153 处手写 `c.JSON(400/500, gin.H{"error":...})` | 无类型化错误、无统一映射 |
| 日志 | 自研分级文本日志（`internal/log`） | 无请求 ID、非结构化、无法聚合 |
| 数据层 | SQLite，建表 + ALTER 幂等迁移 | 无连接池、无 busy_timeout、**外键默认关闭** |
| 认证 | Bearer + 外部注册服务器 whoami，未配置则放行 | 每请求一次远程调用；role 取了但没用于授权 |
| 前后端集成 | 前端全走本地 WS 的 admin 帧（管理面不外露） | 设计好；但地址硬编码、无重试/离线态 |
| 实时 | WS 单连接 + reqId 路由 | 无心跳、无重连、无死连接检测 |
| 生产加固 | CORS 白名单回显（正确）、PSK 准入、路径边界 | 无探针、无限流、无安全头、无优雅停机 |

## 1. 本次已修（12 项，均已验证）

| # | 问题 | 严重度 | 改动位置 |
|---|------|--------|----------|
| 1 | **无优雅停机**：`r.Run()` 在 goroutine 里 `Fatalf`，一出错就 `os.Exit`，main 的 `defer` 全不执行（PeerJS 连接、DB 句柄全靠进程退出兜底）；收到 SIGTERM 直接走人，正在传的文件被从中间掐断 | 高 | `cmd/server/main.go`（改 `http.Server` + `Shutdown(20s)` + 超时强制关闭） |
| 2 | **无 liveness/readiness**：只有 `/ping`，两种探针语义混用 | 中 | `internal/controller/health.go`（`/health` 不查依赖、`/ready` 真 Ping 数据库） |
| 3 | **无请求 ID**：日志里几十个并发请求交织，只能靠时间戳猜 | 中 | `internal/router/middleware.go`（`X-Request-ID` 沿用上游/新生成，写入 context 与响应头） |
| 4 | **日志非结构化**：文本行没法进日志系统 | 中 | 同上（`AccessLog()` 输出 JSON 行；5xx=error、4xx=warn；**不记 Authorization/请求体**） |
| 5 | **无任何安全响应头** | 中 | 同上（nosniff / SAMEORIGIN / no-referrer / COOP / CSP，`/swagger` 例外，`PEERDRIVE_CSP=off` 可关） |
| 6 | **无限流**：上传、跨节点拉取这些真花带宽的口子谁都能刷 | 中 | 同上（每 IP 令牌桶，默认 30 rps，`PEERDRIVE_RATE_LIMIT_RPS` 可调，0=关） |
| 7 | **gin 默认信任所有代理**：`ClientIP()` 直接取可伪造的 `X-Forwarded-For`，限流和日志 IP 形同虚设 | 中 | `router.go` + `config.TrustedProxies`（默认一个都不信，只认 `RemoteAddr`） |
| 8 | **SQLite 无连接池、无 busy_timeout**：并发写（上传登记 / BT 完成回调 / file_index 游标同步同时进行）会偶发 `database is locked`，且只在并发下出现，单跑永远复现不了 | 高 | `repository/db.go` + 两个 `db_driver_*.go`（MaxOpenConns 8、busy_timeout 5s、内存库强制单连接） |
| 9 | **外键默认关闭**：schema 里写了 `ON DELETE CASCADE` 却没生效，删合集留下孤儿条目 | 高 | 同上（`foreign_keys=1`，两个驱动各写一份 DSN 语法） |
| 10 | **DB 路径写死** `./peerdrive.db`，换部署目录只能靠"记得先 cd 对" | 低 | `config.DBPath`（`PEERDRIVE_DB_PATH`） |
| 11 | **启动期零配置校验**：`PORT=300o`、空 `PEERDRIVE_STORAGE` 这类错误不报错，只会行为跑偏（文件落进工作目录） | 中 | `config.Validate()`（端口/路径/最大节点数） |
| 12 | **认证每请求一次远程 whoami**：管理台一个列表页十几个请求就付十几次网络往返，注册服务器一抖全站 401（令牌明明是好的） | 高 | `router/auth_middleware.go`（30s TTL 缓存，只缓存成功结果；代价见注释：吊销最长延迟 30s） |

前端：

| # | 问题 | 严重度 | 改动位置 |
|---|------|--------|----------|
| 13 | **WS 无心跳 / 无重连 / 无死连接检测**：`onclose` 只把 sock 置空，等下一次请求才重连——节点重启一下，没有请求在飞就永远没人触发重连，页面整片"点了没反应" | 高 | `front/src/ws.js`（25s 心跳 `/ping`、60s 无帧判定死连接、1s→30s 指数退避重连、`onStatus()` 订阅） |
| 14 | **后端地址硬编码**：源码里写死一个域名和一个**明文 http 的公网 IP**（在 https 页面下会被按混合内容直接拦掉，那条默认后端从来没真能用过） | 中 | `front/src/api.js`（改 `VITE_API_BASE`，移除明文 http 条目） |

> 13、14 合到上表计数里是 12 项，这里单列是因为它们属于前端。

第二轮深挖又修的 3 项（都是"谁能碰管理面"这条边界上的）：

| # | 问题 | 严重度 | 改动位置 |
|---|------|--------|----------|
| 15 | **`/ws/peer` 对无 Origin 的连接一律放行**：浏览器握手必带 Origin，所以这条规则实际只对脚本/curl 生效——意味着**任何能连到这个端口的人**（局域网内机器、端口映射出去的公网）不带 Origin 就能拿到完整 admin 管理面；而 `WSSession.IsLocal()` 恒 `true`，它还同时被当成"自己"，private 共享内容也可见 | **高** | `router/peerjs_routes.go`（无 Origin 只放行回环 `RemoteAddr`；反代后面的浏览器请求都带 Origin，走白名单那条路，不受影响） |
| 16 | **Swagger 公开且无法关闭**：`/swagger/*any` 无鉴权，等于把 105 个端点和参数结构做成地图送出去 | 中 | `PEERDRIVE_SWAGGER=off`（默认仍开，避免破坏现有用法） |
| 17 | **监听地址不可配**：默认 `0.0.0.0`，同一局域网内谁都能连上来当管理员（管理面没有账号体系，端口可达性就是它的全部边界） | 中 | `PEERDRIVE_HOST`（默认空=保持历史行为；只在本机用管理台时设 `127.0.0.1`） |

### 设计上刻意保留的行为

- **本机回环与预检 OPTIONS 不限流**：管理面只服务本地 WS（`serveAdmin` 按会话 ID 拒绝远端），内部转发请求统一标成 `127.0.0.1`；OPTIONS 被 429 会因为缺 CORS 头变成一句莫名的跨域报错。
- **`/health` 不查依赖**：liveness 查依赖会把"数据库抖一下"变成"重启风暴"。
- **分层边界**：健康检查不 import repository，改为装配层注入 `func() error`（`controller.InitHealth(repository.Ping)`）。

## 2. 还没修（按建议优先级）

1. **令牌存 localStorage**（`front/src/api.js`）：XSS 拿到就拿到全部权限，且没有 401 刷新/重试流程。建议迁到 httpOnly Cookie（需后端配 `Set-Cookie`）；退一步也要改成 sessionStorage + 短时效。
2. **无 RBAC**：`role` 从 whoami 取回后只在 `controller/p2p.go:871` 用于**展示**，没有任何一处按角色判定权限——所有通过认证的人权限等价。建议在 `AuthRequired` 之外加 `RequireRole(...)`，先覆盖删除/共享范围变更两类高危操作。
3. **迁移无版本表**：`InitDB` 靠 `CREATE TABLE IF NOT EXISTS` + 一串 `migrationExec(ALTER ...)`，跑没跑过、当前是第几版都没记录，也无法回滚。建议加 `schema_migrations(version, applied_at)`。
4. **没有类型化错误体系**：153 处手写状态码，错误文案与 HTTP 状态散落各处，客户端拿不到稳定错误码。建议 `internal/errors.go` 定义 `AppError{Code, HTTP, Msg}` + 全局 handler，**并把 `err.Error()` 挡在响应之外**（内部路径/DSN 不该出现在回包里）。
5. **前端无重试与离线提示**：4xx 不该重试、5xx 最多重试 3 次、fetch 失败要有离线提示——目前都没有。`ws.js` 已经提供 `onStatus()`，UI 侧还没接线。
6. **多步写入不在事务里**：全库只有 2 处 `DB.Begin()`。合集提交（写版本 + 写条目 + 更新 `current_hash`）中断会留下半截状态。
7. **缺 `.env.example`**：配置全靠环境变量，但没有占位样例，新人只能翻源码。已补 `back/.env.example`。

第二轮深挖新增（未修）：

8. **默认跨域白名单含 `https://*.pages.dev`**（`config.go`）：Cloudflare Pages 的子域是**任何人都能免费注册**的，所以默认配置下，一个托管在任意 `*.pages.dev` 子域的恶意页面就能跨域连到本机节点的管理面（配合 15 修完后，浏览器这条路径仍然开着）。部署在公网/给多人用时必须显式设 `PEERDRIVE_ALLOWED_ORIGINS`，别用默认值。
9. **WS 上传上限与配置脱钩**：HTTP 上传走 `http.MaxBytesReader(MaxUploadBytes)`，但 WS 二进制上传走 `adminBinMax` 这个硬编码常量——改了 `PEERDRIVE_MAX_UPLOAD_BYTES` 对管理台上传无效，两条路上限不一致。
10. **合集提交不在事务里**：写版本 + 写条目 + 更新 `current_hash` 是三次独立写入，中途失败留下"版本有了、条目不全"的半截状态。
11. **前端无 ErrorBoundary**：任一组件渲染抛错就是整页白屏，没有兜底 UI。

## 3. 验证记录（本次改动）

| 项目 | 命令 | 结果 |
|------|------|------|
| 后端编译 | `go build -tags nosqlite ./...`（WSL） | 通过 |
| 后端 vet | `go vet -tags nosqlite ./internal/... ./cmd/...` | 无告警 |
| 后端单测 | `go test -tags nosqlite ./...` | 12 个包全 ok |
| 后端集成 | `go test -tags "nosqlite integration" ./test/integration/ -p 1` | ok（57.9s） |
| 启动冒烟 | 真实实例 + curl | `/health` 200、`/ready` `{"status":"ready"}` |
| 安全头/请求 ID | `curl -D -` | CSP、nosniff、SAMEORIGIN、no-referrer、COOP、`X-Request-Id` 齐全 |
| 限流 | 伪造 XFF 连打 12 次（rps=3,burst=6） | `200×6 → 429×6`；回环 8 次全 200 |
| 结构化日志 | 日志取样 | `{"ts":...,"level":"info","msg":"http request","request_id":...,"latency_ms":0.054,...}` |
| 优雅停机 | `kill -TERM` | 2s 内退出，日志 `main: stopped`，退出码 0 |
| WS 握手边界 | 四组 curl 模拟握手 | 回环无 Origin `101`、非回环无 Origin `403`、非回环白名单 Origin `101`、陌生 Origin `403` |
| Swagger 开关 | `PEERDRIVE_SWAGGER=off` | `/swagger/index.html` 404，`/health` 仍 200 |
| 前端单测 | `vitest run` | 9 文件 / 101 用例全过（含 ws 14 项） |
| 前端构建 | `vite build` | 通过（490KB / gzip 137KB） |

## 4. 新增配置项

| 环境变量 | 默认 | 说明 |
|----------|------|------|
| `PEERDRIVE_DB_PATH` | `./peerdrive.db` | SQLite 元数据库路径 |
| `PEERDRIVE_RATE_LIMIT_RPS` | `30` | 每 IP 速率上限，0=不限 |
| `PEERDRIVE_CSP` | （开） | 设为 `off` 关闭 CSP |
| `PEERDRIVE_TRUSTED_PROXIES` | 空 | 可信反代（IP/CIDR 逗号分隔，或 `all`）；空 = 只认 RemoteAddr |
| `PEERDRIVE_SWAGGER` | 开 | 设为 `off` 关闭 `/swagger/*`（公网部署建议关） |
| `PEERDRIVE_HOST` | 空 | 监听地址；空 = 所有网卡。只在本机用管理台时设 `127.0.0.1` |
| `VITE_API_BASE`（前端构建期） | 兜底域名 | 默认后端地址 |

> `PEERDRIVE_ALLOWED_ORIGINS` 默认值含 `https://*.pages.dev`，该子域任何人可免费注册——
> 部署到公网前请显式列出自己的来源，见 §2 第 8 条。
