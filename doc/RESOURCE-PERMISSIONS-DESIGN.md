# 文件与资源访问权限系统设计规范 (Resource Permissions Specification)

> 对应 Issue: #111  
> 状态: 架构规范与数据模型确立（按用户定序：访问权限为精细化模型，兼容现有三级可见性）  
> 关联 Issue: #89 (5 模式投影), #116 (跨形态访问测试), #121 (Metadata 内容寻址), #115 (架构总纲 - 安全切面)

---

## 1. 核心变革：从「枚举开关」升级到「权限三元矩阵」

### 1.1 现状缺陷剖析
当前 Peerdrive 资源的访问控制停留在粗粒度的单枚举状态：
```go
// back/internal/model/collection.go
Visibility string   // public | unlisted | private
AccessList []string // 用户名白名单
```
导致三个不可逾越的瓶颈：
1. **单一动作困境**：仅支持 `CanView` 判定。「谁能提交（Write）」、「谁能浏览元数据（Browse）」、「谁能修改配置（Manage）」全部被写死绑定在 Owner 单一身份上，无法支持协作提交。
2. **主体形态单一**：`AccessList` 仅支持本地系统账号，无法表达「授权特定对端 PeerID」或「持有特定凭据 Key 可读取」。
3. **资源粒度受限**：仅支持集合级生效，无法针对 Collection 内部的单个 `Entry` 或特定子目录单独收紧权限。

### 1.2 权限三元组抽象：主体 $\times$ 动作 $\times$ 资源
访问控制升级为基于细粒度 ACL 的判定模型：

$$\text{Permission} = \langle \text{Subject}, \text{Action}, \text{Resource}, \text{Effect} \rangle$$

```text
┌─────────────────┐       ┌─────────────────┐       ┌─────────────────┐
│     Subject     │       │     Action      │       │    Resource     │
├─────────────────┤       ├─────────────────┤       ├─────────────────┤
│ user:<username> │  ──►  │ read   (下载正文)│  ──►  │ collection:<id> │
│ peer:<peerID>   │       │ browse (查看目录)│       │ entry:<path>    │
│ key:<sha256>    │       │ write  (追加提交)│       │ file:<sha256>   │
│ owner           │       │ manage (变更授权)│       │ *               │
│ * (匿名/所有人)  │       │ *      (所有动作)│       │                 │
└─────────────────┘       └─────────────────┘       └─────────────────┘
```

---

## 2. 数据结构规范 (Schema v1)

```go
type PermissionRule struct {
    // Subject 主体:
    //   "*"               - 任何人 (匿名/公开)
    //   "owner"           - 资源拥有者
    //   "user:<name>"     - 指定认证用户名
    //   "peer:<peerID>"   - 指定对端节点 ID (P2P 直连)
    //   "key:<sha256>"    - 持有特定密钥凭据者 (出示明文，匹配此哈希)
    Subject string `json:"subject"`

    // Actions 动作集: "read" | "browse" | "write" | "manage" | "*"
    Actions []string `json:"actions"`

    // Resource 资源作用域 (缺省表示继承当前对象):
    //   "" (当前合集根) | "entry:data/video.mp4" | "dir:photos/"
    Resource string `json:"resource,omitempty"`

    // Effect 效力: "allow" (默认) | "deny"
    Effect string `json:"effect,omitempty"`
}
```

---

## 3. #89 五种模式向权限矩阵的投影与兼容

既有的五种可见性与准入模式不是独立体系，而是权限三元组的预置快捷组合（Presets）：

| #89 模式 | 传统配置方式 | 权限矩阵等价表达 | 兼容机制 |
|---|---|---|---|
| **`public`** | `visibility="public"` | `[{Subject: "*", Actions: ["read", "browse"]}]` | 读 `Visibility` 自动合成为该规则 |
| **`unlisted`** | `visibility="unlisted"` | `[{Subject: "*", Actions: ["read"]}]`（禁止公共索引广播，知道地址可读） | 维持 `Visibility` 作为展示别名 |
| **`private`** | `visibility="private"` | `[{Subject: "owner", Actions: ["*"]}]` | 默认最严安全边界 |
| **用户白名单** | `AccessList=["u1", "u2"]` | `[{Subject: "user:u1", Actions: ["read"]}, {Subject: "user:u2", Actions: ["read"]}]` | 双写双读平滑迁移 |
| **带秘钥访问** | 外部传参出示 Token | `[{Subject: "key:" + SHA256(secret), Actions: ["read"]}]` | 资源级专属凭据 |

---

## 4. 资源级凭据 (Resource Key) vs 节点级 PSK 的严格解耦

必须严格划分两层概念，禁止职责漂移：

| 维度 | 节点预共享密钥 (`PEERDRIVE_PSK`) | 资源级凭据密钥 (`Permission Key`) |
|---|---|---|
| **作用层级** | L4/L7 传输准入层（网络互联） | L7 业务应用层（文件/资源读取） |
| **防御目标** | 阻断未授权节点发起任何 WebRTC / WS 握手 | 保护特定敏感合集或条目即使被拉取也需凭据解密/授权 |
| **管理主体** | 节点运维人员（环境变量 / 节点配置） | 内容所有者（写在 Collection JSON 的 Permission 内） |
| **密钥生命周期** | 长期单机静态密钥 | 按合集、按分享链接随时生成与废弃 |
| **比对机制** | 握手包出示明文比对 | 客户端提供明文，服务端执行 `SHA256(key)` 比对规则哈希 |

---

## 5. 存储位置与 CAS 不可变性

遵循 Peerdrive 既有经过严格单测（`anon_visibility_test.go`）锁定的核心原则：
1. **权限参与内容哈希**：
   - 权限规则作为 `Permissions []PermissionRule` 写入 Collection JSON 原文；
   - 更改权限将产生全新的 Collection Hash，新旧权限与版本链（`rollback`）共同存在于 CAS 存储中；
   - 绝不外挂于关系型数据库旁路表，确保离线分发和多节点镜像同步时权限不脱节。
2. **双读与向前兼容**：
   - 当读取旧 Collection JSON 时，若 `Permissions` 数组为空，自动将 `Visibility` 与 `AccessList` 升维计算为等价规则；
   - 向管理端输出时，继续提供 `Visibility` 辅助字段，保障旧前端面板零回归。

---

## 6. 实施演进规划

1. **阶段 1：核心模型与兼容计算器（本阶段落地规范）**：
   - 在 `model/collection.go` 定义 `PermissionRule` 与 `CheckPermission(subject, action, resource)` 计算器。
2. **阶段 2：Service 鉴权改造与端点对接**：
   - 改造 `CanView` 为多动作判定 `Can(ctx, action, resource)`；
   - 支持 HTTP `X-Resource-Key` 头与 WebRTC 帧载荷凭据提取；
   - 为公开路由（如 `/iwara/video/:id` 等）挂载规则级访问保护。
3. **阶段 3：前端权限配置面板**：
   - 在合集详情页提供「公开 / 密码共享 / 节点授权 / 协同编辑」可视化配置界面。
