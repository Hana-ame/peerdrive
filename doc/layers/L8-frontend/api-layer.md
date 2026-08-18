# API 封装层（front/src/api.js）

> 层归属：AOP ⑧ 前端切面（doc/LAYERS.md §1）。前端唯一的业务 API 门面：把后端全部
> HTTP 语义端点封装成具名函数，页面组件只 import * as api 调函数，不感知传输细节。
> 2026-08-17 起全部请求走本地 WS 会话 admin 帧（ws.js），HTTP 端点保留作 legacy。

## 职责

- **统一请求通道**：`request(method, path, body)`（api.js:148）= `ws.admin(...)`，
  全部管理端点（文件/集合/认证/BT/IPFS/任务/同步/访问列表/注册代理）经此发出；
  错误语义与旧 fetch 版一致（`Error.status`/`Error.data` 结构化 body）。
- **二进制 API 集**：上传（`uploadFile`/`btTorrentUpload`）、下载
  （`downloadFile`/`downloadFileToDisk`/`downloadAnonFile`/`downloadUserFile`/
  `downloadTorrentFile`）、预览（`getBlobUrl`/`revokeBlobUrl`，带缓存）。
- **配置管理（localStorage）**：多后端列表（api.js:14-111）、API base/token
  （api.js:113-143）、LLM 参数（api.js:313-339）、认证开关、P2P 网络参数
  （bootstrap/relay/STUN/TURN，api.js:351-367）、IPFS 开关、注册服务器 URL、
  数据同意标记。
- **直连第三方**：注册服务器（regserver）的组管理/评论接口不走 WS，直接
  `fetch(regServerUrl)`（api.js:473-506）——它们不在本地节点上。

**为什么存在**：页面组件需要与后端全部 controller 交互（17 端点组，见
doc/layers/L4-core/controllers.md）。迁移前直接 fetch HTTP URL；迁移后同一声明、
同一错误语义地走 WS admin 帧（REFACTOR.md §3.10），调用方零改动。

## 关键机制

### request() 与错误语义（api.js:148）

```js
async function request(method, path, body = null) {
  return ws.admin(method, path, body);
}
```

- `status>=400` → reject `Error`，挂 `err.status`/`err.data`（409 冲突清单等结构化
  错误体，见 ws.js handleText admin-resp 分支）。**这是页面弹窗消费错误详情的前提**
  （如 Explorer 合并冲突 `e.data.conflicts`、`e.status === 409` 判断）。
- 调用方无感传输迁移：签名与旧 fetch 版完全一致。

### token 体系（api.js:136）

`getAuthToken()`：`peerdrive_auth_token`（URL fragment `#token` 导入）恒优先；
否则设置页开关 `peerdrive_auth_header_enabled==='true'` 时用
`peerdrive_auth_key`；否则空串。与 ws.js `readToken()` 同语义（同步 localStorage）。

`setApiBase(url)`（api.js:121）：当用户输入 `http://host:3000#token123` 时拆出
fragment 存 token、base 存干净 URL——**token 永不混入 API base**。

### 多后端管理（api.js:14-111）

- `DEFAULT_BACKENDS`：`wsl`（https://wsl-3000.moonchan.xyz）+ `bwh`
  （http://97.64.30.221:3000），首次读取时落盘初始化。
- `switchBackend(id)`：写 `peerdrive_api_base` + 同步 STUN/TURN/凭据到全局
  localStorage（`peerdrive_stun_url` 等，api.js:54-56）。
- `addBackend`/`removeBackend`/`updateBackend`：运行时增删改（默认后端不可删；
  删除当前后端自动切回第一个可用）。

### 二进制上传/下载 API

| 函数 | 走 ws.js 的 | 后端端点语义 |
|---|---|---|
| `uploadFile(file)`（api.js:316） | `upload`（field `file`） | `POST /files/upload` multipart |
| `btTorrentUpload(file)`（api.js:284） | `upload`（field `torrent`，path `/bt/torrent`） | `POST /bt/torrent` multipart |
| `downloadFile(hash)`（api.js:157） | `download`（req verb） | sha256 内容寻址拉取 |
| `downloadFileToDisk(hash, filename)` | `downloadToFile` | 同上 + `<a download>` 落盘 |
| `downloadAnonFile(hash, p)`（api.js:224） | `admin GET`（admin-bin 响应） | 集合文件流 |
| `downloadUserFile(username, coll, filepath)`（api.js:250） | `admin GET` | 用户集合文件流 |
| `downloadTorrentFile(infohash)`（api.js:296） | `admin GET` | `.torrent` 文件流 |

- **encodePath**（api.js:221）：下载路径的虚拟路径逐段 `encodeURIComponent`
  （文件名可能含空格/`#`/`?` 等，不编码会破坏 URL；后端 gin `*filepath` 已对
  URL.Path 解码，编码后比对仍正确）。
- **getBlobUrl**（api.js:168）：经 WS 拉取 → Blob → objectURL，`blobUrlCache`
  Map 按 hash 缓存；`revokeBlobUrl` 由调用方在页面卸载时清理。**LRU 上限 50**
  （2026-08-18 审阅修复）：缓存只增不减时每个不重复 hash 的预览各占一个 Blob +
  objectURL，长时间浏览累积；超限逐出最久未用项并 revoke（Map 迭代序=插入序，
  重读 delete+set 刷新位置）。
- 集合下载虚拟路径含 64 位 hash 时先 `encodeURIComponent(hash)` 再拼路径
  （api.js:225）。

### 数据形态兼容层

- `registerURL`（api.js:197）：后端 `RegisterURL` 返回 `{hash,size,mime,filename}`
  （back file.go:120），`RegisterLocalFile` 只返回 `{hash,filename}`——统一补
  `mime_type`/`size` 兼容旧调用方。
- `createAnonCollection`（api.js:211）：entries 归一化为
  `{path, providers:[{type:'sha256'|'url', value, mime_type}]}`。
- `connectPeer`（api.js:261）：后端 `POST /p2p/connect` 绑定 `{addr}` 要求完整
  multiaddr（含 `/p2p/<peer_id>`）——自动补 `/p2p/` 后缀。

### 直连 regserver 的例外（api.js:473-506）

`getUserGroups`/`addUserToGroup`/`getComments`/`postComment` 直接
`fetch(regServerUrl, ...)` 带 `Authorization: Bearer`——评论/组管理由注册服务器
承载，本地节点不转发。这是 api.js 中仅有的非 WS 出网路径（另有 LLMAssistant
组件直接 fetch LLM 端点，不经过 api.js 的 request）。

## 与其它模块的关系

```
页面组件（pages/*）+ 全局组件（Navbar/LLMAssistant）
   │ import * as api
   ▼
api.js ──request()──▶ ws.js admin() ──WS──▶ back/internal/transport/admin.go
api.js ──download──▶ ws.js download() ──WS──▶ ws_session.go req verb
api.js ──直接 fetch──▶ regServerUrl（组管理/评论，例外）
```

- **下（ws.js）**：request/upload/download 均委托 ws.js；token 读取逻辑与其同步
  （双处各有一份，改一处必须改另一处——api.js 注释明确「与 ws.js 同步」）。
- **上（页面）**：全部页面 import * as api；`__mocks__/api.js` 为测试提供同签名
  空实现（见「测试」）。
- **对端（后端）**：HTTP 路由保留作 legacy（router.go LEGACY 注释区），仅供旧
  客户端/curl/集成测试——前端禁止直接 fetch 本地 HTTP 端点（LAYERS.md §5）。
  端点语义权威参考：doc/api-reference.md（标注了 2026-08-17 迁移说明）。
- **前端依赖事实**：`front/package.json` 只有 react 19 / react-dom / react-router-dom
  7 与构建测试依赖（vite 8 / vitest 4 / happy-dom / testing-library），**无
  peerjs/mqtt**——互联栈只存在于后端；vite.config.ts 也无 optimizeDeps 配置。

## 坑与设计决策

1. **全量迁移到 WS 的决策**：2026-08-17 前端全面迁移，HTTP 端点保留但标注
   legacy。管理面只走本地 WS（WebRTC 不实现管理 verb，防权限面漏洞）。迁移后
   「HTTP URL 直链」（`getDownloadUrl`/`getAnonFileDownloadUrl`/`getUserFileDownloadUrl`/
   `btGetTorrentUrl`）全部废弃，预览/下载改 Blob 方式（api.js 注释逐处标注）。
2. **connectPeer 字段坑**：旧实现发 `{peer_id, addrs}` 与后端 `{addr}` 不匹配，
   `req.Addr` 空串导致每次连接都 500——现在自动补完整 multiaddr（api.js:259-260）。
3. **getBlobUrl 缓存**：同 hash 只拉一次；objectURL 生命周期由调用方 revoke，
   否则内存泄漏（api.js:157）。
4. **saveConsentLocal 命名误导**：旧名 `uploadConsent` 让人误以为上传服务器，
   但后端没有 `/consent` 端点——只做本地记录（api.js:431-436）。
5. **token 双来源优先级**：URL fragment token 恒生效，legacy 设置 token 需开关；
   两者并存时 fragment 优先（api.js:136-143）。
6. **registerURL 字段差异**：`mime` vs `mime_type` 后端返回不一致，统一归一
   （api.js:197-202），页面只认 `mime_type`。
7. **直连第三方与 WS 并存**：regserver 接口不走 WS 是刻意的（不在本地节点上），
   新增「第三方服务」接口时沿此例，不要硬塞进 request()。

## 测试

无独立单测文件（api.js 是纯薄封装，逻辑在 ws.js 已测）。测试侧提供：

- **`front/src/__mocks__/api.js`**：全函数同签名空实现（`request()` 返回 `{}`、
  列表函数返回 `[]`），防止 happy-dom 环境真实 HTTP 请求在窗口 teardown 时产生
  AbortError/socket hang up 噪音（文件头注释写明原因）。
- **`front/tests/setup.js`**：`vi.mock('../src/api.js')` 全局挂 mock，组件测试
  （smoke.test.jsx / components.test.jsx / FileTree.test.jsx）自动生效。
- **手动验证**：浏览器任一页面功能 + DevTools Network 确认无 XHR/fetch 到
  `:3000`（除 /ws/peer 升级与 regserver/LLM 直连）。

## 文件清单

- `front/src/api.js`（514 行）—— 本文档主体
- `front/src/__mocks__/api.js` —— 测试 mock（同签名空实现）
- `front/tests/setup.js` —— `vi.mock('../src/api.js')` 全局挂载
- 相关对端（非本模块）：`front/src/ws.js`（传输层）、`doc/api-reference.md`（端点语义）