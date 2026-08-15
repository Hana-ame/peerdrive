# 传输层审阅报告 第二期 — controller HTTP / service 剩余 / repository / router / peerjs (2026-08-16)

> 审阅时间: 2026-08-16
> 背景: 第一轮 (`doc/TRANSPORT-REVIEW-2026-08-15.md`) 已覆盖 peerjs/WS/libp2p 服务层并全部修复。
> 本轮扫未审阅面: controller HTTP 层 + service 剩余部分 + repository/router/peerjs 模块,
> 同一清单 (远程输入 panic / 路径穿越 / 无界内存 / 热路径阻塞 / 无超时 / 泄漏 / 竞态 / 认证绕过)。
> 结论: 发现 **2 个致命级 (任意文件读/写, 认证形同虚设)** + **1 个远程进程崩溃 panic** 等,
> 这些比第一轮的帧协议问题更靠前, 应**优先修复**。

---

## 致命级 (任意文件读写 / 认证)

### F1. 认证形同虚设: `AuthRequired` 从未挂载 — 全站匿名可达

- `back/internal/router/auth_middleware.go:48-69` 定义了 `AuthRequired()`, 但 **全仓 0 调用点**
  (`rg -n "AuthRequired\("` 除定义外无结果)。router 只挂 `AuthOptional()` (且仅当配置了
  RegistrationServer), 它只往 context 塞一个 flag, 唯一读取处是 `/p2p/auth/status` 信息端点。
- 后果: `POST /files/register_local`、`POST /files/register_folder`、`DELETE /files/:hash`、
  `GET /files/browse`、`POST /collections`、`/shares`、`/local/save`、WebDAV 全部对公网开放。
- 修法: 把 `AuthRequired()` 挂到所有 mutating/admin 路由组; 或按 REFACTOR §6 删除整个 legacy `/files` 块。

### F2. 匿名任意文件读: `register_local` 接受任意绝对路径 → `LocalFetcher` 回读

- 链: `POST /files/register_local {"path":"/etc/shadow"}` (`controller/file.go:129`, 无认证)
  → `FileService.RegisterLocal` (`service/file_service.go:52-115`) 对绝对路径**原样保留**
  (`if !filepath.IsAbs(path)` 分支, 不锚定任何根目录), 插入 `local` provider
  → `GET /download/<hash>` → `UniversalDownloader` → `LocalFetcher.Fetch`
  (`service/universal_downloader.go:73-104`) → `os.ReadFile(p.Path)` → 文件字节到手。
- 同样可读: `GET /files/browse?path=/` (`file_service.go:467-505`) 任意目录列举;
  `DELETE /files/:hash` (`file_service.go:454-459`) 对 `local` provider 逐个 `os.Remove` → 任意文件删。
- 修法: `RegisterLocal`/`RegisterFolder` 校验 `Abs + EvalSymlinks` 落在配置的 allow-root 内
  (复用 `file_index.go` 的 `IsPathAllowed` 模式), 拒绝绝对路径/`..`。

### F3. 匿名任意文件写: `POST /files/copy` 目标路径不受控

- `service/file_service.go:546-575` `CopyFile`: `destPath` 若为绝对路径**原样采用**,
  否则 `filepath.Join(s.storageDir, destPath)` 可用 `../` 逃逸 → `os.MkdirAll(dir) + os.WriteFile(absDest, body, 0644)`,
  body 是攻击者先经公开 `/files/upload` 上传的文件内容。可写 `/etc/cron.d/x`、`/root/.ssh/authorized_keys` → RCE 边缘。
- 修法: 写盘前对 `absDest` 做根目录校验 (Abs + EvalSymlinks prefix), 并改用 `io.Copy` 流式写。

---

## 高危 (远程崩溃 / OOM)

### H1. `hash[:2]` 远程 panic — anon 集合路径未校验 — `service/anon_service.go:110,121,141,253` + `repository/anon_repo.go:67,107`

- `GetCollectionByHash`/`GetAnonCollection`/`DownloadAnonFile`/`ForkAnonCollection`/`sync` 直接
  `filepath.Join(storageDir, hash[:2], hash)`, **无长度/格式校验** (`isValidHash` 已在同文件 38 行但未调用)。
- 触发: `GET /anon/collections/a` (route `router.go:364` 任意字符串)、`POST /anon/collections/fork` 短 `source_hash`、
  `GET /anon/collections/a/x`、远端 P2P sync body — hash 长度 0/1 → 切片越界 **panic 杀进程**
  (gin Recovery 只救 HTTP, P2P 路径直接崩)。hash 为 `".."` 时 `Join(storage, "..", hash)` 逃逸 storage 目录。
- 修法: `GetCollectionByHash` 入口调 `isValidHash(hash)`, 非 `^[a-f0-9]{64}$` 直接返回 not-found 错误。

### H2. Bitswap 远端声明的 varint 长度无上限 → OOM — `service/ipfs_compat.go:560-577`

- `readVarintPrefixed` 对任何 libp2p 远端声明的 `length` 直接 `make([]byte, length)`。
  `handleBitswap` (注册于 118-120) 在每条 Bitswap 流上调用; 对端发 varint `1<<40` → 数 GB 分配 → OOM 杀进程
  (libp2p 流 handler 在 goroutine 里无 recover)。上一轮 H4 修了 p2p.go 的 legacy 路径, 漏了这个。
- 修法: 声明长度上限 (≤2MB, boxo 默认 MaxBlockSize), 超限直接拒绝。

### H3. 信令服务器离线队列无界 → OOM — `internal/signalserver/signalserver.go:165`

- `route` 把发给离线 `dst` 的每条非 LEAVE/EXPIRE 消息 append 进 `s.queues[m.Dst]`, 无任何上限;
  过期 (30s TTL) 只在 `flushQueue` (dst 连接时) 才清。dst 永不连接 → 队列无限增长。
- 修法: 每 dst 队列上限 (丢最旧/拒绝), 加周期性 sweeper 清过期项。

### H4. `/peerjs/fetch` 无认证 + 整文件驻留内存 + 8GB 上限仍危险 — `router/peerjs_routes.go:50-70` + `service/peerjs_service.go:636-671`

- 任何人可传 `peer/hash/offset/size`; `FetchFromPeer` 把整个响应 buffer 进 `[]byte` 再 `c.Data` 发。
  H6 的 8GB cap 只防超限, 不防慢读客户端长时间持有 ~8GB RAM (slow-loris)。
- 修法: 经 pipe/`io.Copy` 流式转发, 或 HTTP 路由单独设更小上限 + 认证。

### H5. `FetchFromPeer`/`requestFile` 不校验返回内容 hash — `service/peerjs_service.go:636-671`

- `requestFile` 对 `done` 帧直接返回 `f.got`, 不校验 `sha256(got) == hash` (对比 `UniversalDownloader.Download`
  `universal_downloader.go:325-336` 有校验)。恶意/被攻破对端对任何 hash 回任意字节即被当作内容寻址文件。
- 修法: 返回前 `sha256(data) == hash` 校验 (H6 只查字节数)。

### H6. `list`/`sync` verb 的 LIMIT 直通 SQL — `service/file_index_verbs.go:77-87,112-121` + `repository/file_index_repo.go:92-108`

- `serveList` 把对端 `size` 直接当 SQL LIMIT, `size=2000000000` → 全表物化; `ListFileIndexSince` 无 LIMIT。
  WS/DataChannel 匿名可达 → 内存 DoS。
- 修法: 服务端 clamp (`min(size, 1000)`), `ListFileIndexSince` 也加 LIMIT。

---

## 中危 (竞态 / 无超时 / 泄漏)

- **M1 数据竞争** `p2p_resume.go:347-363`: `downloadChunks` 多 goroutine 无锁改共享 `*DownloadProgress`。
- **M2 信令 hub 锁内阻塞写 + 无读限制 + 无保活** `signaling.go:21-23,119-123,325-353`: `broadcast` 持 `RLock` 调
  `WriteJSON` 无写 deadline; `HandleConnection` 无 `SetReadLimit`/ping-pong; `upgrader.CheckOrigin` 恒 true。
- **M3 relay 远端声明 fileSize 无上限** `relay.go:152,197-219`: 恶意对端 `1<<60` → 假 Content-Length。
- **M4 BT chunk 无界 ReadAll** `p2p_multipeer.go:389` / `p2p_resume.go:433`。
- **M5 UniversalDownloader 无超时 + 无界读 + `lastMetrics` 竞态** `universal_downloader.go:179-192,303-305`。
- **M6 信令 token 形同虚设** `signalserver.go:91-128`: `HandleWS` 只查 key, 客户端 token 不校验 → ID 冒名/队列窃取。
- **M7 peerjs `readLoop` 无读限制** `back/peerjs/peer.go:428-458`: 云端信令转发的超大帧无 `SetReadLimit`。
- **M8 `validateToken` 无超时无 body cap** `router/auth_middleware.go:71-93`: `http.Client{}` 无 timeout。
- **M9 `Connection` handler 字段无锁写入 vs pion pump 读** `back/peerjs/connection.go:52-58,242-267`。
- **M10 `nextFileIndexSeq` RMW 非原子** `repository/file_index_repo.go:39-44`: `SELECT MAX+1` 与 `INSERT` 分离 → 并发下 seq 重复, sync 游标错乱。
- **M11 列表查询无 LIMIT** `anon_repo.go:87`(每行还 os.ReadFile) `collection_repo.go:117,145,218,269,287` `file_repo.go:96` `sync_repo.go:66` `pin_repo.go:36`。
- **M12 WebDAV 无认证写/删** `webdav.go:43-49` + `router.go:450-453` (handler 本身防穿越, 缺权限模型)。
- **M13 上传竞态** `p2p_resume.go` 与 `service` 共享进度; **M14 IPFS blockstore 整文件读内存** `ipfs_service.go:255-265`。
- **M15 discovery 输入可伪造 + 无界 decode** `signalserver.go:209-235` / `mqtt_discovery.go:101-108` / `http_discovery.go:90-121`。

---

## 低危

- **L1 bcrypt 72 字节截断 + 无限流** `auth_service.go:28,64`。
- **L2 `sync_service.go:120` prefix 无路径边界** (`/tmp/foo` 匹配 `/tmp/foobar`); L3 `p2p_connection.go:46-52` 重连无退避。
- **L4 `signaling.go:275-302` 重复 peerID 注册时旧连接清理误删新连接**。
- **L5 `node_registrar.go:85-93`/`relay_registry.go:61-67` 心跳 goroutine 无 Stop**。
- **L6 `delete` verb 响应缺 `seq`** `file_index_verbs.go:102-108` (REFACTOR §4 文档要求 `deleted{hash,seq}`)。
- **L7 `share_repo.go:42-52` 扫描 `expires_at` 进 `*any` → `ExpiresAt` 永为空**。
- **L8 CORS 白名单形同虚设** `router.go:65-84`: 非白名单 Origin 仍发 `Allow-Origin: *` + `Allow-Credentials: true`。
- **L9 `db.go:144-150` ALTER 错误静默忽略**。

---

## 已确认安全

- SQL 注入: 全仓参数化, 唯一拼接的 `ORDER BY` 列名来自 5 值白名单 switch (`file_repo.go:118`)。
- peerjs 模块并发 (`peer.go`/`connection.go`/`transport.go`): conns map 有锁, sendMu + 低水位回调, 30s 流控。
- 帧协议字段 (`reqId/hash/offset/size/total/files/lastSeq/path/name/seq`) 与 REFACTOR §4 一致。
- `IsPathAllowed` (Abs + EvalSymlinks) 正确门禁 `create` verb 与 serveFile 索引回退。
- 第一轮 H1/H6 修复 intact (`isValidHash` 前置 + fetch 尺寸/完整性校验)。
- 上传路径 (file_index.go): 8GB cap / WriteAt 越界拒绝 / chunk 对齐 / reapUploads。
- 下载端点: `/download/:hash`、`/sha256sum/:sha256` 均先 `IsValidSHA256` 再切片; `UniversalDownloader.Download` 校验 sha256。
- `nodestate.go` 全字段锁保护; `config.go` 环境变量解析安全; `p2p_key.go` 0700/0600 + ed25519。
- `peer_scanner.go` 4 个扫描器均 select stopCh 干净退出; `http/mqtt_discovery` ticker 均 Stop。

---

## 修复优先级

1. **F1+F2+F3** (认证 + 任意文件读写) — 一组根因, 共用 `IsPathAllowed` 模式。
2. **H1** (`hash[:2]` panic) — 复用 `isValidHash`, 覆盖所有 anon/p2p sync 入口。
3. **H2** (Bitswap varint OOM)、**H3** (信令队列无界)、**H6** (SQL LIMIT)。
4. **M 类**竞态/超时/泄漏按 M1 → M15 顺序。