# WS 客户端模块（front/src/ws.js）

> 层归属：AOP ⑧ 前端切面（doc/LAYERS.md §1）。浏览器与本地节点 `/ws/peer` 会话之间
> 的帧协议客户端——管理面 admin verb、二进制上传、req verb 文件拉取都在这一条 WS
> 连接上完成；`api.js` 的 `request()` 全部经它。协议权威定义见 REFACTOR.md §3.10/§4
> 与 NODE-API.md §2.4，本文只写前端侧实现与坑。

## 职责

- **管理面**：`admin(method, path, body)`（ws.js:211）→ 后端 admin verb 内部转发
  gin engine → `admin-resp`（JSON）或 `admin-bin`（二进制文件流，如集合文件/.torrent）。
  覆盖集合/认证/BT/IPFS/任务/文件管理等全部 HTTP 语义端点。
- **二进制上传**：`upload(file, fileName, field, path)`（ws.js:227）→ admin 声明帧 +
  连续二进制块（文件分片上传、BT torrent 上传），后端收齐后 multipart 重包转发。
- **数据面拉取**：`download(hash, offset, size)`（ws.js:295）→ `req` verb，与
  WebRTC DataChannel 同一套帧协议（64KB 块 + data 头 + done 收尾）。
- **连接生命周期**：单连接复用、断线 reject 全部 pending 并置空等重连、token 注入。
- **下载落盘**：`downloadToFile(hash, filename)`（ws.js:309）→ Blob + `<a download>` 模拟保存。

**为什么存在**：帧协议（REFACTOR.md §4）只覆盖文件数据面（req 拉取 + create/upload/
list/info/delete/sync 索引 + fwd-* 转发），不覆盖集合/认证/BT/IPFS/任务等管理面。后端
在本地 WS 会话上加 admin verb（`back/internal/transport/admin.go`），内部转发 gin
engine 复用全部 HTTP controller（零重复实现）——浏览器经此通道完成全部管理操作，
**不再直接 fetch HTTP**（HTTP 路由保留作 legacy，见 router.go LEGACY 注释区）。

## 关键机制

### 连接生命周期

**wsUrl 转换**（ws.js:34）：`http://` → `ws://`、`https://` → `wss://`，与 api.js
`getApiBase()` 同源。`getWsBase()`（ws.js:56）读 `localStorage['peerdrive_api_base']`，
缺省 `https://wsl-3000.moonchan.xyz`。

**幂等 connect**（ws.js:62）：

```js
function connect() {
  if (!sock) sock = new WebSocket(wsUrl(getWsBase()) + '/ws/peer')
  if (sock._wsHandlers) return      // handlers 幂等挂载
  sock._wsHandlers = true
  ...
}
```

- 已有 `sock` 直接复用；handlers 以 `sock._wsHandlers` 标记幂等挂载（测试注入 mock
  socket 也能走同一初始化路径）。
- **单连接复用，无连接池**：浏览器与本地节点只有一条会话，所有请求并发经 reqId 路由。

**断线处理**（ws.js:77）：`onclose` → reject 全部 pending（`Error('ws: connection
closed')`）、清空 pending、`binaryExpect = null`、`sock = null`；下一请求前自动重连。
`onerror` → 主动 `close()` 走同一清理路径。

**为什么**：连接断开时挂起的请求永远不会收到响应帧，必须主动 reject 让调用方按网络
错误处理；sock 置空保证重连不会复用僵尸连接。

### reqId 路由（pending Map）

`nextReqId()`（ws.js:89）= `'w' + Date.now().toString(36) + '-' + seq.toString(36)`；
`pending` Map 存放 resolve/reject（download 型还有 `chunks`/`total` 收集态）。响应帧
回显 reqId 配对，**乱序安全**（单测覆盖：先回第二个请求）。

### 文本帧分发（handleText，ws.js:96）

JSON.parse 后按 `msg.type` 分发（解析失败/无 type 直接丢弃）：

| type | 行为 |
|---|---|
| `admin-resp` | 按 reqId 取 pending；`status>=400` → `reject(Error(body.error\|body.message \|\| 'HTTP '+status))`，错误挂 `err.status`/`err.data`（409 冲突清单等结构化体可用）；否则 `resolve(msg.body)` |
| `admin-bin` | 声明「下一二进制帧归本次管理下载」：`binaryExpect = {type:'admin', reqId, size, got:0, chunks:[]}`；size==0 立即 finish |
| `data` | 下载数据块头：仅 `p.kind==='download'` 才接管；`binaryExpect = {type:'download',...}`；size==0 直接清期待（空块罕见） |
| `meta` / `done` | 仅 download 型 pending；`done` → 删除 pending、清 binaryExpect、`resolve(assemble(p))` |
| `err` | 按 reqId reject（`msg.msg` 或 'peer fetch failed'） |

### binaryExpect 单槽（ws.js:44、169）

「最近二进制声明头」单槽：一个二进制帧必属于最近的 admin-bin 或 data 头。

**为什么是单槽（协议正确性依赖）**：后端 `SendFrame` 保证 data/admin-bin 头与二进制
块**原子连续**（sendMu，REFACTOR.md §4 约束 2）——同一时刻最多只有一个「声明头待其
块」的窗口，前端单槽与后端连接级 expect 状态机语义一致。若改成多槽（按 reqId 缓冲
块）会破坏「块归属最近声明头」的隐含序，且协议上无法区分「块属于谁」。

handleBinary（ws.js:169）：
- 无 `binaryExpect` → 丢弃（脏块）。
- 累计 `got`，`got >= size` 时：
  - admin 型 → `finishBinaryExpect`（组装 Uint8Array 并 resolve）
  - download 型 → 块并入 `p.chunks`，**不 resolve**——完整性由 done 帧保证
    （data 头可多次出现，每块收齐后等下一个 data 头或 done 帧）

### token 注入（readToken，ws.js:47）

`peerdrive_auth_token`（URL fragment `#token` 导入，见 api.js `setApiBase`）优先；
否则 `peerdrive_auth_header_enabled==='true'`（设置页开关）时用
`peerdrive_auth_key`；否则空串。admin 请求帧带 `token` 字段，后端转发时注入
`Authorization: Bearer`（与 HTTP 行为一致，NODE-API.md §2.4）。

### admin verb 帧格式实例

普通 JSON 请求（与后端约定，ws.js:10-16）：

```jsonc
// 浏览器 → 后端（发送，ws.js:217）
{"type":"admin","method":"GET","path":"/files?sort=time","body":null,
 "token":"<可选>","reqId":"w-m4f3a-1"}

// 后端 → 浏览器（admin-resp，body 为 controller 原始 JSON）
{"type":"admin-resp","status":200,"body":{"files":[...]},"reqId":"w-m4f3a-1"}

// 4xx/5xx 也走 admin-resp：body 为结构化错误体（409 含 conflicts 清单）
{"type":"admin-resp","status":409,
 "body":{"error":"merge conflict","conflicts":[{"path":"a.txt","local_hash":"...","source_hash":"..."}]},
 "reqId":"w-m4f3a-1"}
```

二进制响应（文件流，如集合文件/`.torrent`，≤64MB `adminBinMax`）：

```jsonc
{"type":"admin-bin","status":200,"size":N,"reqId":"w-m4f3a-1"}
<紧随的 N 字节二进制帧（单块，后端 SendFrame 原子连续）>
```

### 二进制上传帧序列（upload/pumpBinary，ws.js:227）

`upload(file, fileName, field='file', path='/files/upload')` 的完整帧序列：

```
浏览器 → 后端：
  1. admin 声明帧（文本，同步发出）：
     {"type":"admin","method":"POST","path":"/files/upload","binary":true,
      "filename":"a.bin","field":"file","size":N,"token":"<可选>","reqId":"w-m4f3a-2"}
  2. 连续二进制块（≤64KB 分片，Streams API 逐块读出即发）：
     [chunk1][chunk2]...[chunkN]   ← 声明与块之间不允许插入任何其他帧
后端 → 浏览器（收齐后 multipart 重包转发 controller）：
  3. {"type":"admin-resp","status":201,"body":{"hash":"<64hex>",...},"reqId":"w-m4f3a-2"}
```

**为什么声明帧先同步发出**：后端消息泵内同步占槽 `st.adminUp`（NODE-API.md §2.4），
声明与块之间不允许插入其他帧，否则会打断 multipart 收集（e2e smoke 里同样注释
「声明帧不能 await」——响应要等二进制块收齐才回）。

pumpBinary（ws.js:244）：
- **Streams API 优先**：`file.stream().getReader()` 逐块 `reader.read()` →
  `sock.send(value)`。大文件零拷贝、背压友好，不整读进内存。
- **FileReader 回退**（无 stream 的文件对象）：`BIN_CHUNK` 分片 `file.slice(off, off+CH)`
  → `readAsArrayBuffer` → 逐块发送。
- 中途断开：`send` 抛 `Error('ws: closed during upload')` → `fail()` 删 pending 并 reject。
- `field`/`path` 可换：BT torrent 上传用 `field='torrent'` + `path='/bt/torrent'`。

### 下载帧序列（download，ws.js:295）

`download(hash, offset=0, size=-1)` 发送 `{type:'req', hash, offset, size, reqId}`，
pending 记录 `kind:'download'`；服务端按 64KB 块回（与 WebRTC DataChannel 同一套）：

```
浏览器 → 后端：
  {"type":"req","hash":"<64hex>","offset":0,"size":-1,"reqId":"w-m4f3a-3"}
后端 → 浏览器：
  {"type":"meta","hash","total","reqId":"w-m4f3a-3"}
  {"type":"data","offset":0,"size":65536,"reqId":"w-m4f3a-3"} + 64KB 二进制块
  {"type":"data","offset":65536,"size":65536,"reqId":"w-m4f3a-3"} + 64KB 二进制块
  ...（多块）
  {"type":"done","hash","offset","size","reqId":"w-m4f3a-3"}   ← 完整性信号，触发 resolve
  或 {"type":"err","msg":"file not found","reqId":"w-m4f3a-3"}
```

- 前端按 data 头声明 size 收集（handleBinary），**done 帧才 resolve** 为 Uint8Array。
- `downloadToFile(hash, filename)`：download → Blob → `<a download>` 模拟点击 →
  5s 后 `revokeObjectURL`（延迟 revoke 防下载中断）。

### __test 测试钩子（ws.js:324）

生产不导出，仅 vitest 单测使用：
- `connect` / `readToken` / `getWsBase` / `handleText` / `handleBinary` / `pending`
- `_setSock(s)`：注入 mock socket（`_wsHandlers:false` 时 connect 幂等挂 handlers）
- `_reset()`：sock 置空 + 清 pending + 清 binaryExpect

## 与其它模块的关系

```
页面组件（pages/*）
   │  import * as api
   ▼
api.js ──request()──▶ ws.admin()         管理面 JSON/二进制
api.js ──downloadFile──▶ ws.download()   数据面 req verb
api.js ──getBlobUrl──▶ ws.download()     预览 objectURL
   │
   ▼
ws.js ──WebSocket──▶ back/internal/transport/ws_session.go（/ws/peer，WSSession）
                        └─ serveAdmin（仅 ID()=="local" 会话接受）
                              └─ 构造 *http.Request → gin engine ServeHTTP
                                    └─ controller（业务层无感知）
```

- **上层（api.js）**：`request()` 即 `ws.admin()`（api.js:148-150）；`downloadFile`、
  `getBlobUrl`、`downloadFileToDisk` 走 `ws.download`/`ws.downloadToFile`；
  `uploadFile`/`btTorrentUpload` 走 `ws.upload`；`downloadAnonFile`/`downloadUserFile`/
  `downloadTorrentFile` 走 `ws.admin('GET', ...)`（二进制响应 → admin-bin）。
- **后端协议面**：`admin.go`（admin verb 处理 + `adminUp` 上传单槽 + multipart 重包 +
  `adminBinMax` 64MB 上限）、`ws_session.go`（WSSession，帧协议与 DataChannel 一致）、
  `router.go` `SetAdminHandler` 装配。协议权威定义：REFACTOR.md §3.10/§4、NODE-API.md §2.4。
- **非关系（重要）**：peerjs/WebRTC 连接**不实现管理 verb**（防权限面暴露给公共信令
  上的未知节点，用户决策）；前端也没有 peerjs/mqtt 依赖——peerjs 栈只存在于后端，
  旧前端 P2P 客户端代码已删（LEGACY.md F 节）。

## 坑与设计决策

1. **data/admin-bin 头与二进制块必须原子连续**（后端 SendFrame 保证）——前端单槽
   binaryExpect 与之配套，勿改成多槽（ws.js:23, 42）。
2. **admin 响应 status>=400 的语义**：reject 的 Error 带 `err.status`/`err.data`，
   与 api.js 旧 fetch 版一致，409 冲突清单等结构化错误体依赖它（ws.js:26；
   Explorer 合并冲突弹窗直接消费 `e.data.conflicts`）。
3. **连接断开必须 reject 全部 pending**：否则挂起 Promise 永不 settle，调用方无法
   区分「慢」与「死」（ws.js:78）。
4. **管理面只走本地 WS**：peerjs/WebRTC 不实现管理 verb（防权限面漏洞；后端
   serveAdmin 按会话 ID 拒绝非本地连接，ws.js:30）。
5. **上传声明帧必须与二进制块连续发送**：中间插入任何帧都会打乱后端 multipart
   收集；且**不能 await 声明帧的响应再发块**——响应要等块收齐才回（ws.js:227、
   e2e-admin-smoke.mjs 注释）。
6. **下载 resolve 时机**：块收齐不清期待不等于完成，必须等 done 帧——data 头可
   多次出现，done 才是完整性信号（ws.js:168）。
7. **⚠️ BIN_CHUNK 未定义（潜在 bug）**：pumpBinary 的 FileReader 回退分支（ws.js:272）
   引用未定义的常量 `BIN_CHUNK`——`File` 对象带 `.stream()` 时走 Streams API 不会
   触发；一旦遇到无 stream 的文件对象（如 mock/老环境）会抛 ReferenceError 且
   pending 永不 settle。修复方式：定义 `const BIN_CHUNK = 64 * 1024` 或直接删回退
   分支。**发现背景**：写本文档核对代码时 grep 全 front/src 仅一处引用、无定义。
8. **token 双来源**：URL fragment token（`peerdrive_auth_token`）恒生效；设置页
   legacy token（`peerdrive_auth_key`）需开关 `peerdrive_auth_header_enabled`——
   两者同时存在时 fragment 优先（api.js getAuthToken 同语义）。

## 测试（8 单测，`scripts/test-layers.sh` L8 段；前端整体 32 项 × 4 文件）

> 命令：`cd front && npm test`

### 单元测试（front/tests/ws.test.js）

Mock socket 直接注入 `ws.__test._setSock`，`feedText` 手动喂文本帧、`onmessage` 喂
二进制帧，验证纯帧路由逻辑。`beforeEach` 调 `_reset()` + `localStorage.clear()`
（残留 pending 会在 onclose 时连带 reject 产生 unhandled rejection——测试间隔离）。

| 测试 | 覆盖 |
|---|---|
| `admin 请求按 reqId 路由响应` | 两请求乱序响应仍正确配对 |
| `admin 4xx 响应 → reject Error(err.status/err.data)（409 冲突清单语义）` | 结构化错误体透传 |
| `admin 请求携带 token（Authorization 语义）` | token 注入 |
| `download：data 头+二进制块按 binaryExpect 收集，done 帧 resolve` | 多块收集+done 触发 resolve |
| `download：err 帧 reject` | err 帧语义 |
| `admin-bin：二进制文件流响应收集为 Uint8Array` | 文件流响应 |
| `连接关闭 → 全部 pending reject` | 断线清理（先挂 catch 再 onclose，避免 unhandled rejection） |
| `upload：声明帧 + 二进制块按 BIN_CHUNK 切片上传（FileReader 回退路径）` | 150KB 文件分 3 块（64+64+22KB），FileReader 回退分支回归（BIN_CHUNK ReferenceError 修复） |

**发现背景**（文件头注释）：ws.js 是「前端全面迁移到 ws/peerjs」的核心客户端，帧
路由正确性直接决定页面能否工作——单槽 binaryExpect 必须与后端连接级 expect 语义
一致。本套单测是迁移批次的一部分（REFACTOR.md §3.10）。

### E2E 冒烟（front/tests/e2e-admin-smoke.mjs，121 行）

Node 22 原生 WebSocket 直连 `ws://localhost:3000/ws/peer`（本地起服后运行，
`PEERDRIVE_STORAGE=/tmp/pd-storage PORT=3000 go run ./cmd/server/`），走 admin verb
验证管理面全链路：`GET /ping` → 二进制上传 `/files/upload` → `GET /download/:hash`
（admin-bin 响应）→ 匿名集合 POST/GET → `GET /files` → 未知名路由 404 透传。与
ws.js 同一帧协议（实现独立复刻，二进制用 Buffer 收集）。

### 手动验证

- 浏览器打开页面，任一列表/上传/下载操作；DevTools Network 过滤 WS 帧，核对
  admin 声明帧与二进制块的连续性。
- 断网（关掉节点进程）观察所有页面请求报「ws: connection closed」，重启节点后
  下一请求自动重连成功。

## 文件清单

- `front/src/ws.js`（333 行）—— 本文档主体
- `front/tests/ws.test.js`（177 行）—— 帧路由 + upload 单测
- `front/tests/e2e-admin-smoke.mjs`（121 行）—— admin verb E2E 冒烟（需本地起服）
- 相关后端（协议对端，非本模块）：`back/internal/transport/admin.go`（admin verb 服务端）、
  `back/internal/transport/ws_session.go`（WSSession 会话实现）