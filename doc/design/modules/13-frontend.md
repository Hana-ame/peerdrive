# 模块 13：frontend Web 消费者

- **代码位置**：`front/src`（配套：`front/public/service-worker.js`、`front/tests/`、`front/package.json`、`front/vite.config.ts`）
- **功能一句话**：React SPA 消费端——经本地 WS 会话 `/ws/peer` 的 admin 帧做本节点管理面（管理本节点的文件/合集/分享/BT/IPFS/pull 任务/设置），并作为 PeerJS/WebRTC 纯消费端经公共信令按 peer id 拨号对端节点、浏览其共享内容并保存；配置与页面状态存 localStorage / 浏览器内存，业务数据全部委托所连节点后端落盘。
- **依赖**：`react@^19.2.5` / `react-dom`（`front/package.json:14-15`）、`react-router-dom@^7.14.2`（路由，`front/package.json:16`）、`peerjs@^1.5.5`（信令拨号，`front/package.json:13`）；构建用 vite（`front/vite.config.ts:5-8`），测试用 vitest + happy-dom + @testing-library（`front/package.json:19-28`）。`lib/pd-client/` 子包本身零运行时依赖——不 import peerjs，Peer 构造函数由调用方传入（`front/src/lib/pd-client/client.js:952-961`）。
- **被依赖**：无后端代码引用本模块（纯浏览器侧，服务端不 import 前端）；行为被 `front/tests/`（ws.test.js / api.test.js / api-mock-sync.test.js 等）测试，并被连接文档 01 / 12 描述为浏览器侧实现方。

## 1. 逻辑

**模块定位：一个 SPA 对两类后端面**。前端全面迁移后（2026-08-17），页面的所有后端通信只剩两个入口（`front/src/ws.js:1-7`、`front/src/api.js:1-7`）：

| 面 | 通道 | 用途 | 实现 |
|---|---|---|---|
| 管理面（本节点） | 本地 WS 会话 `/ws/peer` admin 帧 | 管理本节点：文件 / 合集 / 分享 / BT / IPFS / pull 任务 / 认证等全部管理操作 | `ws.js`（admin/upload/download/stat）+ `api.js` request() |
| 消费面（对端） | PeerJS DataChannel（公共信令） | 按 peer id 拨号对端节点：看共享清单（share 帧）、拉内容（req 帧）、保存 | `lib/pd-client/` + `lib/PeerJSConnect.jsx` |

管理面**只走本地 WS**；PeerJS/WebRTC 不实现管理 verb（防权限面漏洞的用户决策，`front/src/ws.js:30-31`；后端 `back/internal/transport/admin.go:5-11` 按 `c.ID()=="local"` 拒绝非本地会话）。

**页面清单**（`App.jsx` 路由注册，`front/src/App.jsx:59-69`；外壳+路由 2026-09-25 重做，其余页面为可用的精简版、按模块②③④⑤⑥ 逐个计划重做，见 `front/src/App.jsx:1-2,15-25`）：

| 路由 | 页面 | 说明 |
|---|---|---|
| `/` | Connect 节点搜索/连接（PeerJS 消费端） | 首页，唯一进入导航（NAV，`front/src/App.jsx:29-31`） |
| `/node` | NodeControl 对端节点控制 | 连接成功后跳转（`front/src/pages/Connect.jsx:18`） |
| `/drive` | Drive 本节点网盘 | 已实现，从导航隐藏（URL 直访仍可开，`App.jsx:27-28`） |
| `/collections` | Collections 匿名合集 | 同上 |
| `/settings` | Settings 连接方式/后端地址/认证 | 同上 |
| `/transfers` | Transfers 跨节点 pull 任务 | 同上 |
| `/bt` | BT DHT / torrent 下载 | 同上 |
| `/ipfs` | IPFS pin / gateway 面板 | 同上 |
| `*` | Placeholder「页面不存在」 | `App.jsx:68` |

**管理面核心流程**：`ws.js` 维护**单条** WS 连接（单连接复用、reqId 并发路由，`front/src/ws.js:153-154`）。`admin(method,path,body)` 组 `{"type":"admin",method,path,body,token,reqId}` 发送（`front/src/ws.js:339-350`）；后端把 admin 帧转成内部 `*http.Request` 注入 gin engine，复用全部 controller（`back/internal/transport/admin.go:13-19`）；响应 `admin-resp{status,body,reqId}` 按 reqId 配对，status≥400 → 拒绝并带 `err.status/err.data`（`front/src/ws.js:215-228`）。二进制上传 = admin 声明帧（`binary:true,filename,size,field`）+ 连续二进制块（64KB 分片，`front/src/ws.js:47,358-375`），后端收集到临时文件后构造 multipart 转发（`back/internal/transport/admin.go:21-24,47-49`）。文件拉取走 req verb：`{"type":"req",hash,offset,size,reqId}` → meta / data 头+二进制块 / done / err（`front/src/ws.js:17-20`）。连接保活：25s 心跳 `admin GET /ping` + 60s 无帧判死 + 指数退避自动重连（上限 30s，`front/src/ws.js:67-150`）。

UI 层不直接碰帧细节：`api.js` 的 `request()` 就是 `ws.admin()`（`front/src/api.js:158-160`），全部后端端点以路径字符串暴露（files / collections / anon / access / reg 代理 / bt / ipfs / p2p pull / peerjs nodes、share 等），旧 fetch 版语义保留（`front/src/api.js:2-7,155-157`）。

**消费面核心流程**：`PeerJSConnect.jsx` 组装 PeerJS 选项（本机稳定 id 恒作 `id`，`serialization:'raw'`、`reliable:true`，`front/src/lib/PeerJSConnect.jsx:58-88`）→ `connectToPeer` 建 Peer 并 `peer.connect(peerId)`（`front/src/lib/pd-client/client.js:987-1021`）→ `PeerDriveClient` 帧状态机：open 后第一帧出示 PSK（可选，`client.js:156-164`）→ `shares()` 拉共享清单（share 帧 → share-resp，`client.js:205-220`）→ `stream()/fetch()/saveAs()` 经 req 帧拉内容（`client.js:354-445`）。数据面帧协议与 Go 侧 `back/internal/transport/conn.go` **逐字对齐**（`front/src/lib/pd-client/protocol.js:1-24`）。在线节点发现走信令 REST `GET /discover/nodes`（`client.js:907-935`）。

**生命周期**：

- 应用挂载：`App` useEffect 调 `registerSW()`（`front/src/App.jsx:53`）；SW 首装未被控制时刷新一次（`front/src/lib/swBridge.js:20-27`）。
- WS 连接惰性建立：首个 admin/download/upload/stat 请求触发 `connect()`（`front/src/ws.js:156,339-360,430-489`）；断开 → 全部 pending reject + 排自动重连（`front/src/ws.js:179-191`）。
- PeerJS 会话：拨号成功 → `setNodeSession({client,peerId,myId})` 存模块级变量（跨页面共享：连接页 → 节点控制页，`front/src/lib/nodeSession.js:1-18`、`PeerJSConnect.jsx:82`）；SW 经 MessageChannel 发 `pd-fetch` 消息向页面要流，喂给 `/swdrive/` 的伪造 fetch（`front/src/lib/swBridge.js:39-63`、`front/public/service-worker.js:142-207`）。
- 页面刷新/卸载：WS 连接、nodeSession、blobUrlCache、UI 态等内存态全部丢失；localStorage 配置保留，下次启动按配置重建（见 §2）。

## 2. 如何储存

分四类（浏览器侧）与一类委托：

1. **localStorage（跨刷新/跨标签持久）**：配置、多后端列表、认证 token、消费端本机 id 等。键清单见 §4；读写点集中在 `api.js`（`front/src/api.js:9-22,124-141,419-454,612-616`）、`ws.js readToken`（`front/src/ws.js:54-61`）、`PeerJSConnect getStableMyId`（`front/src/lib/PeerJSConnect.jsx:19-31`）、`Settings.jsx`（`front/src/pages/Settings.jsx:17,34-40,62-64,150-153`）。
2. **sessionStorage（单标签会话期）**：`pd-sw-reloaded` 防重载死循环标记（`front/src/lib/swBridge.js:8,23-25`）。
3. **Cache Storage（Service Worker 持有，缓存名 `peerdrive-v1`）**：应用壳（`'./','./index.html'`，`front/public/service-worker.js:3-7`）；API 请求 network-first 时写缓存副本、静态资源 cache-first（`service-worker.js:41-75`）、网络失败回退缓存（`service-worker.js:49,78`）。旧缓存由 activate 清理（`service-worker.js:19-27`）。
4. **进程内内存态（不持久化）**：页面刷新即全部丢失，清单见 §4。
5. **委托下级模块持久化（实质储存）**：本模块自身不写任何文件/DB。用户数据全部经管理面写/读到**所连节点**的后端——上传走 admin 二进制 → `back/internal/transport/admin.go` → controller → storage / repository（内容寻址落盘与元数据，见 [03-storage.md](03-storage.md)、[02-repository.md](02-repository.md)）；跨节点 pull 任务由后端服务端任务执行、落盘到本节点 storage（[11-transport-storage.md](../connections/11-transport-storage.md) 的后端侧）。前端只是通道，不缓存内容本身。

**进程重启（页面刷新）影响**：

- WS 重连：sock 置空后由下一请求重建，或退避定时器自动重连（`front/src/ws.js:179-191,141-150`）。
- 在飞请求：onclose 时全部 pending reject（`front/src/ws.js:185`），调用方按网络错误处理。
- nodeSession 丢失 → NodeControl 显示「还没有连接任何节点」（`front/src/pages/NodeControl.jsx:243-252`）。
- 消费端本机 id 在 localStorage（`peerdrive.panel.v1.myId`）里，刷新后不变（`front/src/lib/PeerJSConnect.jsx:19-31`）；重拨是全新 DataConnection。
- 预览缓存/下载进度/UI 态（React state）全丢。

## 3. 何时储存

| 时机 | 触发点 | 内容 |
|---|---|---|
| 应用挂载 | `App` useEffect → `registerSW()`（`front/src/App.jsx:53`） | 注册 SW；首装未接管则刷新一次（`swBridge.js:20-27`）；SW install/activate 写/清 Cache Storage（`service-worker.js:10-27`） |
| 首次读后端列表 | `getBackends()` 无本地记录时写默认（`front/src/api.js:40-42`） | 写 `peerdrive_backends` |
| 首个管理请求 | `admin/upload/download/downloadStream/stat` 调 `connect()`（`front/src/ws.js:156,339-360,430-489`） | 建 WS 连接（内存态）；open 后启动心跳定时器（`ws.js:165-169`） |
| 心跳周期 | 每 25s 发 `admin GET /ping`（`front/src/ws.js:124-139`） | 纯网络探活，**不写任何储存** |
| 拨号成功 | `PeerJSConnect.connectTo`（`front/src/lib/PeerJSConnect.jsx:77-83`） | `setNodeSession({client,peerId,myId})`；组件状态置 online |
| 首次生成本机 id | `getStableMyId()` 缓存未命中（`PeerJSConnect.jsx:19-31`） | 写 `peerdrive.panel.v1` |
| 节点搜索 | 组件挂载即搜 + 手动按钮（`PeerJSConnect.jsx:120,102-117`） | 只读信令 `GET /discover/nodes`（`client.js:907-935`），不落盘 |
| 保存后端地址 | Settings `saveBase`（`front/src/pages/Settings.jsx:34-40`） | 写 `peerdrive_api_base` 并刷新页面生效 |
| 注册/登录 | Settings `doAuth`（`front/src/pages/Settings.jsx:56-72`） | reg server 验证通过 → 写 `peerdrive_auth_token`；退出删除（`Settings.jsx:74-77`） |
| URL 携带 `#token` | `setApiBase`（`front/src/api.js:131-141`） | 拆分写 `peerdrive_api_base` + `peerdrive_auth_token` |
| 预览下载完成 | `getBlobUrl`（`front/src/api.js:203-213`） | 写 `blobUrlCache`（内存 LRU）；超 50 淘汰并 revoke objectURL（`api.js:207-213`） |
| 上传/管理操作 | 页面动作发 admin 帧（如 Drive 上传/删除/分享，`front/src/pages/Drive.jsx:40-79`） | 委托后端落盘（storage/repository），前端不存文件 |
| SW 拦截网络请求 | SW fetch handler（`service-worker.js:30-79`） | API network-first 写缓存副本；静态 cache-first |
| 断开/关闭 | `handleDisconnect` / `clearNodeSession`（`PeerJSConnect.jsx:92-99`、`nodeSession.js:13-18`） | 关 client、清内存会话 |
| WS 断开 | onclose（`front/src/ws.js:179-191`） | reject 全部 pending、清 binaryExpect、排自动重连 |

说明：本模块**没有**「优雅关闭时集中落盘」——所有持久化都是动作即写（每次写 localStorage / Cache Storage 都是同步或异步即时完成）；内存态按设计随刷新消失，不在 onunload 里抢救。

## 4. 储存什么

**localStorage 键清单**（默认值与读写点）：

| key | 内容 / 默认值 | 代码 |
|---|---|---|
| `peerdrive_api_base` | 管理面后端地址；默认 `https://wsl-3000.moonchan.xyz` 或构建期 `VITE_API_BASE` | `front/src/api.js:9,18,124-126`；`ws.js:63-65` |
| `peerdrive_auth_token` | 认证 token（URL `#token` 或 reg 登录写）；默认无 | `api.js:10,131-141`；`Settings.jsx:62-64,74-77` |
| `peerdrive_auth_key` / `peerdrive_auth_header_enabled` | legacy 设置 token 与开关；默认关 | `api.js:447-449`；被 `ws.js:54-61` 读 |
| `peerdrive_backends` | 多后端 JSON 数组 `[{id,name,url}]`；默认 `[{id:'wsl',name:'WSL',url:DEFAULT_API}]` | `api.js:21,28-47` |
| `peerdrive_current_backend_id` | 当前后端 id；默认 `'wsl'` | `api.js:22,49-55` |
| `peerdrive.panel.v1` | 消费端本机 id：JSON `{"myId":"pd-<ts36>-<rand6>"}`（PeerJS 用） | `PeerJSConnect.jsx:19-31` |
| `peerdrive_reg_server_url` | 注册服务器地址；默认 `https://account.moonchan.xyz` | `api.js:612-616`；`Settings.jsx:17` |
| `peerdrive_llm_endpoint` / `_model` / `_apikey` / `_body_template` | LLM 配置；默认 endpoint `https://siliconflow.moonchan.xyz`、model `Qwen/Qwen3-8B`、body 模板含 stream/max_tokens/temperature | `api.js:419-444` |
| `peerdrive_data_consent` | 数据同意；默认未设（读 `=== 'true'`） | `api.js:423,443-444,536-538` |
| `peerdrive_follow_redirects` | 默认未设（读 `!== 'false'` 即开） | `api.js:452-454` |
| `peerdrive_ipfs_enabled` | 默认未设（读 `!== 'false'` 即开） | `api.js:469-471` |

**sessionStorage**：`pd-sw-reloaded`（`front/src/lib/swBridge.js:8,22-25`）。

**Cache Storage**：`peerdrive-v1`——`./`、`./index.html` 应用壳（`front/public/service-worker.js:3-7`）+ 网络请求缓存副本（`service-worker.js:41-75`）。

**进程内内存态（不持久化，刷新即失）**：

| 对象 | 构成 | 代码 |
|---|---|---|
| WS 连接态 | `sock`、`reqSeq`、`pending`（reqId → resolve/reject + 下载收集态）、`binaryExpect` 单槽、`status`/`statusListeners`、心跳/重连定时器与 `retryDelay`、`lastRecv` | `front/src/ws.js:38-96,154-196` |
| 节点会话 | 模块级 `session={client,peerId,myId}`（跨页面共享） | `front/src/lib/nodeSession.js:1-18` |
| 预览缓存 | `blobUrlCache`（LRU≤50，淘汰即 revoke）、`blobUrlInflight`（并发去重 Promise） | `front/src/api.js:175-218` |
| PeerDriveClient 状态 | `_pend`/`_verbs`/`_uploads`（reqId 槽）、`_expect`（连接级块归属）、`_openWaiters`、`_closeErr`、`_openState`、psk 状态（none/pending/sent/ok/err）、`stats{requests,chunks,bytes,failures}` | `front/src/lib/pd-client/client.js:128-146` |
| 页面 UI 态 | 各页面 React state（`pdStatus`/`searchStatus`/`share`/`preview`/`downloading`/`files`…） | `PeerJSConnect.jsx:40-47`、`NodeControl.jsx:52-59`、`Drive.jsx:22-26` 等 |

**委托后端储存（本模块不负责任何落盘）**：上传文件 → storage 内容寻址存储（[03-storage.md](03-storage.md)）；文件/合集/分享/权限/share_scope/pull 任务元数据 → repository SQLite（[02-repository.md](02-repository.md)）；跨节点 pull 落盘 → 后端 transport↔storage（[11-transport-storage.md](../connections/11-transport-storage.md)）。前端只发 admin/req 帧、只展示响应，不缓存内容。

## 5. 边界与坑

- **帧协议三条硬约束**（`front/src/lib/pd-client/protocol.js:16-24`）：① 文本帧 = 控制头、二进制帧 = 数据块（raw 序列化下 PPID 51/53 区分，发反了对端把控制头当数据块吞掉）；② data 头与其块必须连续（对端是连接级 expect，块里不带 reqId，两请求块交错必错位）；③ 字段名逐字对齐（`reqId` 改 `req_id` 不会报错只会静默挂到超时）。
- **`serialization` 必须是 `'raw'`**：binary 序列化下数据块被 peerjs chunker 包装，对端解析不出（`front/src/lib/pd-client/client.js:958-961`；与 Go 侧互通前提，即连接 07/12 的互通约定）。
- **peerjs 的 `id` 必须走位置参数**：`new Peer({...,id})` 会把 options.id 忽略而照旧 GET `/id`；公共静态页（file://、Pages CDN）跨域会 CORS 失败 → 报 `server-error: Could not get an ID`。`new Peer(id, opts)` 才认（`client.js:991-995`）；自带随机 id（16 位小写字母数字）顺带省掉一次跨域往返（`client.js:964-985`）。
- **`conn.peer` 是远端 id**：面板第一版把 `conn.peer` 当自己显示，结果屏幕上是对方节点名；本端 id 从自持有的 Peer 取 `localPeerId`（`client.js:170-180`）。
- **binaryExpect 是「最近二进制声明头」单槽**：一个二进制帧必属最近的 data/admin-bin 头；空声明（size=0）必须立刻清单槽，否则残留会把后续无关二进制帧误判给已完成请求（`front/src/ws.js:49-51,229-253,294-313`；回归测试 `front/tests/ws.test.js:175-190`）。
- **连接断开必须 reject 全部 pending**：否则调用方永远等不到；`downloadStream` 消费者 cancel 必须清 pending + binaryExpect，防迟到帧污染（`front/src/ws.js:179-191,467-471`；测试 `front/tests/ws.test.js:157-173,203-213`）。
- **req 帧体不带 token**（`front/src/ws.js:439` 只有 type/hash/offset/size/reqId）：数据面鉴权由连接级完成（PSK 门禁 / 共享范围，见 `client.js` 注释与 `protocol.js:65-79`、后端 share gate）；token 只出现在 admin 帧（`ws.js:345,370`，后端注入 Authorization header，`back/internal/transport/admin.go:26-27`）。
- **大文件不能整份进内存**：`download()`/`fetch()` 全量组装；`fetch` 默认 256MB 内存闸，超限抛 `TOO_LARGE`（`front/src/lib/pd-client/client.js:67-74,400-408`、`protocol.js:28-36`）；WS 侧预览路径 200MB 阈值（`front/src/api.js:176-202`）；大文件保存用 `downloadToFile`（FS Access API 流式）或 `stream()`（`front/src/ws.js:496-520`、`client.js:354-394`）。协议硬上限 8GB 与 Go 侧一致（`protocol.js:28-30`）。
- **流式块不进请求 state**：块只进有界队列（背压），多存一份等于变回整体驻留内存（`client.js:622-643`）；但 **NodeControl 的保存/图片预览路径仍把全部块收进数组**再组 Blob（`front/src/pages/NodeControl.jsx:79-87,142-162`），大文件仍有内存占用——视频/音频预览才真正走 SW 流式（`NodeControl.jsx:169`）。
- **协议里没有取消帧**：消费端提前 break 只做本地 detach（清 pending/binaryExpect），对端会把本次请求发完，后到帧查不到即丢弃；要真正中断只能关连接（`client.js:813-823`）。
- **全量请求才做内容寻址校验**：`offset===0 && size<0` 时流式增量 sha256 校验，不匹配抛 `HASH_MISMATCH`；range 片段不等于整份摘要故不校验（`client.js:746-756,660-671`）；`done` 声明字节不一致抛 `INCOMPLETE`（`client.js:645-659`）。增量 SHA-256 是纯 JS 实现（WebCrypto 一次性、与流式冲突，`front/src/lib/pd-client/sha256.js:3-13`）。
- **`meta` 帧双义**：既可能是拉取已就绪、也可能是上传分片授权——按 reqId 归属分流，看错一边会让上传等到超时或拉取收到未知帧（`client.js:524-539`）；上传 offset 以服务端 meta 给出的为准（断点续写），自顾自增会写错位置（`client.js:298-313`）。
- **一次只允许一个上传**（协议按片授权，`client.js:222-231`）；`UPLOAD_CHUNK` 必须与 Go 侧 uploadChunkSize(64KB) 一致，否则被判非法偏移（`protocol.js:85-94`）。
- **err 帧的 reqId 可能属于拉取/verb/上传三种槽**：全查否则 `put()` 挂到超时掩盖真因（`client.js:675-691`）；带等待 reqId 的未知帧必须报 PROTOCOL 错误而不是忽略（两侧协议版本不一致，`client.js:557-566`）。
- **PSK 门禁**：密钥必须是连接第一帧、明文传（DataChannel 强制 DTLS，防的是陌生人不是窃听）；配了才发、不等回执（`protocol.js:65-79`、`client.js:131-137,156-164`）。`PSK_REQUIRED` 单独成错误码，UI 按 code 分支不匹配文案（`client.js:52-56,675-691`）。
- **SW 首装需刷新一次**：controller 为空时 `/swdrive` 请求 404，`registerSW` 以 sessionStorage 标记防死循环刷新一次（`front/src/lib/swBridge.js:19-27`）；媒体加载失败有内存 blob 兜底（`front/src/pages/NodeControl.jsx:181-198`）。
- **`/swdrive` 只发给第一个 client**：SW `clients[0].postMessage(pd-fetch)`（`front/public/service-worker.js:189-193`），依赖当前持有 PeerJS 连接的页面；未连接时回 `{'no active peer connection'}`（`swBridge.js:47-50`）。
- **discoverNodes 依赖信令 CORS**：`GET /discover/nodes` 是跨域请求，信令没放 `Access-Control-Allow-Origin` 会在 fetch 层失败且拿不到状态码；客户端把 TypeError 点破为 CORS 提示并指向信令升级（`client.js:898-935`）。
- **管理面通信中，reg server 相关是极少数直接 fetch 的例外**：认证注册/登录（`front/src/pages/Settings.jsx:42-72`）、分组/评论 API（`front/src/api.js:577-610`）走 `fetch` 而非本地 WS——reg 是独立第三方服务、不经所连节点路由；需其开放 CORS（`Settings.jsx:181`）。消费面另有一处直连 fetch：`discoverNodes` 打信令的 `GET /discover/nodes`（`client.js:918`）。
- **合集权限是内容寻址快照**：改可见性写新 JSON → 返回新 hash，旧 hash 仍是旧权限快照，调用方必须拿新 hash 当集合身份（`front/src/api.js:261-264`）；`access_list` 是账号名数组不是 hash（`api.js:243-245`）；匿名分派按 body 有无 username 分流、匿名创建字段是 `friendly_name`（`api.js:543-547`）。
- **SW 的 `/api/upload` 背景同步已休眠**：前端迁移 WS 上传后不再产生 `/api/upload` 缓存请求，`sync-uploads` handler 是 legacy 路径（`front/public/service-worker.js:82-107`、`front/src/api.js:1-7`）。
- **NodeControl 本机身份显示的兜底失效**：`session.client.id` 恒为 undefined（`PeerDriveClient` 无 `id` 字段、只有 `peerId`），显示靠前面的 `session.myId` 兜住（`front/src/pages/NodeControl.jsx:274`）。
- **测试纪律**：`tests/setup.js` 全量 automock `api.js`（防组件测试发真请求，`front/tests/setup.js:7`）；`api.test.js` 用 `doUnmock` 解除测真实实现（`front/tests/api.test.js:10`）；`ws.js` 的 `__test` 钩子与 `_wsOwned` 标记区分真实连接与测试注入 mock（`front/src/ws.js:80-82,524-544`）。
- **常量不与 api.js 同放**：非函数导出被 automock 替换会丢（`front/src/constants.js:3-8`），可见性常量放 `constants.js` 再 re-export（`front/src/api.js:258`）。

## 6. 对外连接

- [frontend ↔ backend 本地 WS 会话](../connections/01-frontend-backend.md)：本模块管理面的唯一通道——`ws.js` 连 `/ws/peer`（后端 `back/internal/router/peerjs_routes.go:155-182` 建 WSSession("local")）发 admin/req/upload 帧，admin 帧内部转发 gin engine；Origin 白名单、token 注入、binary 单槽与 64KB 分片等约束都在这一对。
- [router ↔ controller](../connections/02-router-controller.md)：admin 帧经 `back/internal/transport/admin.go:13-19` 构造内部请求注入 gin engine 后走到的分发链——前端全部管理端点的后端目的地（HTTP 路径语义复用，Error.status/Error.data 与 fetch 版一致）。
- [frontend ↔ signalserver](../connections/12-frontend-signalserver.md)：消费端经公共信令按 peer id 拨号（PeerJS OFFER/ANSWER/CANDIDATE 协商）+ `GET /discover/nodes` 在线发现（`front/src/lib/pd-client/client.js:907-1021`、`front/src/lib/PeerJSConnect.jsx:11-17`）。
- [controller ↔ storage](../connections/10-controller-storage.md)：上传（admin 二进制 → `/files/upload`）等写盘操作的实质落盘处。
- [service ↔ repository](../connections/04-service-repository.md)：文件/合集/分享/权限/pull 任务元数据的实质持久化处。
- [transport ↔ storage](../connections/11-transport-storage.md)：跨节点 pull 任务由后端执行并落盘到本节点 storage / file_index（前端只发 `/p2p/pull*` 管理请求与轮询任务列表，`front/src/api.js:364-370`、`front/src/pages/Transfers.jsx`）。

> 备注：管理面与数据面的分离是本模块的核心不变式——本地 WS 会话承载 admin（含认证 token）与 req 数据面；PeerJS/WebRTC 只承载对端 share/req 数据面，不实现管理 verb（`front/src/ws.js:30-31`、`back/internal/transport/admin.go:5-11`）。