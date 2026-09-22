# 传输层审阅报告 — 对照 wintools/webrtc-proxy 修复清单 (2026-08-15)

> 审阅时间: 2026-08-15
> 背景: 另一项目 wintools 的 `webrtc-proxy` 刚完成一轮 P2P 传输层加固
> (缓冲丢包、信令回调阻塞、流超时、帧校验、句柄生命周期、并发锁等 8 类问题),
> 本次用同一清单对照审阅 peerdrive 的 PeerJS/WebRTC/WS/libp2p 传输层。
> 结论: 并发 map、句柄双删、发送流控、关闭路径等已安全; 但发现 **3 个远程崩溃/
> 任意文件读取级漏洞** 和 1 个**信令永久失联**问题, 建议优先修复。
> 所有行号基于审阅当日 main 分支。

---

## 修复状态 (2026-08-16 全部完成并验证)

> 全部高危/中危/低危项已修复。验证：
> - 单元测试（含新增回归用例）：`cd back && go test -tags nosqlite -race ./internal/... ./pkg/...` ✅
> - peerjs 模块：`cd back/peerjs && go test ./... -count=1 -race` ✅
> - 集成测试（真实公共信令 + 公共 broker，串行）：`go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1` ✅ 全过
> - 构建：`go build -tags nosqlite ./...` + `go vet` ✅

| 条目 | 修复要点 |
|---|---|
| H1 | `serveFile` 切片前 `isValidHash` 校验，非法回 err 帧（`peerjs_service.go`；回归测试 `TestServeFile_InvalidHashNoPanic`） |
| H2 | `NewFileIndexService` 锚定 uploadDir 为安全根；`Create` 与 `serveFile` 均经 `IsPathAllowed`（Abs+EvalSymlinks 前缀判定）；越权 `fi.Path` 回退内容寻址存储（`file_index.go`/`peerjs_service.go`；测试 `TestFileIndex_CreateSymlinkEscape`/`IsPathAllowed`/`TestServeFile_IndexPathOutsideRoot`）。create 集成测试改用 DownloadDir 根内文件 |
| H3 | libp2p `handleChunkRequest`/`processWSRequests` 补 `isValidHash`（`p2p_transfer.go`/`p2p.go`） |
| H4 | `maxP2PListBytes`(256MB)/`maxP2PFileBytes`(8GB) 上限：`RequestCollectionList`/`requestData` 先校验再分配（`p2p.go`） |
| H5 | 二进制帧路由决策留在消息泵（廉价 bookkeeping），`WriteAt`/`Complete` 移入连接级 `uploadWorker` goroutine（`binCh` 缓冲 16，`binDone` 关闭退出）——保序且不阻塞泵（`peerjs_service.go`；测试 `TestUploadWorker_WriteThenComplete`） |
| H6 | `maxPeerFetchSize`=8GB 上限；done 校验 `len(f.got)==r.Size`（对照 done 帧 Size，非 meta.total——range 请求场景）；不满足走 errCh（测试 `TestRouteResponse_DataSizeCap/DoneSizeMismatch/DoneSizeMatch`） |
| H7 | `Signaller.Done()`：readLoop 意外退出才关闭（显式 Close 置 nil 不触发）；startLoop `case <-p.Done()` 触发整轮重连（`peerjs/peer.go`+`peerjs_service.go`） |
| M1 | `pipeBoth`：任一侧 Copy 返回即双向关闭，`forward.go` 两处替换 wg 模式 |
| M2 | 流读超时：`forward.go` 三处 + `p2p_transfer.go` `requestFileSize` 均 `SetReadDeadline(now+30s)` |
| M3 | `requestFileSize`/`DownloadFile` 对 `totalSize` 过 8GB 上限后再 `Truncate`/建 channel |
| M4 | 部分：`p2p.go handleExchange`(io.Copy) 与 `p2p_ws.go` request 路径（NextWriter+sha256 流式）已流式；peerjs 拉取侧仍整文件驻留内存，由 H6 8GB 上限兜底（改 API 会破坏 `FetchFromPeer` 调用方，留待后续）；`file_service.go:585/602` 未动 |
| M5 | `WSSession` `SetReadLimit(192KB)` + ping 30s/pong 90s 保活 heartbeatLoop（`ws_session.go`） |
| M6 | `pendingUpload.created` + 30s 过期自动清空（`file_index_verbs.go`；测试 `TestServeUploadBegin_StalePendingCleared`） |
| M7 | `UploadSession.aborted` 幂等中止；`reapUploads` 持 `sess.mu` 判 idle+置 aborted+摘句柄（与 WriteAt/Complete 串行）；`BeginUpload` 同名 size 不一致拒绝（`file_index.go`；测试 `TestFileIndex_BeginUploadSizeMismatch`/`AbortIdempotent`） |
| 低危 1-7 | 全部处理：SendFrame 30s 等待上限；`peerMu` 保护 `s.peer`；connecting 去重；broadcast/FindProviders 快照后解锁；ReplyCh 阻塞+超时；MsgError/MsgIDTaken 记日志 |

---

## 高危 (优先修, 按顺序)

### H1. 远程崩溃: `req.Hash[:2]` 切片 panic — `back/internal/service/peerjs_service.go:570`

`serveFile` 拿到请求后第一件事就是 `filepath.Join(s.storageDir, req.Hash[:2], req.Hash)`,
**没有任何 hash 校验**。对端发 `{"type":"req","hash":""}` 或 `"a"` → `[:2]` 越界 →
**panic 杀死整个进程**(serveFile 在 goroutine 里, Go panic 无法 recover)。

危险: 这是公共信令(0.peerjs.com)+ MQTT 广播 ID 的网络, 任意节点可无认证连入,
一行 JSON 就能打崩全节点。

修法: 切片前先 `isValidHash(req.Hash)`(`anon_service.go:38` 已有现成 helper),
非法就回 `err` 帧。

### H2. 远程任意文件读取: create verb 无路径限制 — `file_index_verbs.go:9-19` + `peerjs_service.go:571-573`

`serveCreate` 接受任意绝对路径注册进 file_index(只做 `os.Stat`+hashFile,
**不锚定任何目录**); 随后 `serveFile` 对命中索引的 hash 直接 `os.Open(fi.Path)` 回传。

攻击链: `create /etc/shadow` → 拿 hash → `req` → 文件内容到手。
`info` verb 还会把 Path 泄露给对端(`file_index_verbs.go:83`)。
WS 本地会话有 Origin 白名单, 但 RTC 路径对公网全开放。

修法: `Create` 限定 path 必须落在 `cfg.DownloadDir`(或配置的允许根目录)内,
符号链接解析后校验; `serveFile` 对 `fi.Path` 同样校验根目录。

### H3. 同类 bug: `hash[:2]` panic + 路径穿越 — `p2p_transfer.go:350`、`p2p.go:786→794`

`handleChunkRequest` 的 `Fscanf` 读出的 hash 未校验就切片; `handleRequest`→
`processWSRequests`(`p2p.go:794`)也完全没校验。除 panic 外, hash 形如 `../secret`
时 `Join(storageDir,"..","../secret")` 解析到存储目录外 → **libp2p 流上任意文件读取
(≤512KB/次)**。对比 `p2p.go:672/707` 两处都有 `len(hash)!=64` 校验, 唯独这两条路径漏了。

修法: 与 H1 相同, 先 `isValidHash` 再切片; libp2p 侧应优先走 `openFile` 式锚定。

### H4. 远端声明长度无上限 → OOM — `p2p.go:608`(RequestCollectionList)、`p2p.go:644`(requestData)

`data := make([]byte, size)`, `size` 直接来自对端 `OK %d` 行, **无任何上限**。
DHT 公网恶意节点回 `OK 4294967296` → 一次分配 4GB → OOM。
FetchFile 的 sha256 校验在分配之后, 救不了命。

修法: 按 `maxFileSize` 配置(或 8GB 硬上限)+ 已知文件大小双重校验后再分配;
读超过上限即断开。

### H5. 头-of-line 阻塞: 二进制帧处理路径上做同步磁盘 IO — `peerjs_service.go:375-409`

文本帧都 `go serveXxx(...)` 逃逸了, 但**二进制帧是同步执行**:
`up.sess.WriteAt()` 落盘 → 分片完成后 `Complete()` 里 `u.file.Sync()`(fsync)+
`hashFile(u.path)` **把整个文件(上限 8GB)读一遍算 sha256**——全部发生在 pion
DataChannel 的消息泵 goroutine 里(peerjs/connection.go:250-254 直接同步回调)。

危险: 一个 8GB 的上传完成瞬间, 这条连接上的所有其他帧(并发的 req 服务、
其他 fetch 的 data 块)全部冻结到 Complete 结束; 慢磁盘上大文件直接卡死整条连接
(wintools webrtc-proxy 的 io.Pipe 同类问题)。

修法: 二进制帧投递到连接级 worker 队列(按 upload 保序), WriteAt/Complete 移到
队列 goroutine; Complete 的 hashFile 更应移到单独 goroutine 或改为边收边算。

### H6. 接收侧无长度校验 + done 不校验完整性 → 静默损坏 — `peerjs_service.go:385-388, 449-471`

`routeResponse` "data" 直接把远端声明的 `r.Size` 赋给 `f.size`(:453), 无上限;
恶意对端声明 `size=1<<62` 并持续发 data 帧 → `f.got` append **无界增长 → OOM**。
反向: `done` 分支(:455-462)不检查 `len(f.got)==f.size` 就发结果,
`requestFile` 原样返回成功——对端提前 done(或只发 meta+done)→ **截断文件当作成功
返回**, 静默数据损坏。

修法: `f.size` 设上限(≤8GB, 且与 meta.total 一致才接受); done 时校验
`len(f.got)==f.size`, 不满足走 errCh。

### H7. 信令 WS 断开后永久僵尸 — `peerjs/peer.go:406-429` + `peerjs_service.go:214-221`

readLoop 出错静默退出、`connected=false`; 但 startLoop 成功 Dial 后只
`select` ctx/closed **两个永不触发的信号**, 没有任何"信令断开→重连"机制。
heartbeatLoop 的 Send 错误被 `_ =` 吞掉(:438)继续空转; connectLoop 则永远在
"peerjs: not connected" 里指数退避打转。

后果: 公网 WS 掉一次(很常见), 节点**永久失聪**直到重启——所有主动/被动连接
全部失效, 而进程还"活着"。

修法: signaller 暴露 `Done()`(readLoop 退出时关闭), startLoop 把 `<-p.Done()`
加入 select, 触发整轮重连(现有 backoff 逻辑已具备, 只差这个信号)。

---

## 中危

### M1. 双向 pipe 永久泄漏 — `forward.go:208-219`、`forward.go:270-280`

经典 `io.Copy` 双向 + `wg.Wait`: 一侧出错后另一侧仍然阻塞(对端关流但 TCP 没关,
或反之), defer close 又排在 wg.Wait 之后 → **goroutine + 连接泄漏到地老天荒**。
handleStream 和 forwardClientConn 两处都有。

修法: 任一侧 Copy 返回即同时关闭两个方向(或用 errgroup, 取消时 Close 两端)。

### M2. 流读无超时 — `forward.go:119/195/229`、`p2p_transfer.go:289`

`ReadString('\n')` 前无 SetReadDeadline(ConnectForward 校验流和 handleStream 首行
都挂死); `requestFileSize` 的 Fscanf 无 deadline, 而 309 行的 requestChunk 有——
同一文件两套写法。

修法: `stream.SetReadDeadline(now+30s)` 后再读。

### M3. 远程 SIZE 声明 → 内存/磁盘 DoS — `p2p_transfer.go:166/182/237`

`ChunksTotal` 由远端 SIZE 响应直接算出: `make(chan int, ChunksTotal)`(16GB 文件
即 6 万个 int, 恶意 `1<<62` 直接 OOM)+ `outFile.Truncate(totalSize)`(稀疏超大文件)。

修法: `totalSize` 先过上限校验(≤8GB/配置)再分配。

### M4. 整文件驻留内存 — `peerjs_service.go:385`、`p2p_ws.go:152-173`、`p2p.go:714/722`、`file_service.go:585/602`

8GB 文件 = 8GB RAM/请求, 并发叠加即 OOM。

修法: 拉取侧改成流式(临时文件 + 分块回调), 服务侧用 `io.Copy` 流式发送。

### M5. WS 本地会话无读限制/无保活 — `ws_session.go:106-119`

无 `SetReadLimit`(恶意大帧无限占内存)、无 ReadDeadline/ping-pong(浏览器标签页
死掉 → readLoop goroutine + 会话常驻, 连接 map 永不清理, pending fetch 挂 5 分钟)。

修法: `SetReadLimit(2*chunkSize+头)` + ping/pong + 定期读 deadline。

### M6. pendingUpload 无超时 → 连接级上传永久拒入 — `peerjs_service.go:49-57`

对端发 `upload` 头后不发数据块 → `st.pendingUpload` 永远占用, 之后该连接所有
upload 全部 "already in progress"。恶意对端可据此废掉连接的上传能力(连接级 DoS,
重连才恢复)。

修法: pendingUpload 带创建时间, 超时(如 30s)自动清空。

### M7. reap 与 WriteAt 竞态 / 同名会话尺寸不一致 — `file_index.go:45-60, 128-131`

reapUploads 判定 idle 后 `Abort()` 关文件, 而一个正在 WriteAt 的并发分片会拿到
已关闭句柄 → 上传莫名失败; `BeginUpload` 同名复用时不校验声明 size 一致性,
位图按旧 size 建, 续传偏移错乱。

修法: 会话加 generation/refcount, reap 只清理无人引用的; BeginUpload 尺寸不一致
时拒绝或重建。

---

## 低危

1. **SendFrame 流控等待无整体超时** — `peerjs/connection.go:114-120`: 只等 `done`,
   慢消费者时依赖 ICE disconnected(~30s)兜底。有界但不快, 可接受; 如需更严可加等待上限。
2. **`s.peer` 无锁读写** — `peerjs_service.go:145`(Close 读)vs `:173`(startLoop 写):
   关停期 data race。加个 mutex 即可。
3. **同一 peerID 可能被两个 connectLoop 并发** — `peerjs_service.go:190-197` + `:244`
   (配置 peers 与 MQTT 发现重复触发)→ 双连接浪费资源, 靠 conns map 覆盖兜底, 无实质危害。
4. **broadcast 持 RLock 做阻塞写** — `p2p_ws.go:64-73`: 慢客户端卡住 hub 的
   add/remove/count。快照后解锁再写。
5. **`FindProviders` 持 `p.mu.RLock` 做 5s 阻塞 Connect** — `p2p.go:418-427`:
   锁争用, 快照后解锁。
6. **ReplyCh `select+default` 静默丢** — `p2p.go:816-822`: 经典丢数据模式, 目前是
   死代码(无调用方设置 ReplyCh), 但留着是地雷, 复用时必踩。建议删掉或改阻塞+超时。
7. **信令 route 忽略 MsgError/MsgIDTaken** — `peerjs/peer.go:146-181`: ID 被占用
   静默无感, 两节点同 ID 时双双失联。至少记日志。

---

## 已确认安全 (无需重复审)

- **并发 map**: `conns/pending/fetches`、`peer_tracker.go`、`p2p_connection.go`
  全部 RWMutex 规范包裹, 迭代在锁内或快照, 无裸遍历。
- **句柄/双删**: `Connection.Close`/`WSSession.Close` 均 closeOnce + 幂等;
  ICE closed/failed/disconnected → Close; OnClose 里 fetches 全部唤醒
  (`peerjs_service.go:411-435`, 非阻塞 errCh, 无泄漏); Close 的 onClose 回调都在
  解锁后调用(持锁调用死锁的坑已修, REFACTOR §5 有记录)。
- **发送侧流控**: `SendFrame` 持 `sendMu` + `lowWater` 全局回调 + `done` 退出,
  等待有界、连接关闭不悬挂; `OnBufferedAmountLow` 只在 attach 注册一次
  (替换式回调的坑已避)。
- **WS 写路径**: `sendMu` + 15s 写 deadline, 双写 panic 已防。
- **pion 缓冲**: 每消息独立 slice, `append` 复制, 无别名风险。
- **上传基础校验**: size ≤8GB、offset chunk 对齐、WriteAt 越界拒绝、位图末 word
  判满(REFACTOR §5 记录)、文件名 sanitize——都已做。
- **reqId**: UUID v4 跨连接唯一(`randHex8` 碰撞问题已在注释中修正)。
- **peerjs 模块**: 与 wintools/pkg/peerjs 同为 pion 泵上的同步回调(wintools 靠上层
  channel 解耦); 文本帧已 `go` 逃逸但二进制帧没逃(即 H5)。Send 错误处理两端都正确。
- **关闭路径**: 连接关闭 → OnClose 清理 conns/pending + 唤醒全部 fetch +
  connectLoop 收到 `done` 后重连; `fetchState` 有 5 分钟超时, 无永久挂起的 waiter。

---

## 修复优先级建议

1. **H1**(崩溃)→ **H2**(任意文件读取)→ **H3**(libp2p 崩溃/穿越)——前三个都是
   几行校验就能堵上的洞, `isValidHash` 已有现成 helper。
2. **H7**(信令失联)→ **H4/H6**(OOM)→ **H5**(阻塞)——影响可用性/稳定性。
3. 中危项按暴露面排期: M2/M6 是连接级 DoS, M1 是泄漏, M5 是本地 WS 面。