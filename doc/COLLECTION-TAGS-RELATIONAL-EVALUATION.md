# 集合层 Tag 关系表迁移与 json_each 驱动可用性评估

> 对应 Issue: #157  
> 状态: 评估完成，确立落地路线  
> 关联 Issue: #91 / PR #120 (`sha_tags` 文件层实现), #139 (可见性治理), #151 (Schema 版本跟踪)

---

## 1. 现状与非对称性诊断

在当前 Peerdrive 仓库中，标签（Tag）存在两套完全不对称的存储与查询架构：

| 维度 | 文件层 (`sha_tags`) | 集合层 (`collections.tags`) |
|---|---|---|
| **物理表形态** | 独立规范化关系表 `sha_tags` | 扁平嵌入列 `collections.tags` |
| **存储数据类型** | 单一关系行 `(sha, tag)` 组合主键 | JSON 字符串数组（如 `["tag1", "tag2"]`） |
| **索引情况** | 双 B-tree 索引 (`idx_sha_tags_tag`, `idx_sha_tags_sha`) | **0 索引**，全表扫描 |
| **查询机制** | `SELECT sha FROM sha_tags WHERE tag = ?` ($O(\log N)$) | `WHERE tags LIKE '%"tag"%'` (全表字符串扫描) |
| **多标签查询** | `IN (?, ?)` 走索引交并集 | 多段 `AND tags LIKE ?` 拼接，多次全表遍历 |

### 1.1 核心问题
1. **LIKE 假命中与前缀通配不可索引**：`%"tag"%` 无法利用任何 B-Tree 索引；即使对 `tags` 列建文本索引也必定走全表扫描。
2. **两层设计割裂**：文件层通过 #91 PR #120 落地了高性能关系表，集合层仍然停留在原型期的 JSON 字符串模糊匹配。

---

## 2. 替代方案评估：`json_each()` 在双驱动下的可用性与瓶颈

### 2.1 双驱动可用性实测
- **cgo 驱动 (`mattn/go-sqlite3`)**：已集成现代 SQLite 源码，内置启用 JSON1 扩展。
- **pure-Go 驱动 (`modernc.org/sqlite`, `-tags nosqlite`)**：基于 SQLite 3.44+ C 语言转译生成，自 SQLite 3.38.0（2022年起）JSON1 已经成为核心内置功能，默认强制启用。
- **实测结论**：**两个驱动均天然支持 `json_each()` 函数**，无需外挂加载动态库。

### 2.2 `json_each()` 的致命短板
尽管 `json_each()` 能够将 `tags LIKE '%"tag"%'` 转换为合法的 JSON 元素等值匹配：
```sql
SELECT c.* FROM collections c, json_each(c.tags) je
WHERE je.value = 'target-tag';
```
但其存在致命的性能瓶颈：
- **无法走索引**：`json_each` 是表值函数（Table-Valued Function），针对每一行 `collections` 记录都需要在运行时动态反序列化解析 JSON 文本。
- **复杂度仍然是 $O(N)$**：随着集合量级增长，每次标签搜索都是全表反序列化扫描，CPU 开销甚至高于裸字符串 LIKE。
- **结论**：`json_each()` 仅解决了语法语义严谨性，**未解决性能与索引的核心诉求**。

---

## 3. 推荐方案：规范化关系表 `collection_tags`

完全复刻 `sha_tags` 的优秀模式，构建对称规范化关系表：

### 3.1 DDL 设计
```sql
CREATE TABLE IF NOT EXISTS collection_tags (
    collection_id INTEGER NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    tag           TEXT    NOT NULL,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (collection_id, tag)
);

CREATE INDEX IF NOT EXISTS idx_collection_tags_tag ON collection_tags(tag);
CREATE INDEX IF NOT EXISTS idx_collection_tags_collection ON collection_tags(collection_id);
```

### 3.2 三阶段平滑迁移路线图（无缝停机/零中断）

借助已落地的 #151 `schema_migrations` 与 `PRAGMA user_version` 体系：

1. **阶段 1：Schema 增加与历史回填 (Migration v8)**：
   - 在 `applyMigrations` 中注册 version 8：创建 `collection_tags` 及其索引。
   - 启动期扫描已有 `collections` 中非空的 `tags`，反序列化后 `INSERT OR IGNORE INTO collection_tags` 完成存量数据回填。
2. **阶段 2：应用层双写（Dual-Write）**：
   - 修改 `collection_repo.go`：在创建与更新集合（`CreateCollection`, `UpdateCollectionTags`）时，事务内同时写入 `collections.tags`（保证旧代码兼容）与 `collection_tags` 关系表。
3. **阶段 3：查询切换（Switch Read）**：
   - 将 `SearchCollectionsWithFilters` 中的标签查询改造为：
     ```sql
     JOIN collection_tags ct ON ct.collection_id = c.id AND ct.tag IN (...)
     ```
   - 彻底废弃 `tags LIKE '%"tag"%'`。
   - `collections.tags` 列降级为纯只读展示缓存。

---

## 4. 结论与执行建议

1. **不采纳纯 `json_each()` 方案**：其不能消除全表扫瓶颈，与已有 `sha_tags` 架构不对称。
2. **采纳 `collection_tags` 独立关系表方案**：与 `sha_tags` 实现对称，充分利用 SQLite B-Tree 联合主键与索引。
3. **分步执行**：在独立 PR 中先落地 Migration v8 与双写，确保数据零丢失。
