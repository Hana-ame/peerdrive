# SAMBA (SMB) 协议接口可行性与形态选型评估报告

> 对应 Issue: #163  
> 状态: 调研评估完成，决策明确  
> 关联 Issue: #165 (WebDAV 数据源), #106 (OpenList 数据源), #88 (访问形态分层), #89 (节点准入防匿名), #90 (基础设施层外移), #111 (资源访问权限), #115 (架构划分总纲), #121 (metadata 内容寻址化)

---

## 1. 核心结论摘要

1. **形态选择**：
   - **形态 A（SMB 服务端）严格否决**：纯 Go 生态缺乏生产级 SMB2/3 服务端实现；自研协议复杂度极高（ASN.1/SPNEGO/NTLM/租约/安全描述符）；引入 CGo/外部 smbd 破坏单二进制与跨平台编译硬约束；445 端口特权与客户端不可控连接给单进程服务带来巨大风险。
   - **形态 B（SMB 客户端 / 取数数据源）高度可行**：纯 Go 客户端库（`github.com/hirochachacha/go-smb2`）成熟稳定，天然提供 `io/fs.FS` 与带 Seek 的随机分片读取；作为 `source.Source` 接入成本可控（约 200 行胶水代码）。
2. **与 WebDAV 的主次与落地顺序**：
   - **若需提供外部挂载服务端**：WebDAV 服务端（基于 HTTP、生态成熟、无特权端口限制）是唯一现实路径，应优先于任何 SMB 方案。
   - **若作为数据源客户端**：WebDAV 数据源（#165）与 SMB 数据源（形态 B）同属「外挂取数源」，架构上对齐并复用相同的通用远程目录爬取与索引同步基础设施。

---

## 2. 三种候选形态深度对比

| 评估维度 | 形态 A：SMB 服务端 | 形态 B：SMB 客户端（数据源） | 形态 C：双向（A + B） |
|---|---|---|---|
| **定位** | Peerdrive 作为 SMB 服务端供 Windows/Mac 挂载 | Peerdrive 作为客户端挂载外部 NAS/共享，按 SHA-256 取数 | 同时支持服务端与客户端 |
| **Go 生态库支持** | ❌ 无可用纯 Go 服务端库 | ✅ `hirochachacha/go-smb2` (纯 Go, SMB2/3) | ❌ 卡在服务端库缺失 |
| **单二进制 / 跨平台兼容** | ❌ 若包 CGo/smbd 则破坏 5 平台交叉构建 | ✅ 100% 纯 Go，零 CGo，完美契合 `-tags nosqlite` | ❌ 破坏跨平台 |
| **端口与权限限制** | 必须监听 TCP 445（Linux/macOS 下需要 root 绑定或 iptables 转发） | 客户端向外出网连接，无特权端口限制 | 同样受 445 特权限制 |
| **协议复杂度** | 极高（Negotiate, SessionSetup, TreeConnect, NTLM/Kerberos, ACL, 租约） | 中低（库已封装全部协议帧，暴露标准 `io/fs`） | 极高 |
| **与既有架构契合度** | 低（与 WebRTC/HTTP 面板形态割裂，引入大量不可控 OS 客户端行为） | 高（直接实现 `source.Source`，与 `OpenListSource`、`WebDAVSource` 同级） | 低 |
| **建议结论** | **坚决否决** | **推荐采纳（作为 Phase 2 外挂数据源）** | **否决** |

---

## 3. 形态 A（SMB 服务端）可行性深度剖析

### 3.1 纯 Go SMB2 服务端生态调查
在 Go 开源生态中：
- 目前 GitHub 与 pkg.go.dev 上所有活跃维护的 SMB 库（如 `hirochachacha/go-smb2`、`cloudsoda/go-smb2`、`stacktitan/smb`）**全部仅包含 Client 实现**。
- 极少量的实验性 Server 项目（如个人原型实验库）仅支持残缺的 SMBv1（已被现代 Windows/macOS 默认禁用且存在重大安全漏洞）或无法通过现代 Windows 客户端安全策略校验。

### 3.2 自行实现 SMB2 最小协议子集的代价
若尝试自研一个「最小只读 SMB2 服务端」：
1. **握手流程**：
   - NetBIOS Session Service 封装（4 字节长度前缀）。
   - SMB2 Negotiate（协议方言协商，需支持 SMB 2.1 / 3.0）。
   - SMB2 Session Setup（需在纯 Go 中手写 SPNEGO / GSS-API 封装，并处理 NTLMSSP 质询响应逻辑）。
   - SMB2 Tree Connect（共享名挂载协商）。
2. **操作与属性**：
   - SMB2 Create（处理各种 DesiredAccess、FileAttributes、ShareAccess、CreateDisposition 与 EA 扩展属性）。
   - SMB2 Query Directory（返回 FileDirectoryInformation 二进制结构，包含精确的 Windows FileTime 时间戳）。
   - SMB2 Read / Close。
3. **复合请求与并发压力**：
   - 现代 Windows Explorer 在打开一个目录时，会并发发送大量 Compounded Requests（复合请求）以及 OpLocks/Lease 申请。一旦协议处理不严谨，客户端将直接断开连接或发生资源管理器卡死。
4. **结论**：自研代码量预计将超过 8,000 ~ 12,000 行，且安全与兼容性隐患极大，违背轻量单二进制的设计哲学。

### 3.3 外部 smbd 进程封装的硬冲突
若通过启动外部 Samba `smbd` 进程或调用 C 库：
- `.github/workflows/go-build.yml` 定义了 5 平台（Linux amd64/arm64、Windows amd64、macOS amd64/arm64）的交叉编译矩阵，采用 `CGO_ENABLED=0` 保证可移植性。
- 依赖宿主机环境（如要求系统预装 Samba）会导致 Docker 镜像膨胀、Windows/Termux 环境部署极度脆弱。

---

## 4. 形态 B（SMB 客户端 / 数据源）架构设计

### 4.1 技术栈与接口契约
- **选用库**：`github.com/hirochachacha/go-smb2`
  - 纯 Go 实现，无 CGo 依赖。
  - 完整实现 SMB 2.0.2 ~ 3.1.1 协议。
  - 支持 `fs.FS` 接口，提供 `ReadDir`、`Stat`、`Open` 等标准操作。
- **与 `source.Source` 契约匹配**：
  - 能力声明：`CapStream | CapRange`。
  - 由于 SMB 的 `smb2.File` 实现了 `io.ReadSeekCloser`，支持任意 `Seek`，分片请求（`offset > 0`、`size > 0`）可以直接在 SMB 协议层高效读取指定偏移范围，**相比 HTTP WebDAV 的 Range 下推更加天然和高效**。

### 4.2 接入实现路径
1. **配置项**：
   - `PEERDRIVE_SMB_SERVER` (host:port, 默认 445)
   - `PEERDRIVE_SMB_SHARE` (共享名)
   - `PEERDRIVE_SMB_USER` / `PEERDRIVE_SMB_PASS` / `PEERDRIVE_SMB_DOMAIN`
2. **目录爬取与 SHA-256 索引构建**：
   - 复用与 OpenList / WebDAV 协同的通用远程爬取器抽象 `RemoteDirectoryCrawler`。
   - 遍历 SMB 共享目录树，流式计算各文件的 SHA-256 建立 `hash → smb_rel_path` 索引表。
   - 注册为 `SMBSource`（`Type() == "smb"`），优先级按配置可调。

---

## 5. 与 WebDAV 的关系与演进路线

### 5.1 共享协议服务端需求
如后续需要允许外部原生文件管理器挂载 Peerdrive 集合：
- **强烈推荐走 WebDAV 服务端**（对应 `doc/guide/API-USAGE.md §9` 已预留的端点设计）。
- WebDAV 天然运行在 80/443 或 Peerdrive 自带的 HTTP 端口（3000）上，反向代理友好，无需特权端口。
- Go 官方扩展库 `golang.org/x/net/webdav` 或 `emersion/go-webdav` 提供了现成健壮的服务端支持。

### 5.2 实施步骤规划

```text
[当前] 明确形态选型：否决 SMB 服务端，确认 SMB 客户端数据源可行性
  │
  ├──> [优先] 完善与接线 WebDAV 数据源 (#165) 及通用爬取器
  │
  ├──> [按需] 接入 SMB 数据源 (基于 go-smb2，复用通用爬取器)
  │
  └──> [远期规划] 若有外部网络盘挂载诉求，实现 HTTP WebDAV 服务端，拒绝 SMB 服务端
```
