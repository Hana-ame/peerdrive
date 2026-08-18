# 管理面切面（AOP ③）— transport/admin.go + router 装配

> 一句话职责：浏览器经**本地 WS 会话**（/ws/peer）发送 admin/admin-bin 帧管理本节点——内部转发 gin engine，复用全部 HTTP controller，零重复实现；WebRTC 连接不实现管理 verb，防权限面暴露。

## 职责

- 定义管理面帧协议：`admin`（请求声明，JSON body 或 binary 上传）、`admin-resp`（JSON 响应）、`admin-bin`（二进制响应头）、`err`（失败）
- 把 admin 帧翻译成内部 `*http.Request` → 注入 gin engine 的 `ServeHTTP`（httptest recorder）→ 把 HTTP 响应还原成帧
- 二进制上传：声明帧 + 后续二进制帧收集到临时文件 → 构造 multipart/form-data 转发（controller 的 `FormFile` 读取无感知）
- 二进制响应（文件流）：`admin-bin` 头 + 单二进制帧
- 认证透传：前端 admin 帧带 token → 转发时注入 `Authorization: Bearer <token>` → gin `AuthRequired` 中间件行为与 HTTP 完全一致

## 关键机制

### 1. 帧协议（admin.go:14-27 注释 + 结构体）

```
浏览器 → 本地会话（请求）:
  {"type":"admin","method":"GET|POST|...","path":"/files/upload?x=1","body":{...},"token":"...","reqId":"..."}
  binary 上传声明: {"type":"admin","method":"POST","path":"/files/upload","binary":true,
                    "filename":"a.bin","field":"file","size":12345,"reqId":"..."}
  声明后: 后续二进制帧作为请求体（收齐 → multipart 转发）

本地会话 → 浏览器（响应）:
  JSON 响应   → {"type":"admin-resp","status":200,"body":<原始 JSON>,"reqId"}
  文本响应   → {"type":"admin-resp","status":200,"body":"pong","reqId"}
  二进制流   → {"type":"admin-bin","status":200,"size":N,"reqId"} + 紧随一个二进制帧
  失败       → {"type":"err","msg":"...","reqId"}
```

### 2. 会话隔离（serveAdmin, admin.go:112-117）

```go
if c.ID() != "local" {  // WebRTC/PeerJS 连接一律拒绝
    SendJSON(err: "admin verb is only allowed on the local session")
}
```

**为什么**：WebRTC 连接可能来自公共信令上的任意节点——若管理 verb 同样实现，等于把本节点管理口（认证 token、文件读写、集合变更）开放给未知对端。管理面固定走本地 WS 会话（`id="local"`，Origin 白名单校验，见 peerjs_routes.go `registerPeerJSRoutes`）。

### 3. 内部转发（dispatchAdmin, admin.go:272-305）

```
admin 帧 → buildAdminRequest(method, path, body, contentType, token)
        → s.adminHandler(req)   // router.go:357 注入的 gin engine 包装
        → 响应分类:
             json.Valid(respBody)           → admin-resp（反序列化 body）
             status>=400 / text/*           → admin-resp（字符串 body）
             其余二进制（文件流）           → admin-bin 头 + SendFrame 二进制体
```

**响应分类坑（admin.go:280-282，发现背景：E2E 冒烟 /ping 返回 Buffer）**：初版按 respBody 是否以 `{` 开头判断——`text/plain` 响应（如 /ping 的 "pong"）被误判为二进制 → 前端收到 Uint8Array 而非字符串，且 admin-bin 占用 binaryExpect 槽。修复：`json.Valid` 优先，非 JSON 时再按 status/content-type 判断。

**二进制上限（admin.go:43-45）**：`adminBinMax = 64MB`——内部转发已把整个响应驻留内存（httptest recorder），大文件应走 req verb 分片拉取（前端 ws.js 下载路径）。响应超限返回 413 提示走 req verb。

### 4. 二进制上传（admin.go:130-165, 186-250）

- 声明帧同步占槽（`st.adminUp`，**必须在消息泵内同步设置**——否则泵已路由后续二进制帧时 adminUp 仍为空 → 数据块丢失。发现背景：初版 case "admin" 全部 go 异步，上传分片先于 adminUp 到达，上传永远收不齐，admin.go:109-111）
- 块写入走 worker goroutine（`adminUploadChunk`，IO 移出消息泵，与 H5 对 fileIndex 上传一致）；last 块触发 `serveAdminUploadComplete`
- **上传转发路径坑（admin.go:150-155）**：声明帧的 path 即转发目标——BT torrent 上传传 `path=/bt/torrent`（field=torrent）；旧实现硬编码 /files/upload 导致 torrent 上传打错端点（发现背景：前端 btTorrentUpload 迁移时核对 admin 帧与后端转发路径）
- **Seek(0) 坑（admin.go:216-217）**：au.f 的写 offset 已在文件末尾，必须 `Seek(0)` 从头读，否则 io.Copy 读到 0 字节（发现背景：admin 上传测试 got 0 bytes）
- 超时防护（admin.go:47-49）：`adminUploadTimeout = 30s`——浏览器声明后不发数据块会永久占位（同 M6 对 upload verb 的修复）；防御替换旧槽（`os.Remove` 清临时文件）
- 临时文件必清：`defer os.Remove(au.path)`（正常/异常路径都覆盖）

### 5. 装配（router.go:350-362）

```go
peerjsService.SetAdminHandler(func(req *http.Request) (int, []byte, string, error) {
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)          // 复用整个 gin engine（含全部中间件/controller）
    return rec.Code, rec.Body.Bytes(), rec.Header().Get("Content-Type"), nil
})
```

- `SetAdminHandler` 加锁（adminMu）：装配期设置、之后只读（serveAdmin 并发调用）
- adminHandler 为 nil 时返回 "admin handler not configured"（防御未装配即用）

## 与其它模块的关系

```
前端 ws.js admin()/upload()
  └─ admin 帧 → WSSession(id="local") → transport 分派（bindConn case "admin"）
       └─ serveAdmin → buildAdminRequest → gin engine（router.go）
            └─ 全部 HTTP controller（AOP ④）：文件/集合/分享/任务/BT/IPFS
                 └─ service → repository / source / downloader
```

- 与旧 upload verb（文件索引/下载目录会话）**互斥独立**：admin 上传走 storageDir 内容寻址存储（/files/upload 语义）
- 前端侧协议客户端见 `doc/layers/L8-frontend/ws-client.md`（含帧格式实例）
- 帧协议全局定义以 `doc/REFACTOR.md` §3.10 / §4 为准

## 坑与设计决策

| # | 坑 | 修复 |
|---|---|---|
| — | 管理面暴露给 WebRTC = 权限面全开 | 仅 `c.ID()=="local"` 会话可发 admin |
| — | 初版 admin 全异步 → 上传分片先于 adminUp 到达 | 声明帧同步占槽（泵内） |
| — | 响应按 `{` 开头判断 → /ping "pong" 误判二进制 | json.Valid + content-type 三级分类 |
| — | multipart 转发未 Seek(0) → 上传 0 字节 | Seek(0) 从头读 |
| — | BT torrent 上传硬编码 /files/upload | 声明帧 path 即转发目标 |
| — | 声明后不发块 → 永久占位 | 30s 超时 + 槽替换清理 |
| — | 大文件响应驻留内存 | adminBinMax 64MB + 413 提示走 req verb |

## 测试（9 单测，`scripts/test-layers.sh` L3 段）

- `admin_test.go`（`back/internal/transport/`）：admin 帧协议单测——JSON 响应分类、
  二进制上传收齐/写失败清理/中止清理、token 注入 Authorization、非 local 会话拒绝、
  文本响应分类
  （`go test -tags nosqlite ./internal/transport/ -count=1 -run "^TestAdmin"`）
- 发现背景均标注在测试 doc comment（全局 AGENTS.md 硬性要求）

## 文件清单

| 文件 | 说明 |
|---|---|
| `back/internal/transport/admin.go` | 本模块（315 行：协议结构体 + serveAdmin + 上传收集 + 转发 + 响应分类） |
| `back/internal/transport/admin_test.go` | 单测 |
| `back/internal/router/router.go:350-362` | SetAdminHandler 装配（httptest recorder 包装 gin engine） |
| `back/internal/router/peerjs_routes.go` | /ws/peer 本地会话路由（Origin 白名单） |
| `front/src/ws.js` | 前端 admin()/upload() 客户端（见 L8-frontend/ws-client.md） |