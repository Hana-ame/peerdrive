# Egress Abstraction: 评估 + 决策（Issue #273）

> 关联 Issue: [#273](https://github.com/Hana-ame/peerdrive/issues/273)
> 关联设计: [`doc/UNIFIED-EGRESS-ABSTRACTION.md`](../UNIFIED-EGRESS-ABSTRACTION.md)（#193/#195/#197）
> 分支：`feat/issue-273`（本评估对应 PR 落地）
> 结论时间：2026-10（评估作者：architecture-agent）

---

## 1. 结论摘要（TL;DR）

**推荐结论：冻结 + 标注（Freeze + Annotate），暂不接线、暂不瘦身。**

- `back/internal/egress` 共 1640 LOC，实际引用点**恰好 2 处**：`controller/download.go` 的
  `ParseRangeHeader`（真实消费者，2026-08 已接入）与 `source/manager.go:30` 的接口断言
  （编译期契约校验）。设计稿里的三条消费出口（HTTP / WS / PeerJS）**均未接入** Pipeline。
- **接线不可行**：这不是"在某处加一行 `pipeline.Serve(...)`"的问题，而是一次**跨三条
  消费链路的中型重构**——每条链路当前实现都**已经比设计稿里的 Pipeline 更贴合现实**
  （http.ServeContent 原生支持 If-Range/ETag 条件请求；PeerJS serveFile 已经集成了
  QoS 限流、file_index 路径安全、Authorizer 门禁、Trace 防环）。强行收敛会**回归
  这些已实现的安全/流控能力**，而不是把它们搬进 Pipeline。
- **瘦身风险高**：`range.go` 的 `ParseRangeHeader` 是 HTTP 面唯一的 RFC 7233 解析器
  （controller/download.go 依赖它）；`egress.go` 里的块池 `chunkPool` 概念在
  transport/inbound.go 里被独立复刻了一遍（64KB 缓冲池，同样注释）；三条 sink 契约
  虽未被消费，但**测试覆盖完整**（22+5 个测试用例），是"下一次真正接线"的脚手架。
  删了之后重做代价不低于现在冻结维护。
- **冻结成本最低**：给 `internal/egress` 包加"未接线 / 实验性"包级注释，在
  `UNIFIED-EGRESS-ABSTRACTION.md` 里标出实际状态与下一次接线的**前置条件**，让维护者
  在真正要启动 #165 WebDAV 客户端时，能带着**明确的接线前提**再动 Pipeline。

---

## 2. 现状审计（实读结论）

### 2.1 egress 包全部引用点

```
back/internal/controller/download.go:23:   "peerdrive/internal/egress"
back/internal/controller/download.go:297:  offset, size, isSatisfiable, valid := egress.ParseRangeHeader(rangeVal, fileSize)
back/internal/source/manager.go:26:        "peerdrive/internal/egress"
back/internal/source/manager.go:30:        var _ egress.ContentProvider = (*Manager)(nil)
```

**结论**：全仓库只有 **2 处真实引用**，Issue #273 的判断准确：

1. **`controller/download.go:297`** — `parseRangeHeader()` 委托到 `egress.ParseRangeHeader`，
   是 HTTP 面唯一的 RFC 7233 Range 解析入口。**有真实运行时消费者**（`DownloadBySHA256Internal`
   → `handleRangeRequest` → `parseRangeHeader`）。**绝对不能删**。
2. **`source/manager.go:30`** — `var _ egress.ContentProvider = (*Manager)(nil)` 是**编译期
   接口断言**，不是运行时调用。`Manager` 已经实现了 `OpenRange` + `InfoSize`（`ContentProvider`
   契约要求）；这一行只是让编译器记住"未来若有人把 Manager 改坏了，Pipeline 编译不过"。
   **无运行时收益**，删了不会让任何东西失效，但保留成本极低。

### 2.2 三条消费出口的实际路径

| 出口 | 实际路径 | 设计稿期望 | 是否走 Pipeline |
|---|---|---|---|
| **HTTP** | `controller/download.go`：`DownloadBySHA256Internal` → `universalDownloader.Download` 返回 `[]byte` → `handleRangeRequest` + `c.Data`；`DownloadBySHA256Local` → `http.ServeContent` | `egress.Pipeline` + `HTTPEgressSink` | ❌ 未接入 |
| **WS** | `transport/admin.go:dispatchAdmin` → gin recorder → `SendFrame(admin-bin header, respBody)` 单帧一发（≤ 64MB） | `egress.Pipeline` + `WebSocketEgressSink`（分块流式） | ❌ 未接入（且是**单帧一次性**，非流式） |
| **PeerJS** | `transport/inbound.go:serveFile` → `router.OpenRange` → `SendFrame(dcReq.data ...)` 分块循环（64KB 缓冲池、QoS 限流、Authorizer 门禁、Trace 防环） | `egress.Pipeline` + `PeerJSEgressSink` | ❌ 未接入 |

三条链路确实**各走各的老管线**，Issue #273 的描述准确。

### 2.3 egress 包内容与测试规模

| 文件 | LOC | 内容 |
|---|---|---|
| `egress.go` | 131 | 核心模型（EgressRequest/Meta/Sink 接口）、块池 `chunkPool`、`GuessMimeType` |
| `pipeline.go` | 179 | `Pipeline.Serve`：hash 校验 → 防环 → Range 钳制 → Meta → 64KB 循环泵 → 背压 |
| `range.go` | 134 | `ParseRangeHeader`（真实消费者）、`NewRequestFromHTTP`、`FormatContentRange` |
| `http_sink.go` | 137 | `HTTPEgressSink`：206 / 416 / Content-Range / ETag |
| `ws_sink.go` | 101 | `WebSocketEgressSink` + `FrameSender` 适配 |
| `peerjs_sink.go` | 97 | `PeerJSEgressSink`：meta / data / done / err 帧 |
| `egress_test.go` | 568 | **22 个**测试用例，覆盖 HTTP 全量/Range/空文件/背压/取消/错误路径 |
| `range_test.go` | 293 | **5 个**测试用例，覆盖 RFC 7233 矩阵 + `<video>` 流式断言 |
| **合计** | **1640** | |

---

## 3. 接线方案评估（Wiring）

### 3.1 HTTP 面接线

**目标路径**：`controller/download.go` → `egress.Pipeline` + `HTTPEgressSink`。

**发现的三个阻塞点**：

1. **`DownloadBySHA256Local` 已经用 `http.ServeContent`**——Go 标准库的 `ServeContent` 是
   2007 年就有了、2024 年还在修的成熟实现，支持 RFC 7233（206/416/If-Range）、`ETag`、
   `Content-Range`、`Last-Modified` 条件请求。**换成 Pipeline 会丢失 `If-Range` 支持**
   （当前 `HTTPEgressSink.WriteMeta` 里没有 If-Range 分支），是一次**功能回归**。

2. **`DownloadBySHA256Internal` 的数据源是 `universalDownloader.Download(ctx, hash) ([]byte, ...)`**
   ——返回整个 `[]byte`（全文件入内存），不是 `io.ReadCloser`。要接到 Pipeline，必须先
   改 `UniversalDownloader` 提供流式 API，那是一个**独立的重构**。或者用
   `bytes.NewReader` 包一层，但那只是把"整文件内存"从 controller 挪到 pipeline 内部，
   **不解决设计稿 §1.2 提到的核心痛点**（"消除 os.ReadFile 整文件内存加载"）。

3. **`EgressMeta` 没有扩展点携带业务自定义头**：`DownloadBySHA256Internal` 会写
   `Content-Disposition`（inline/attachment + filename）、`Content-Encoding`（gzip）、
   `X-Protocol`（多协议路由命中）、`X-Peerdrive-Collection`（合集标记）。
   `HTTPEgressSink.WriteMeta` 里 `h.Set` 会覆盖调用者预设的头——虽然调用者可以先用
   `c.Header(...)` 预设（WriteHeader 之前不会 commit），但一旦 Pipeline 未来加新头，
   就容易出现"业务头被 pipeline 覆盖"的隐性 bug。

**风险等级**：高。**工作量**：中型（3-5 天，含 UniversalDownloader 流式化 + 回归测试 + E2E）。
**结论**：现阶段接线**性价比为负**。

### 3.2 PeerJS 面接线

**目标路径**：`transport/inbound.go:serveFile` → `egress.Pipeline` + `PeerJSEgressSink`。

`serveFile` 当前的实现（约 140 行）已经包含：
- hash 校验（`hashutil.IsStrictSHA256`）—— Pipeline 也有
- QoS 并发上限（Issue #269）—— Pipeline **没有**
- Authorizer 门禁（Phase 7 身份校验）—— Pipeline **没有**
- Trace 防环（append 本节点 ID）—— Pipeline 也有（但语义有细微差别，见 §3.4）
- 元数据查询（`router.InfoSize`）—— Pipeline 用 `provider.InfoSize`
- `router.OpenRange` 流式打开 —— Pipeline 用 `provider.OpenRange`
- 64KB 缓冲池 + 分块循环 —— Pipeline 完全一致
- QoS 单帧节流（`s.qos.Throttle(n)`）—— Pipeline **没有**
- `SendFrame(dcReq.data ...)` 分帧发送 —— `PeerJSEgressSink.WriteChunk` 等价

**要真正接线，必须**：
- 把 QoS 并发 + 单帧节流**注入 Pipeline** 或**留在 sink 层**。前者要改 Pipeline 签名，
  后者要 sink 感知 offset/size，会破坏"sink 只管协议"的抽象。
- 把 Authorizer 门禁放在 Pipeline **之前**（调用者负责），这是可行的——但那样
  "Pipeline 统一入口"就变成了"Pipeline 之前还有一大段业务前置代码"，抽象价值大幅缩水。
- 处理 QoS 单帧节流：`PeerJSEgressSink.WriteChunk` 里可以调用 `qos.Throttle(n)`，
  但需要 sink 构造时注入 qos 依赖，会**污染 sink 接口**。

**风险等级**：中-高。**工作量**：中型（3-4 天）。**关键风险**：PeerJS 是主链路
（网盘消费端 + 面板 + E2E 都靠它），任何回归都会直接砸用户。

### 3.3 WS admin-bin 面接线

**目标路径**：`transport/admin.go:dispatchAdmin` → `egress.Pipeline` + `WebSocketEgressSink`。

**发现**：admin-bin **不是流式的**——gin handler 已经跑到 `[]byte`，一次性
`SendFrame(admin-bin header, respBody)` 发出去，还有 64MB 上限保护。
设计稿假设的"admin-bin 分块流式"从来没存在过。

要接线，等于**重写 admin-bin 的语义**：从"单帧"变"分块帧 + done 帧"，前端 `binaryExpect`
逻辑也要跟着改，E2E 面板测试要跟着改。

**风险等级**：高。**工作量**：中型（前端 + 后端联动）。**结论**：**不接线**，
admin-bin 走"单帧一次性"是刻意设计（避免前端状态机复杂化）。

### 3.4 Pipeline 自身的设计缝隙（次要发现）

- `pipeline.go:100` `fwdTrace := append(append([]string{}, req.Trace...), p.nodeID)`：
  Pipeline 会把 nodeID 追加进 `context.WithValue`，但**sink 侧并不感知**——实际消费端
  若要防环，还得在**收到 sink 事件时**自己再判一次。当前 serveFile 就是在
  **调 Pipeline 之前**做防环，逻辑重复。
- `pipeline.go:92` `if hs, ok := sink.(*HTTPEgressSink); ok { hs.SetTotalSize(total) }`：
  **类型断言**到具体 sink 类型——**破坏抽象**。如果未来加了 `GinSink`、`MuxSink`，
  416 分支都要重复写。
- `range.go:93 NewRequestFromHTTP` 存在，但**零消费者**（无 HTTP 面用它构建 req）。
- `egress.go:45 PutChunkBuffer`：`if cap(b) >= DefaultChunkSize { b = b[:DefaultChunkSize] }`
  —— 用 `len(b)` 判断更严谨（当前实现"cap 大但 len 小"也归还，但 slice 长度被强行
  截断，可能触发下游 `Write` 越界？实际没触发，因为没有消费者）。

这些不是阻塞接线的硬问题，但反映 Pipeline 还没被"真实使用压力"打磨过——**冻结**
让维护者在下次接线时能一并整理。

---

## 4. 瘦身方案评估（Slim Down）

**主张**：删除未消费的 sink 和 pipeline 代码。

**反主张（本次结论支持的理由）**：

1. **`range.go` 不能删**——`ParseRangeHeader` 有真实消费者（`controller/download.go`）。
2. **`egress_test.go` 覆盖 22 个测试用例**——不是"死代码"，是**架构脚手架 + 契约测试**。
   下一次真接线时（#165 WebDAV 客户端）会直接受益，比从 git history 挖出来便宜。
3. **`chunkPool` 与 `transport/inbound.go` 的块池是**"同一思想的两处实现"**，
   不是重复代码**——前者是抽象层的，后者是具体实现层的。抽象层现在没人用，但
   概念已固化（REFACTOR 里已有多处提到"64KB 块池"）。
4. **接口契约的价值**：`egress.ContentProvider` 让 `source.Manager` 天然成为 Pipeline
   的 provider——`manager.go:30` 的断言不是死代码，是"下一次接线的钩子"。
5. **删除 1640 LOC 会让 Issue #273 从"评估 + 决策"变成"删东西"**——Issue 描述里
   明确写"不要无脑删 1640 行"。

**结论**：**不瘦身**。

---

## 5. 冻结 + 标注方案（本次落地）

### 5.1 改动清单

- `back/internal/egress/egress.go`：包级注释加 `// EXPERIMENTAL` 标注 + 当前状态说明
  + 指向本评估文档。
- `doc/UNIFIED-EGRESS-ABSTRACTION.md`：状态从"架构设计完成 · 待分阶段实现"更新为
  "Phase 3 阻塞 · 消费端未接线 · 见 EGRESS-ABSTRACTION-EVALUATION.md"，让后续接线
  的人先读评估文档。
- **不删除任何代码，不改动任何测试，不改任何消费端行为。**

### 5.2 下一次接线的**前置条件**（维护者读这段做决策）

如果 #165 WebDAV 客户端启动时真正要接 Pipeline，建议按下列顺序：

1. **先流式化 `UniversalDownloader`**：暴露 `DownloadStream(ctx, hash) (io.ReadCloser, protocol, total int64, err)`，
   让 `DownloadBySHA256Internal` 走 `http.ServeContent`（对齐 `DownloadBySHA256Local`）。
   这一步**不依赖 Pipeline**，是纯工程改进，可以独立 PR。
2. **补齐 `HTTPEgressSink` 的 If-Range 支持**：读 `r.Header.Get("If-Range")` +
   `meta.ETag` 比对；不匹配则退化为 200 全量。补一个 `<video>` 拖动刷新的 E2E。
3. **把 QoS / Authorizer 注入 sink**：`PeerJSEgressSink` 构造时可选注入 `QOShaper`
   + `Authorizer` 依赖，`WriteChunk` 内部节流。避免污染 Pipeline 签名。
4. **拆掉 Pipeline 里对 `HTTPEgressSink` 的类型断言**：改成 `Sink.SetTotalSize`
   通用方法（或者从 Meta 里已经带了 TotalSize，直接靠 sink 自己存）。
5. **最后才做 PeerJS serveFile → Pipeline 的收敛**：把 serveFile 里的
   hash 校验 / QoS / 防环 前置；sink 走 Pipeline；sink 的 backpressure hook 挂
   DataChannel `OnBufferedAmountLow`。
6. **admin-bin 不动**：单帧一次性是刻意设计，不接线。

**如果 #165 迟迟不动**，Pipeline 就继续冻结；每次有人想"顺便接一下"，先读本评估文档
§3 再看成本。

---

## 6. 决策记录

| 选项 | 是否采纳 | 理由 |
|---|---|---|
| ① 接线 | ❌ | 三条链路各走各的老管线，且现状已**超过** Pipeline 抽象能提供的能力（QoS / Authorizer / If-Range / http.ServeContent 语义）。接线是**中型重构**，风险 > 收益。 |
| ② 瘦身 | ❌ | `ParseRangeHeader` 有真实消费者不能删；测试覆盖完整、是下次接线的脚手架；删了重做代价不低于维护。 |
| ③ **冻结 + 标注** | ✅ | 最低成本决策；不破坏任何现状；留下评估文档让下次接线的人读。 |

---

## 7. 相关文件索引

- 本评估：`doc/design/EGRESS-ABSTRACTION-EVALUATION.md`
- 原设计：`doc/UNIFIED-EGRESS-ABSTRACTION.md`（本次已更新状态）
- egress 包：`back/internal/egress/`（本次已加包级 EXPERIMENTAL 注释）
- 唯一真实消费者：`back/internal/controller/download.go:297`（`ParseRangeHeader`）
- 唯一接口断言：`back/internal/source/manager.go:30`（`ContentProvider` 契约）
- 三条实际消费链路（**不接线**）：
  - HTTP：`back/internal/controller/download.go`（`DownloadBySHA256Internal` / `DownloadBySHA256Local`）
  - WS admin-bin：`back/internal/transport/admin.go:dispatchAdmin`
  - PeerJS serveFile：`back/internal/transport/inbound.go:67`
