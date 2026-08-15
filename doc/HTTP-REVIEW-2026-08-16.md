# HTTP 层审阅报告 — 对照 wintools/webrtc-proxy 修复清单 (2026-08-16)

> 审阅时间: 2026-08-16
> 背景: 沿用 `doc/TRANSPORT-REVIEW-2026-08-15.md` 的同一清单，审阅 peerdrive 的
> **HTTP controller 层**（`back/internal/controller/` 全部 15 个文件 + `internal/router/`）。
> 传输层（PeerJS/WebRTC/WS/libp2p）已在上轮审阅并修复，本轮只查 HTTP 面，
> 服务层已有修复项（H1-H7）不重复报告，仅交叉引用。
> 结论: 传输层加固后，**HTTP 层成为本节点攻击面最大的入口**。发现 **5 个高危**（无认证
> 全匿名、任意文件读/写、本地端口转发劫持、SSRF）、**8 个中危**、**5 个低危**，
> 另列出已确认安全的端点清单。所有行号基于审阅当日 main 分支。
> 修复状态: **待修复**（本文档仅报告，未改动任何代码）。

---

## 高危 (优先修, 按顺序)

### H1. 全站无认证 + 无归属校验 — `router.go:89-92` + `auth_middleware.go:21-69`

`r.Use(AuthOptional())`（`router.go:91`）只是往 context 塞两个标志，
`AuthRequired`（`auth_middleware.go:48-69`）**定义了但从没被 mount**（全仓 grep 无调用）。
当 `cfg.RegistrationServer == ""` 时连 AuthOptional 都不装——**整个 API 零认证**。

后果（全部匿名可达）:
- `CreateCollection`（`collection.go:50-76`）的 `username` 来自**请求体**——可冒充任意用户建集合。
- `AddEntry`/`RemoveEntry`/`CommitCollection`/`RollbackCollection`/`SetCollectionVisibility`/
  `UpdateCollectionTags`（`collection.go:189/232/322/441/494/568`）、`MergeFromSource`
  （`merge.go:41-153`）、`ForkCollection`（`fork.go:32-87`）**无任何归属校验**——用户 A 可读改写删
  用户 B 的集合。
- `GetCollection`（`collection.go:134-175`）与 `DownloadCollectionFile`（`:263-305`）
  **从不检查 `col.Visibility`**——"private" 集合形同虚设，知道名字即可读
  （名字还可经 `ListCollections`/`SearchCollections`/`ListPublicCollections` 枚举）。

修法: 所有变更类路由与 `/files`、`/collections`、`/local`、`/p2p/forward*`、`/p2p/download*`、
`/p2p/sync` 挂 `AuthRequired`；`username` 一律取自 token 而非 body/path；读路径校验 visibility。

### H2. 任意文件读取: `register_local` 绝对路径 + `LocalFetcher` 兜底直读 — `file_service.go:52-125` + `universal_downloader.go:73-100`

- `RegisterLocal`（`file_service.go:62-65`）对绝对路径直接 `os.Open`，**不锚定任何根目录**，
  并把绝对路径原样写进 provider（`:115`）。
- `RegisterFolder`（`file_service.go:128-169`）对任意绝对目录 `filepath.Walk`，效果相同。
- `LocalFetcher.Fetch`（`universal_downloader.go:76, 86-100`）对绝对 provider path
  直接 `os.ReadFile(path)`，**无条件**，hash 对上就返回。

攻击链: `POST /files/register_local {"path":"/etc/passwd"}` → 拿 hash →
`GET /download/<hash>`（`download.go:229-249`，hash 校验通过）→ **文件内容到手**。
`BrowseDir`（`file.go:344-363` → `file_service.go:467-506`）可列任意目录做侦察。

修法: 绝对路径用 `filepath.Abs` + `EvalSymlinks` 前缀判定锚定到配置根目录
（与上轮 H2 在 `file_index.go` 的 `IsPathAllowed` 同一套路）；`LocalFetcher` 拒绝根目录外的
绝对 provider path。

### H3. 任意文件写入: 5 处独立入口，同一根因（target 路径无锚定）

- `CopyFile` — `file.go:230-264` → `file_service.go:547-564`: `destPath` 接受绝对路径，
  `os.MkdirAll` + `os.WriteFile` 可写到任意位置（如 `/etc/cron.d/x`）。
- `ResumeDownload` / `MultiPeerDownload` — `p2p_download.go:42`（`req.TargetPath` 直传）→
  `p2p_resume.go:98-101`（`os.MkdirAll` + `os.OpenFile(targetPath, O_RDWR|O_CREATE)`）、
  `p2p_multipeer.go:147-150`（`os.Create(targetPath)`）。
- `SyncFromPeer` — `p2p.go:283` → `service/p2p.go:544-567`: `req.TargetDir` 任意，
  `os.MkdirAll(targetDir)` + `WriteFile`。
- `SaveLocal` — `sync.go:32` → `sync_service.go:30-74`: `local_path` 只查 `..`
  （`:32`），绝对路径经 `filepath.Abs`（`:36`）后 `MkdirAll`+`WriteFile` 到任意目录
  （如 `/root/`）；且 `:130` 用 `context.Background()` 无超时。

修法: 统一一个 `IsPathAllowed` helper，target 一律拒绝绝对路径并锚定到配置的下载根目录。

### H4. 端口转发: 任意本地端口暴露 + 密钥即认证 + 密钥泄露 — `p2p.go:1221-1317` + `forward.go:78-161`

- `CreateForwardSession`（`p2p.go:1221-1252` → `forward.go:78-99`）: `{key, port}` 完全
  攻击者可控，可暴露**本机任意端口 1-65535**。
- `ConnectForwardSession`（`p2p.go:1255-1295` → `forward.go:102-161`）: **共享 key 就是唯一认证**。
- `ListForwardSessions`（`p2p.go:1298-1317`）: **把全部会话的 key 明文返回**。

攻击链: 攻击者对本节点 `POST /p2p/forward/create {key:"k", port:6379}`，再从自己节点
`ConnectForward(target_peer=受害节点, key:"k")` → 受害节点本机 Redis/MySQL/peerserver
全部可从攻击者机器直达（配合 H1 无认证）。`forward.go:136` 监听也绑定 127.0.0.1，
等于把受害节点变成内网跳板。

修法: 挂 `AuthRequired`；key 由服务端生成且绝不列出；暴露端口走白名单。

### H5. SSRF: `register_url` 无 scheme/目标限制、无超时、无体积上限 — `file.go:95-126` → `file_service.go:173-306`

`ResolveURL`（`file_service.go:173-240`）: `client.Get(rawURL)` 用裸 `http.DefaultClient`
（**无 Timeout**），`io.ReadAll` **无体积上限**（`:198`），`followRedirects=true`，不拦
内网/回环/链路本地地址。

攻击链: `POST /files/register_url {"url":"http://169.254.169.254/latest/meta-data/..."}`
→ 响应被 hash 化存储 → `GET /download/<hash>` 读回内容 → **云元数据/内网 HTTP 服务可读**
+ 无界内存。scheme 虽被 net/http 限制为 http/https（`file://` 会报错），但不拦内网。

修法: 目标地址黑名单（私有/回环/链路本地）+ `http.Client{Timeout}` + 体积上限。

---

## 中危

### M1. `hash[:2]` 切片 panic 家族 — 4 处新实例（上轮 H1 修的是 peerjs/libp2p 路径，这几处漏了）

全部被 gin Recovery 兜底成 500（可重复 DoS），但**服务层函数在 goroutine 里则是杀进程**
（sync 路径）：

- `anon.go:84-91` `GetAnonCollection`、`:104-111` `DownloadAnonFile`、`:181` `ForkAnonCollection`、
  `:245` `CommitAnonCollection` 把裸 hash 传给 `GetCollectionByHash` → `anon_service.go:141`
  `hash[:2]`（`GET /anon/collections/a` 即 panic）。
- `sync.go:42` `SaveLocal`（请求体 `collection_hash`）与 `p2p.go:1116` `BTSeedCollection`
  → `repository.GetAnonCollectionByHash` → `anon_repo.go:67` `hash[:2]`。
- `sync_service.go:131` `saveFile` → `Download` → `LocalFetcher.Fetch` → `universal_downloader.go:76`
  `hash[:2]`（见 M2 流程：集合 entry 的 hash 可任意，如 `"x"`）。
- `p2p.go:1141/1185` `BTSeedCollection` 的 `entry.Hash[:2]`: 经 `CommitCollection` 落库的条目
  **`Hash` 字段恒为空**（`collection.go:350-355` 只序列化 Providers），`""[:2]` 必 panic。

修法: 所有 controller 入口先 `hashutil.IsValidSHA256`（或至少 64-hex）再进服务；
`BTSeedCollection` 改用 `entry.GetPrimaryHash()` 并校验。

### M2. 路由参数名错位: POST `/collections/:id/...` 全部读到空 `username` — `router.go:389-394`

路由注册的是 `:id/:collection_name/...`（`:389-394`），但 handler 读的是
`c.Param("username")`（`collection.go:190/233/323/442/495/569`）→ **恒为 `""`**。
`AddEntry` 把条目写进幽灵用户 `""` 的集合（`GetOrCreateCollection("", ...)`，
`collection_repo.go:99`），`Commit`/`Rollback`/`Visibility`/`Tags` 对真用户 404。
GET 路径经 `dispatchGetTree` 重命名参数（`collection_dispatch.go:70-77`）正常，
POST 静默全坏——单测只因为直接注入 `gin.Params{{Key:"username",...}}` 才通过。

修法: 路由参数改注册为 `:username`，或 POST 也走 dispatcher 注入（与 GET 一致）。

### M3. `BTTorrentUpload` 无体积限制 + 整块预分配 — `p2p.go:822-851`

无 `http.MaxBytesReader`（对比 `file.go:45`），`data := make([]byte, header.Size)`
直接按 multipart part 声明大小预分配（`:836-839`）——恶意传个几 GB 的 "torrent" 即 OOM；
`file.Read` 还可能短读不带 EOF，把半截 buffer 喂给 `AddTorrentBytes`。

修法: `MaxBytesReader`（几 MB）后再 `FormFile`。

### M4. 全站无请求体大小上限（系统性）— 仅 `file.go:45` 一处有 `MaxBytesReader`

所有 `ShouldBindJSON` 端点、`dispatchCreateCollection` 的 `io.ReadAll`
（`collection_dispatch.go:18-23`）、`BEP44Put` 的 base64 解码（`p2p.go:719-724`，
DHT 1000B 上限在解码**之后**）都不设限。

修法: 全局中间件统一 `MaxBytesReader`（如 16MB）。

### M5. `validateToken` 无超时的同步外呼 — `auth_middleware.go:75-78`

裸 `http.Client{}`（无 Timeout、不绑 request context）→ 每个带 `Authorization` 头的请求
都同步等注册服务器 `whoami`；注册服务器挂掉时这些请求全部阻塞/失败——可用性绑死外部依赖。

修法: `http.Client{Timeout: 2s}` + 绑请求 context + token 短 TTL 缓存。

### M6. `UniversalDownloader.lastMetrics` 数据竞争 — `universal_downloader.go:232/304/360`

`:232` 定义字段，`:304` 在 `Download` 的 defer 里写（所有下载 handler 并发调用），
`:360` `LastMetrics` 无锁读。上轮修的是传输层 race，这个在 HTTP 面共享的 downloader 上。

修法: 加 mutex（或 atomic 指针）。

### M7. `/bt/*` DHT 端点同步阻塞且无 context deadline — `p2p.go:492/517/735/776/800`

`BTAnnounce`/`BTFindProviders`/`BEP44Put`/`BEP44Get`/`BEP51Sample` 同步调 DHT，
request context 完全没传进去。部分有内部上限（FindProviders 15s、`bep44.go:45` 1000B），
但 `DiscoverInfohashes`（`bep51.go:159-220`）**无整体超时**——`wg.Wait()` 等到每个采样节点
都响应为止，慢节点 = handler goroutine 永久挂起。

修法: 每个包 `context.WithTimeout(c.Request.Context(), 30s)` 并下传 BT 服务层。

### M8. HTTP 下载面整文件驻留内存 — `download.go:63/92/241-248`、`anon.go:130-143`

`UniversalDownloader.Download` 返回完整 `[]byte`（上限 8GB）再 `c.Data` 写出；
Range 请求也要先整文件进内存（`download.go:87-92`）。并发叠加即 OOM。这是上轮 M4
（peerjs 拉取侧）在 HTTP 面的另一半暴露点。

修法: 改流式（`io.Copy`/`http.ServeContent`，边取边写）。

---

## 低危

1. **`ConnectPeer` 拨任意 multiaddr — 开放连接扫描器** — `p2p.go:167-184`:
   攻击者给任意地址（如 `/ip4/<内网>/tcp/<port>/p2p/...`）节点就去拨。
   修法: 只允许 discovery/注册服务器见过的地址，或挂认证。
2. **`Register` 重复用户返 500** — `auth.go:28-32`（应 409/400）；另
   `dispatchGetCollection`（`collection_dispatch.go:41`）把 **64 位长的用户名**误判为
   anon hash 走错分支。
3. **不可满足的 Range 返 200 整文件而非 416** — `download.go:200-215`:
   `ParseRange` 返回 `ok=false` 时（如 `bytes=99999-`、多段 Range）落到 `c.Data(200, 全量)`；
   快速预检（`:200-209`）只拦 `start-end` 形式。切片本身安全（`relay.go:385-433`
   ParseRange 已钳制 `end ≤ fileSize-1`，验证过）。
4. **CID 未转义拼进网关 URL + 网关拉取无体积上限** — `provider/ipfs.go:199`
   `fmt.Sprintf("%s/ipfs/%s", gw, cid)`（只影响受信网关的路径/查询，影响小）；
   `PinCID`/`DownloadByCID`（`p2p.go:1436-1506`、`download.go:137-184`）60s 上限内无界拉取。
5. **url provider 开放重定向** — `collection.go:300`、`anon.go:151`
   `c.Redirect(302, p.Value)`；集合是攻击者自建的自娱自乐，仅在他人信任公共集合时有害。

---

## 已确认安全 (无需重复审)

- **`GET /ping`** — 无输入。
- **`GET /sha256sum/:sha256[/:filename]`**（`DownloadBySHA256Local`，`download.go:100-132`）—
  `hashutil.IsValidSHA256` 在 `hash[:2]` 与 `os.ReadFile` 之前。
- **`GET /download/:hash`、`/sources`、`/refresh`** — 64-hex 校验；sources/refresh 有
  30s `context.WithTimeout`（`download.go:263`）。
- **Range 切片** — `ParseRange`（`relay.go:385-433`）拒 `start ≥ fileSize`、钳 `end ≤ fileSize-1`，
  `data[start:end+1]` 不可能越界（逐行验证）。
- **`GET /ipfs/:cid`** — CID 只进受信网关/DB，本地写入用服务端自算 hash（`download.go:151-162`）。
- **`GET /anon/collections[...]` 创建路径** — `AnonService.CreateCollection` 校验路径
  （`isRelativePath` 拒 `..`/绝对路径，`anon_service.go:84-95`）与 provider（64-hex 或 http(s)
  URL，`:47-72`）。（读取路径有 M1 panic。）
- **`GET /collections/:id` + `/*filepath` 分发** — 纯 hash 查表；`GetCollection`/`GetVersionLog`
  用库里存的 64-hex `current_hash`。
- **`GET /collections/search|public`、`GET /collections/:username`** — 参数化 SQL（`collection.go:533`）。
- **`GET /:username/:collection_name/*filepath`** — entry hash → `DownloadBySHA256Internal`（校验过），
  无直接 FS 访问。
- **`GET /files/verify/:hash`、`DELETE /files/:hash`** — 64-hex 校验（`file.go:179/210`）。
- **`POST /files/upload`** — `MaxBytesReader`（`file.go:45`）+ 自算 hash 落盘 + defer 删临时文件。
- **`POST /files/diff`** — JSON int + DB 只读。
- **`GET /tasks`、`/tasks/:id`** — `strconv.Atoi` 校验过（`task.go:32`）。
- **`GET /local/status/:hash`** — 仅 DB 查表（`sync_service.go:77-109`）。
- **`/p2p/*` 只读信息端点**（node/status/peers/discovered/connections/peers/detail/stats/
  topology/quality/ws/info/webrtc/info/auth/status/node/operator）— 内存态读取，无 FS/网络副作用。
- **`POST /p2p/push`** — 纯回显不落盘（`p2p.go:341-395`）。
- **`POST /p2p/request-file`** — 仅网络；无本地 FS 访问。
- **`POST /bt/bep44/get`** — 40-hex 长度校验后才解码（`p2p.go:761-774`）；DHT 数据 ≤1000B。
- **`/bt/download|seed|pause|resume|remove|stats|downloads`** — 只操作 torrent client 状态。
- **`/ipfs/pins|gateways|toggle|pin` 读路径** — DB/状态读；`checkGateway` 5s 超时（`p2p.go:1578`）。
- **`/shares`、`/s/:token`** — token→hash 查库，只重定向到公共路由。
- **`/peerjs/node`** — 只读；**`/ws/peer`** — Origin 白名单（`peerjs_routes.go:76-88`）；
  **`/peerjs/fetch`** — 远端校验 hash（H1 已修，`peerjs_service.go:713/783`）+ H6 体积上限。
- **`/ws/signal`** — SignalingHub（服务层上轮已审）。
- **Auth 端点 Register/Login/Logout/Me** — 密钥本地/AuthService 校验，无用户输入拼路径。
- **资源清理** — 上传临时文件、`rows.Close()`（`collection.go:541`、`anon_repo.go:94`）、
  multipart `file.Close()`（`file.go:54`、`p2p.go:835`）均正确 defer；forward 会话 map 与
  resume `active` map 均有锁保护（`forward.go:83`、`p2p_resume.go:83`）。

---

## 修复优先级建议

1. **H1**（认证/归属）→ **H2**（任意读）→ **H3**（任意写）——同一个 `IsPathAllowed`
   helper + 挂 `AuthRequired` 能同时堵掉前三类；M1 的 hash 校验也是每处几行的活，
   建议与 H2 一起做（对照上轮 H1 的 `isValidHash` 现成 helper）。
2. **H4**（转发劫持）→ **H5**（SSRF）——一个在暴露面上，一个在内网侦察/读云凭据。
3. 中危按暴露面排期: M2（路由参数错位，功能全坏）→ M3/M4/M8（内存）→ M5/M7（阻塞）→
   M6（race）。