# Metadata 内容寻址化与跨节点传输合并设计规范

> 对应 Issue: #121  
> 状态: 架构设计确立（按 Issue 边界：锁定数据模型与传输合并语义，为后续 PR 实施提供基准）  
> 关联 Issue: #70 (Entry 元数据字段), #91 (Tag 关系索引与搜索), #108 (Entry 多源消费), #115 (架构总纲)

---

## 1. 现状痛点与根本矛盾

在 Peerdrive 既有架构中，文件级元数据（Metadata）分散在三个互不相通的维度：
1. **SQLite `file_meta` 表**：
   - 记录 `hash, size, created_at, mime_type, gziped, filename, type, cid`。
   - **痛点**：属于单机持久化状态，不随 P2P 数据流传输。节点 B 从节点 A 拉取文件后，仅拉取到纯二进制内容，节点 A 上的文件名、原始创建时间、IPFS CID 等全部丢失，接收端必须重新探测 MIME 与 Size。
2. **Collection JSON 的 `Entry` 内嵌字段**：
   - 记录 `Name, MIME, CreatedAt, ModifiedAt, Size`。
   - **痛点**：粒度是合集条目而非文件本身；同一个 SHA 出现在多个合集中会存在冗余甚至冲突的元数据；修改条目名称会导致整个合集 JSON 的哈希发生变动。
3. **`twitterpic/index.json`**：
   - 独立的旁挂索引文件，同样无法跨节点同步。

**核心矛盾**：文件内容已经全面内容寻址化（CAS: `storage/{h[:2]}/{h}`），但元数据仍停留在本地关系表与合集内嵌，导致数据与元数据在流转中脱节。

---

## 2. 核心架构设计

### 2.1 Metadata 作为独立不可变对象入 CAS
将元数据文档本身作为独立的不可变 JSON 数据对象，按自身内容的 SHA-256 存入 CAS：
```text
storage/{m[:2]}/{m}
```
其中 $m = \text{SHA256}(\text{Metadata JSON bytes})$。

#### Metadata 文档规范（JSON Schema v1）
```json
{
  "schema": 1,
  "target_hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
  "intrinsic": {
    "size": 1048576,
    "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
    "mime_type": "video/mp4",
    "gziped": false
  },
  "extrinsic": {
    "filename": "demo_video.mp4",
    "cid": "QmXoypizjW3WknFiJnKLwHCnL72vedxjQkDDP1mXWo6uco",
    "type": "video",
    "created_at": 1712000000,
    "modified_at": 1712000500,
    "tags": ["tutorial", "p2p"]
  }
}
```

### 2.2 字段属性与可变性分层
- **内容内生字段（Intrinsic）**：由二进制内容唯一确定（`size`, `sha256`, `mime_type`, `gziped`），任意节点对于同一文件计算必定恒等，无需合并策略。
- **内容外生字段（Extrinsic）**：包含人类赋予或外部协议赋予的属性（`filename`, `cid`, `tags`, 时间戳）。
  - **语义契约**：遵循 CAS 既有心智模型——修改外生字段（例如重命名文件）即生成一个新的 Metadata 对象并产生新的 $m$ 哈希，新旧元数据在存储层共存。

### 2.3 零协议改动传输（Zero Protocol Changes）
现有的帧协议包括：
- `req`：按 Hash 请求字节流
- `meta`：仅为流式传输进度分母（`Total` 大小声明帧），**并非元数据载体**
- `data` / `done` / `err`

**关键决策**：**绝不修改现有 `meta` 协议帧**。
元数据对象是一个普通的 CAS 数据块。节点请求元数据时，直接发送 `{type: "req", hash: m}`，对端按普通文件拉取管线流式回传。`PeerSource`、`ShaSource` 及 WebRTC DataChannel 传输链路零改动。

---

## 3. 多节点 Metadata 合并算法（Field-Level Union）

当节点从多个对端或数据源汇聚到针对同一个 `target_hash` 的多个不同元数据对象时，采用**字段级并集（Field-Level Union）**策略：

```text
  节点 A (本地录入)              节点 B (IPFS 节点)
  {                             {
    filename: "video.mp4",        cid: "QmXoy...",
    created_at: 1000              tags: ["archive"]
  }                             }
                \                 /
                 ▼               ▼
          [ 字段级 Union 合并引擎 ]
                     │
                     ▼
  {
    filename: "video.mp4",
    cid: "QmXoy...",
    created_at: 1000,
    tags: ["archive"]
  }
```

1. **缺失字段互补（Complement）**：若一方缺失某外生属性（如 `cid` 为空），无条件继承另一方的有效值；
2. **冲突解决（Conflict Resolution）**：
   - 标量字段（如 `filename`）：以 `modified_at` 时间戳较大者为准；若时间戳相同，以字典序确定性胜出；
   - 集合字段（如 `tags`）：自动取并集（`Union`）去重；
3. **本地缓存与索引更新**：
   - 合并计算出的最新视图同步更新本地 SQLite `file_meta` 表，作为热路径查询缓存。

---

## 4. 与 Collection `Entry` 的解耦与向后兼容

### 4.1 引用化改造
Collection 的 `Entry` 增加 `meta_sha` 引用字段，不再强依赖扁平内嵌：
```go
type Entry struct {
    Hash       string   `json:"hash"`
    MetaSha    string   `json:"meta_sha,omitempty"` // 指向 CAS 中的 Metadata 对象
    // 以下内嵌字段作为兼容层保留
    Name       string   `json:"name,omitempty"`
    MIME       string   `json:"mime,omitempty"`
    Size       int64    `json:"size,omitempty"`
    CreatedAt  int64    `json:"created_at,omitempty"`
    ModifiedAt int64    `json:"modified_at,omitempty"`
}
```

### 4.2 向后兼容与双读机制
- **读取路径（Dual-Read）**：
  若 `MetaSha` 存在，优先从本地 CAS（或异步跨节点）读取并反序列化 Metadata 对象；若不存在或读取失败，平滑降级使用 `Entry` 内嵌的旧字段。
- **解耦效益**：
  多个 Collection 引用同一文件时，共享同一个 `MetaSha`。修改标签或名称仅需更新指向的新 `MetaSha`，避免了全量条目数据的膨胀与多源不一致。

---

## 5. 实施路线图

1. **Phase 1（规范与数据结构定义，本阶段完成）**：确立不可变 Metadata JSON Schema 与字段级合并契约。
2. **Phase 2（CAS 读写与本地表适配）**：在 `back/internal/repository` 增加 Metadata 对象写盘与读取方法，打通拉取后的自动挂载。
3. **Phase 3（Collection Entry 引用化与管理台对接）**：前端管理台支持元数据编辑与跨节点同步呈现。
