# Source 控制面设计（草案）

> 2026-08-19 · 文档先行。目标：在现有“读取面 Source 接口”之外，
> 增加每个 source 的**控制面**，让用户可以主动管理/写入/下载文件。
> 当前只记录设计，不改代码。

## 1. 为什么需要控制面

现在的 `Source` 接口是**只读**的：

- `Open`：按 hash 流式读取
- `Fetch`：按 hash 整体读取
- `Info`：查询元数据
- `Available`：是否可用

但实际使用还需要“写入/管理”能力，例如：

- 把本地已有文件加入 local source
- 直接写文件到 local source
- BT 主动下载某个 torrent / magnet
- IPFS 主动 pin / unpin / 下载 CID

这些不属于“按 hash 读取”，而属于 **source 控制面**。

## 2. 控制面原则

- **读取面保持稳定**：现有 `Source` 接口不破坏。
- **控制面作为可选能力**：不是所有 source 都必须实现。
- **统一入口**：由 `SourceControl`/路由统一收口，避免每个 source 各搞一套 HTTP。
- **一旦控制面写入完成**：文件进入内容寻址体系，之后仍通过读取面 `Open/Fetch` 获取。

## 3. 控制面接口草案

```go
// Control 是 source 的可选控制能力。
// 实现方可以只实现自己支持的子集；不支持的返回 ErrUnsupported。
type Control interface {
    // Local：把本地已有文件加入 source（计算 hash、登记索引）
    AddLocalFile(path string) (*FileMeta, error)

    // Local：直接写文件，写完后计算 hash 并登记
    WriteFile(name string, r io.Reader) (*FileMeta, error)

    // BT：下载 torrent / magnet 到本地存储，并登记结果文件
    DownloadTorrent(location string, opts TorrentOptions) (*TorrentTask, error)

    // BT：查询任务状态 / 取消任务 / 列表
    TorrentStatus(taskID string) (*TorrentTask, error)
    CancelTorrent(taskID string) error
    ListTorrents() ([]TorrentTask, error)

    // IPFS：主动 pin / unpin / 按 CID 下载
    PinCID(cid string) (*FileMeta, error)
    UnpinCID(cid string) error
    ListPins() ([]PinInfo, error)
}
```

> 具体方法名可以后续细化；这个草案先表达“每个 source 有什么控制能力”。

## 4. 各 source 控制面现状

| Source | 控制面能力 | 现有可复用代码 |
|---|---|---|
| Local | `AddLocalFile`、`WriteFile` | `transport.FileIndexService.Create`、`UploadSession` |
| Peer | 暂不定义 | 暂无 |
| URL | 暂不定义 | 暂无 |
| IPFS | `PinCID`、`UnpinCID`、`ListPins`；单独 serve IPFS 协议；control 控制其行为 | `internal/controller/p2p.go` 已有 pin 端点、`internal/provider/ipfs.go`、`front/src/pages/IPFSPanel.jsx` |
| BT | `DownloadTorrent`、状态/取消/列表 | `back/p2p_bt` 已有 DHT 获取，torrent 主动下载待接入 |

## 5. IPFS 源的特殊性：自己 serve IPFS 协议

IPFS 不只是“从公共网关拉取文件”的被动源，它还可以在本节点**单独 serve IPFS 协议**：

- 对外提供 `/ipfs/:cid` 或 IPFS 兼容 API，让其他客户端/节点直接访问本节点上的 CID。
- 内部通过 Bitswap / 网关 / DHT 回源。
- 是否启用、开放哪条路径、是否允许 pin、哪些网关可用，都由 Control 面控制。

因此 IPFS 的控制面设计分成两类：

| 分类 | 能力 |
|---|---|
| 内容管理 | `PinCID`、`UnpinCID`、`ListPins`、按 CID 下载 |
| 协议服务控制 | 启用/停用 IPFS serve、配置网关列表、切换 Bitswap/HTTP 模式、开放 `/ipfs/:cid` 路由、查看服务状态 |

对应控制接口可以扩展成：

```go
type IPFSControl interface {
    Control // 通用：AddLocalFile/WriteFile 等若适用

    // 内容管理
    PinCID(cid string) (*FileMeta, error)
    UnpinCID(cid string) error
    ListPins() ([]PinInfo, error)

    // 协议服务控制
    EnableIPFSServe(enable bool) error
    IPFSServeStatus() (*IPFSServeStatus, error)
    SetGateways(gateways []string) error
    SetBitswapEnabled(enabled bool) error
}
```

## 6. 推荐落点

- 新建 `internal/source/control.go`：定义 `Control` 接口与 `TorrentOptions` 等模型。
- `LocalSource` 实现 `AddLocalFile` / `WriteFile`，复用现有 file-index/upload 逻辑。
- BT 控制面先包住 `back/p2p_bt`，任务状态存 `repository` 或内存表。
- IPFS 控制面可以复用现有 controller 的 pin 逻辑，后续收编到统一 `SourceControl`。
- 管理入口走 router/admin：例如
  - `POST /sources/local/add`
  - `POST /sources/local/upload`
  - `POST /sources/bt/download`
  - `GET /sources/bt/tasks`
  - `POST /sources/ipfs/pin`
  - `POST /sources/ipfs/serve/enable`
  - `GET /sources/ipfs/serve/status`

## 7. BT / IPFS 做成可选 DLL/插件（架构偏好）

用户偏好：BT 和 IPFS 都做成可选外部模块（Windows 下可叫 DLL，**EXE 也可以接受**），
**不需要时就不带这个模块**，核心 peerdrive 仍然可以工作。
评判标准不是“必须 DLL 还是 EXE”，而是：**只要能控制、能当 Source 用**。

### 为什么合理

- BT/IPFS 涉及较重依赖、外部网络协议、open-source 库，不是人人需要。
- 做成可选模块后：
  - 主程序不强制引入 BT/IPFS 依赖。
  - 只要系统里没有对应 DLL/插件，对应 source/control 就显示“不可用”。
  - 需要时才部署对应 DLL/插件，不影响主程序升级。

### Go 里的可选模块方案

| 方案 | 说明 | 适合场景 |
|---|---|---|
| **独立进程/服务**（推荐首选） | BT/IPFS 各自做成独立 EXE/本地服务，主程序通过 HTTP/gRPC 调用；不需要时就不部署 | 跨平台最省事，不需要 CGO/DLL 加载，EXE 可接受 |
| **c-shared DLL** | 用 cgo 把 BT/IPFS 编译成 Windows DLL / Linux .so，主程序动态加载 | 如果必须“一个 DLL 文件”形态 |
| **Go plugin** | Go 官方 plugin（`.so`） | 仅 Linux，Windows 不支持 |
| **build tags 可选编译** | `//go:build bt && ipfs`，不满足 tag 就不编译对应代码 | 构建期决定，不是运行期动态加载 |
| **独立 go.mod** | 像现在的 `back/p2p_bt` 一样做成独立仓库/模块，主程序按需 replace | 已经具备类似结构 |

> 考虑到项目在 Windows 下，DLL 和 EXE 都可接受；推荐优先 **独立 EXE/本地服务**，
> 用稳定的本地接口（HTTP/gRPC/JSON）暴露“控制 + 当 Source 读取”的能力。
> 如果只是“不需要就不带”，独立进程或 build tags 更简单可靠。

外部模块只要满足以下条件，无论 DLL 还是 EXE 都算合格：

```text
1. 可被主程序控制：加载/卸载、启用/停用、pin/下载/任务管理等。
2. 可当 Source 用：主程序能通过它按 hash/CID/磁力等获取文件内容。
3. 未安装时主程序仍能正常工作，对应能力标记为“不可用”。
```


### 预留接口

在 Control 面上增加“能力探测”，让主程序知道哪些外部模块可用：

```go
type SourceControl interface {
    // 检测外部模块是否已加载/可用
    CapabilityStatus() map[string]CapabilityStatus
}
```

每个可选 DLL/模块暴露同一套本地接口：

- 加载时注册：`local` / `peer` / `url` 是核心，始终存在。
- 可选模块：`ipfs` / `bt` 未加载时，`Available=false`，相关控制入口直接返回“模块未安装”。

### 开源库参考（后续选型）

- IPFS / Bitswap：
  - `boxo`（IPFS 底层库，bitswap / gateway）
  - `kubo` / `go-ipfs` RPC 或 HTTP API（作为独立进程接入）
- BT / DHT：
  - `github.com/anacrolix/torrent`
  - `github.com/anacrolix/dht/v2`
  - 现有 `back/p2p_bt` 已经是独立 go.mod，可以继续作为 BT 模块基础

> 当前阶段只记录方向，不绑定具体库；实际接入时再根据许可证/体积/稳定性选择。
