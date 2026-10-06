# 节点标识与用户归属：两层架构（DNS 类比）

> 日期：2026-10-06
> 结论先行：**这个原则是对的，而且仓库已经按它存着数据了**——`users` 表和
> `relay_nodes` 表就是两个独立的层。缺的不是分层，是**层之间的绑定表**，以及
> 「凭什么你能说这个节点是你的」的**凭证机制**。
>
> ⚠️ 本文档里 §4 起的 schema 和 API 是**设计提案，尚未实现**。
> 已实现的部分都在 §2，每一处都标了文件:行号。

---

## 1. 原则的表述

分两层：

- **第一层：节点目录**。节点只有自己的标识符（`peer_id`），加上它的运行属性。
  一个节点可以没有任何人认领，照样能被查到。
- **第二层：归属记录**。用户可以声明「我拥有哪些节点」。这是一条独立、可变、
  可撤销的记录。

对应 DNS：

| DNS | 这里 |
|---|---|
| 域名 → 地址记录（A/AAAA） | `peer_id` → `addrs` |
| 区域解析（谁都能查） | `/relay/nodes`、`/discover/nodes` |
| 域名注册商 / whois 里的持有人 | `users` + 归属绑定 |
| DNSSEC / 注册商的写入权限 | 归属声明的凭证 |

**关键的性质是：解析和归属相互独立，所以两者可以各自失败。** 这一点决定了
整个架构能不能表达「我已经买了但还没上架」。

---

## 2. 现状：两层已经存在，只是没有连起来

`back/internal/regserver/regserver.go` 的 `openDB()`（189 行起）建了**两张独立
的表**：

```sql
CREATE TABLE IF NOT EXISTS users (
    id, username UNIQUE, password_hash, role, created_at
)
CREATE TABLE IF NOT EXISTS relay_nodes (
    peer_id PRIMARY KEY, addrs, storage_mb, load_pct, version,
    registered_at, last_heartbeat
)
```

- `relay_nodes` **没有 owner 列**——这是对的，第一层不该知道归属。
- `users` **没有任何指向节点的列**——也是对的，第二层不该知道运行属性。
- **两张表之间没有外键、没有 join 表。** 所以今天你无法回答：
  「这个用户有哪些节点？」和「这个节点是谁的？」

也就是说，**分层已经做对了，只是缺一条边。**

节点注册接口（`relayRegister`，325 行）也印证了这一点——它只要求 `peer_id`，
其余全是节点的自我描述，不涉及任何人：

```go
if strings.TrimSpace(req.PeerID) == "" {
    writeErr(w, http.StatusBadRequest, "peer_id is required")
    return
}
```

而用户子系统是另起一套的（`newToken` 82 行 / `authRegister` 239 行 /
`authLogin` 268 行），发的是自己的 token，和节点目录没有交集。

### 违反原则的一处

`back/internal/model/peer.go` 的 `PeerInfo` 里有：

```go
RegVerified  bool   `json:"reg_verified"`
RegUsername  string `json:"reg_username,omitempty"`
```

这是把归属**焊在了传输层连接元数据上**。后果：归属信息只在「节点当前正连着、
且连接时说了自己是谁」的时候存在。节点一断线，归属就没了——
也就是**「已认领但离线」这个状态根本无法表达**。

对照 DNS：域名不会因为 A 记录暂时不可达就从注册商那里消失。归属应该是
注册表里的持久事实，不是连接时的自报。

---

## 3. 这个类比有一个断点，必须补上

DNS 之所以叫它"归属"而不是"标签"，是因为**你不能靠说就拥有域名**——
注册商是信任锚点，域名转移必须走它改 zone signing key。

对应到这里，问题就是：**节点注册时凭什么证明它属于这个用户？**

现状是「凭谁都可以说」。一个节点只要知道某个用户名，就能声称自己是那个
用户的节点——`relay_register` 完全没校验。所以归属记录会退化成一个**可伪造的
装饰标签**，第一层的可信度被第二层污染。

要成立，必须有一个只有真所有者能提供的东西。三种方案：

| 方案 | 机制 | 成本 | 强度 |
|---|---|---|---|
| A 信任制 | 用户声明，服务端照记 | 最低 | 可伪造，本质是白名单 |
| B 节点共同签 | 服务端发一次性挑战，节点回签名 | 中 | 真绑定，需要节点常驻验签 |
| C 认领凭证（推荐） | 用户认证后服务端发 claim token，节点注册时出示 | 低 | 真绑定，一次性 |

C 对应 DNSSEC 的思路——**所有权靠持有私有材料证明，而不是靠声明**。

流程：

```
1. 用户 → 注册服务端：登录，换取 claim token（与 node 无关，仅绑定 user）
2. 用户 → 节点：把 claim token 交给节点（配置时写入，或走已建直连下发）
3. 节点 → 注册服务端：注册时带上 claim token
4. 服务端校验通过 → 写绑定（user, peer_id），token 一次性作废
```

关键属性：**token 不编码节点身份**，所以它无法用来冒充别的节点；
它只证明「这个用户授权了这个特定的 claim 动作」。

---

## 4. 建议的第三层：绑定表（提案，未实现）

```sql
CREATE TABLE IF NOT EXISTS node_ownership (
    peer_id      TEXT NOT NULL,
    username     TEXT NOT NULL,
    claimed_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (peer_id, username),
    FOREIGN KEY (peer_id) REFERENCES relay_nodes(peer_id) ON DELETE CASCADE,
    FOREIGN KEY (username) REFERENCES users(username)     ON DELETE CASCADE
)
```

- 主键 `(peer_id, username)`：一个节点可以有多个声明人（共享/协作），
  一条绑定只记一次。
- `ON DELETE CASCADE`：节点下线或用户注销时自动清理，不留孤儿记录。
- 服务端**只校验存在性**，不校验"是不是所有者"——
  因为归属本来就是可转让的，转让就是插入新行、删除旧行。

配套 API（提案）：

| 端点 | 语义 |
|---|---|
| `GET  /relay/nodes/{peer_id}` | 查节点（第一层，任何人可查，不含归属） |
| `GET  /me/nodes` | 查我拥有的节点（第二层，需认证） |
| `POST /me/nodes` `{peer_id, claim_token}` | 声明归属（需 claim token） |
| `DELETE /me/nodes/{peer_id}` | 撤销归属（需认证） |

`GET /relay/nodes/{peer_id}` **刻意不返回归属信息**——第一层保持公开可查，
归属是第二层的私有事实。这和「DNS 任何人都能解析，但 whois 是另一回事」一致。

---

## 5. 这套分层买到了什么

分层后，四种状态各自可表达、可查询——当前架构只表达得出一半：

| 状态 | 当前 | 分层后 |
|---|---|---|
| 上线但无人认领 | ✅ | ✅ |
| 已认领 | ⚠️ 仅在连接存活时 | ✅ 持久 |
| **已认领但离线** | ❌ 无法表达 | ✅ |
| **已认领但被撤销** | ❌ 无法表达 | ✅ |

后两行是实打实的缺口：「我昨天买了节点，今天它离线了，我还算拥有它吗」
——当前答不上来。

---

## 6. 代价与迁移

分层不是免费的：

1. **默认更诚实，也更冷清。** 没认领的节点不会出现在任何人的「我的节点」列表里。
   第一层公开，第二层安静——这是设计意图，不是缺陷。
2. **存量节点需要一次迁移。** 已经在线但从未声明归属的节点，
   需要决定默认值：
   - 选项 1：默认无归属，用户首次登录后逐个认领（干净，工作量大）
   - 选项 2：`relayRegister` 时若带 `claim_token` 则自动绑定，否则视为无归属
   （渐进，推荐）
   - 选项 3：首次启动时节点自认领到预共享 secret 对应的用户（零配置，
     但引入了"这个 secret 是谁的"这个循环问题）

3. **`PeerInfo.RegUsername` 应该删掉或降级。** 它是违反这条原则的地方。
   迁移路径：让 `RegUsername` 只从绑定表回填，不再由连接自报。

---

## 7. 推翻本文的判据

按知识库纪律，写下判错路径：

1. **如果归属只是给前端做 UI 过滤，不参与任何授权决策** —— 那 §3 的凭证机制
   是过度设计，用方案 A（信任制）就够了。判据：翻 `regserver.go` 的
   `authRequired`（152 行）看有没有用归属做访问控制。目前**没有**，
   所以今天是过度设计，§3 属于"先备好"。
2. **如果实际只需要「一个用户一个节点」** —— 那 join 表退化成
   `relay_nodes.owner_username` 单列，主键约束就够了，§4 的复合主键是多余的。
3. **如果节点的归属永远不会转让/撤销** —— `DELETE` 端点和 `ON DELETE CASCADE`
   都不需要，绑定变成永久事实，退化成一个只追加的日志表。

第 3 条最可能被证伪：多数节点大概是「买了就一直是我的」，
那 §4 简化成只追加的行更贴合。但先按可撤销建模，
因为"不可撤销"是"可撤销 + 决定不用撤销"，反向改造成本高得多。
