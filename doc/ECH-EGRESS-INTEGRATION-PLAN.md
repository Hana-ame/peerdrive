# ECH 出口整合架构与演进实施路线规范

> 对应 Issue: #107  
> 状态: 架构规划与分阶段路线确立（按用户定性：建立统一 ECH 出口体系）  
> 关联 Issue: #47 (echcore 并入), #57 (文档引用修正), #59 (Source 接口统一), #115 (架构总纲 - 外部协议切面)

---

## 1. 核心定位：建立统一的「ECH 出口」

Peerdrive 节点的出网数据通信（Egress）严格分为两大并立通道：

| 属性 | ① 直接出口 (Direct Egress) | ② ECH 出口 (ECH Egress) |
|---|---|---|
| **核心机制** | 标准 `http.Client` (标准 DNS + 标准 SNI) | 基于 `internal/echcore` (DoH + ECH 域前置 + uTLS 指纹 + IPMode 控制) |
| **主要用途** | 访问开放公网、标准直连 CDN、无封锁源站 | 规避 SNI 阻断、绕过特定域名封锁 (Cloudflare 域前置、Pixiv、Twitter、ExHentai 等) |
| **数据源映射** | `source.URLSource` (`Type() == "url"`) | `source.EchSource` (`Type() == "ech"`) |
| **地址表达** | 模板化 URL (`fmt.Sprintf`) | 程序化 `Resolver` (支持 X-Version 签名与动态 Token 计算) |
| **出口契约** | `http.RoundTripper` | `source.Egress` 接口 (`Do(ctx, req) (*http.Response, error)`) |

---

## 2. 演进路线图：三个 PR 分阶段执行

```text
[现状: 外部 exe + 局部直接调用]
       │
       ▼
[PR2: echproxy 去 exe 化] ──► 移除 ProcessManager，exhentai/twimg 统一使用 echcore 库内拨号
       │
       ▼
[PR3: source 统一收敛 Egress] ──► URLSource / EchSource 均支持透明注入 Egress 实例
       │
       ▼
[PR4: media-node 多模式扩展] ──► 从单一 url 帧扩展支持 sha 内容寻址与 ech-url，复用 source 管线
```

---

## 3. PR2 详细设计：echproxy 去 exe 化

### 3.1 现状缺陷
历史实现的 `back/internal/echproxy/process.go` 引入了 `ProcessManager`：
- 在运行时联网下载外部 `ech-proxy` 可执行二进制文件；
- 本地监听 `127.0.0.1:8443`；
- 通过 `RoundTripper` 将流量重写至本地端口代理转发。
这违背了项目「纯 Go、单二进制、零外部宿主依赖」的基石原则。

### 3.2 改造方案
1. **废弃 `ProcessManager`**：
   - 移除下载 exe、监听子进程、探测端口的全部胶水代码；
2. **直连 `echcore` 原语**：
   - `exhentai.RoundTripper` 与 `twimg.RoundTripper` 不再拨号 `127.0.0.1:8443`，而是直接调用 `echcore.Do(req)` 或基于 `echcore.NewClient()` 封装内部 RoundTripper；
3. **保持无侵入透明性**：
   - 当模块未启用时，保持为 no-op；启用时，通过库内 uTLS 握手完成域前置，性能与稳定性大幅提升。

---

## 4. PR3 详细设计：Source 统一接入 ECH Egress

### 4.1 `Egress` 契约边界裁决
依据 Issue #107 结论：**`Egress` 接口定义留在 `internal/source` 包中**。
- `echcore` 保持为纯净的底层网络拨号原语；
- `source.Egress` 作为消费端接口，允许注入：
  1. `echcoreEgress`（走 `echcore.Do`）；
  2. 标准 `http.Client`（直接出口）；
  3. 离线测试 Mock（`httptest`）。
- `source.URLSource` 增加可选的 `Egress` 注入字段，实现与 `EchSource` 的底层传输完全收敛。

---

## 5. PR4 详细设计：media-node 多模式扩展

### 5.1 现状与扩展
- `cmd/media-node` 已经完成了 `echcore.InitDefault` 的接入；
- 但协议帧目前只接收 `Msg.Type == "url"`；
- 扩展后：
  - 支持 `Type == "url"`（现有 twimg 链路维持向后兼容）；
  - 支持 `Type == "sha"`（向近端节点或 CAS 索引拉取媒体流）；
  - 支持 `Type == "ech-url"`（通用 ECH 前置媒体直取）。

---

## 6. 验收基准

1. **零外部 exe 依赖**：全仓编译与运行不再触发任何外部二进制下载，`ProcessManager` 彻底退出历史舞台；
2. **交叉编译矩阵 100% 绿灯**：`.github/workflows/go-build.yml` 的 5 平台矩阵保持全部通过；
3. **测试覆盖**：`echcore` 离线单元测试与 `source` 契约测试均无需依赖真实外网连接。
