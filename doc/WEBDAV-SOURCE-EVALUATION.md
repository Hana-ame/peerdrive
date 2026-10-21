# WebDAV 数据源接入与可行性评估报告

> 对应 Issue: #165  
> 状态: 评估完成，技术路线与接口设计已确立  
> 关联 Issue: #163 (SMB 数据源评估), #106 (OpenList 数据源), #56 (OpenListSource), #59 (Source interface 统一), #111 (资源访问权限), #115 (架构划分总纲), #193 (统一出口抽象 `doc/UNIFIED-EGRESS-ABSTRACTION.md`)

---

## 1. 架构定位：数据源客户端，而非对外服务

Peerdrive 自身的内容对外暴露链路已经由 PeerJS 信令 + WebRTC DataChannel、局域网直连、HTTP/WebSocket 统一承载（遵循 `doc/ROADMAP.md`、`doc/NETDISK.md` 与统一出口规范 `doc/UNIFIED-EGRESS-ABSTRACTION.md`）。

本功能定位**明确且严格为取数客户端（Data Source Client）**：
- Peerdrive 作为 WebDAV 客户端连接到用户配置的外部 WebDAV 服务（例如 Nextcloud、Alist、Seafile、坚果云、InfiniCLOUD 等）。
- 将外部 WebDAV 中的文件作为 Peerdrive 的后端取数来源之一，注册至 `back/internal/source` 中的 `Manager`。
- **不实现 WebDAV 服务端**（避免引入过多的协议维护负担与安全攻击面）。

---

## 2. 接入模型评估：索引型 vs 摄取型

外部 WebDAV 资源接入存在两种典型模型：

| 评估维度 | ① 索引型（推荐首选） | ② 摄取型（按需扩展） |
|---|---|---|
| **核心机制** | PROPFIND 遍历远程目录，拉取并流式计算 SHA-256 建立 `hash → webdav_path` 映射；数据保留在远端，按需流式拉取 | 在索引基础上，将远程文件全量下载落盘至本地存储 / CAS 存储区，登记入 `file_index` |
| **存储开销** | 极低（仅占几 KB ~ 几 MB 内存与 SQLite 索引） | 高（受限于本地磁盘容量，产生重复占用） |
| **时效与离线** | 强依赖远程 WebDAV 服务的连通性与带宽 | 本地持久化，完全支持离线和 P2P 转发 |
| **适用场景** | 外部网盘拥有海量媒体、备份文件，本地空间有限 | 关键文件归档、离线备份、热点文件预热 |
| **开发复杂度** | 中（需要解决 Range 分片与索引同步问题） | 低（在索引型完成后只需调用现成落盘管线） |

**结论**：
**阶段一优先实现「① 索引型数据源」**，作为 `source.Source` 接口的标准实现；**阶段二在管理面增加「保存到本地网盘」的摄取能力**，复用既有的 `PeerPuller` / `FileIndexService` 流式落盘与校验链路。

---

## 3. Go 生态与库能力实测分析

### 3.1 库选型
- `golang.org/x/net/webdav`：仅包含服务端（`Handler`、文件系统接口与 HTTP 动词分发），**不提供客户端实现**，无法直接用于数据源。
- `emersion/go-webdav` (v0.7.0)：提供完整的纯 Go WebDAV 客户端（`client.go`），支持 Basic Auth、PROPFIND、GET、PUT 等，依赖少且质量可靠。

### 3.2 深度递归 `Client.ReadDir(recursive=true)` 在真实服务上的表现
`emersion/go-webdav` 的 `ReadDir(ctx, path, recursive)` 通过发送带有 `Depth: infinity` 的 `PROPFIND` 请求实现单次递归：
1. **服务端兼容性限制**：
   - Nextcloud、ownCloud、Apache mod_dav 在默认配置下通常允许 `Depth: infinity`。
   - 但是多数公有云网盘、企业网盘和反向代理（如 Alist 某些驱动、坚果云、Nginx 限制层）为了防止 DoS，**禁止或忽略 `Depth: infinity`**，会返回 `HTTP 403 Forbidden`、`HTTP 400 Bad Request`，或者仅返回深度为 1 的条目。
2. **条目数量与超时限制**：
   - 远程目录若包含数万级文件，单个 `Depth: infinity` 响应体积达数十兆 XML，解析缓慢且容易触发客户端或代理的读取超时。
3. **技术方案**：
   - 爬取器应当采用**两段式策略**：先尝试 `ReadDir(ctx, path, true)`；如果服务端返回 400/403/405 或超时，平滑退化为类似 `openlist_crawler.go` 的逐层深度优先/广度优先扫描（`Depth: 1` 递归）。

### 3.3 `Client.Open` 缺少 Range 支持的瓶颈与薄客户端方案
`emersion/go-webdav` 的 `Client.Open(ctx, name)` 返回 `io.ReadCloser`，底层直接发裸 `GET`，无 `offset` / `size` 参数，不支持发送 `Range: bytes=start-end` HTTP 头。

1. **后果分析**：
   - Peerdrive 的 `Source.Open(ctx, hash, offset, size)` 要求支持分片流读取。如果分片请求无法下推为 HTTP Range，客户端每次请求特定分片（如媒体拖拽播放、多线程分块下载）都只能从远程全量拉取后在内存截取，造成巨大的下行带宽浪费。
2. **解决方案：自研薄客户端封装**：
   - WebDAV 本质上是标准 HTTP/1.1 之上的协议扩展（RFC 4918）。
   - `emersion/go-webdav` 主要负责处理 PROPFIND 的 XML 序列化与解析（这一步相对繁琐，值得复用）。
   - 对于文件内容读取，可以直接基于标准库 `http.Client` 包装支持 Range 的 `OpenRange(ctx, path, offset, size)`：
   ```go
   req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
   if size > 0 {
       req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+size-1))
   } else if offset > 0 {
       req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
   }
   ```
   - 此封装仅需约 50 行代码，即可赋予 WebDAV 源完整的 `CapStream | CapRange` 能力。

---

## 4. 远程目录爬虫通用化与 OpenList 爬虫接线治理

### 4.1 现状与教训
`back/internal/source/openlist_crawler.go` 已经实现了完整的并发爬取、流式哈希计算与热重载逻辑（PR #135），但目前全仓除测试外没有任何外部调用方，沦为「已实现但未接线」的代码。

### 4.2 统一抽象：`RemoteDirectoryCrawler`
应当提炼通用的远程目录同步模型，避免为每种外部源（OpenList、WebDAV、未来的 SMB）各写一套胶水层：

```go
type RemoteCrawler interface {
    // Crawl 遍历远程目录树并计算/提取 sha256 映射
    Crawl(ctx context.Context, root string) (map[string]string, error)
}
```

- **OpenListCrawler**：实现 `listFS`（REST API）+ `fetchFileHash`。
- **WebDAVCrawler**：实现 `PROPFIND`（XML/Depth）+ `fetchFileHash`（标准 GET 流式计算）。

### 4.3 接线机制（Admin 端点与配置自启动）
为了彻底解决爬虫未接线的问题，建立两级触发机制：
1. **启动时自动播种**：
   - 若检测到环境变量 `PEERDRIVE_WEBDAV_URL`（及 `PEERDRIVE_OPENLIST_URL`），在后台 goroutine 中自动启动初始爬取任务，构建并在完成后热加载入 `Manager`。
2. **管理面动态刷新端点**：
   - 增加管理面 WS/HTTP 请求（仅限本地 admin）：
     - `POST /admin/sources/webdav/sync`
     - `POST /admin/sources/openlist/sync`
   - 允许运维在外部网盘文件变动时按需触发重新扫描。

---

## 5. 安全设计与凭据隔离

1. **外部元数据不可信**：
   - WebDAV 响应中的 `getetag`、`getcontentlength`、自定义 hash 属性各服务实现差异极大，且存在伪造可能。
   - **SHA-256 必须在 Peerdrive 端边流式读取边计算**，计算出的校验和作为唯一权威哈希。
2. **认证凭据存储**：
   - 用户名与密码（或 Token）支持 `PEERDRIVE_WEBDAV_USER` / `PEERDRIVE_WEBDAV_PASS` 环境变量注入。
   - 若后续支持多 WebDAV 挂载，凭据只保存在受保护的本地配置存储中，禁止通过 WebRTC / 普通 API 泄露。
3. **只读约束与权限控制**：
   - WebDAV 数据源在 Peerdrive 内部为严格只读（`Read-Only`），不暴露远程写接口。
   - 外部 WebDAV 文件的访问权限由 Peerdrive 自身的 `NodeShare` 共享范围与权限系统（见 #111）重新统一定义，不继承源站复杂的 ACL。

---

## 6. 实施路线图

1. **阶段 1：薄客户端与数据源适配器（本阶段落地）**
   - 实现轻量 `WebDAVSource`（支持 Basic Auth 与 Range GET）。
   - 规范化 `source.Source` 中的 `Type()` 分类。
2. **阶段 2：目录爬虫与管理面接线**
   - 统一 OpenList 与 WebDAV 爬取器接线，支持配置注入与 Admin API 手动触发同步。
3. **阶段 3：前端展示与摄取集成**
   - 在前端管理台展示 WebDAV 数据源状态与健康度，提供一键摄取（保存到本地）入口。
