# 多 SQLite 库（peerdrive.db 与 reg.db）收敛评估与架构治理

> 对应 Issue: #154  
> 状态: 评估完成，确立架构决策  
> 关联 Issue: #90 / #137 (基础设施剥离), #139 (单库可见性), #151 (Schema 版本跟踪), #152 (环境变量统一)

---

## 1. 现状盘点与双库特征

目前 Peerdrive 后端维护了两套物理隔离的 SQLite 数据库：

| 维度 | 主节点库 (`peerdrive.db`) | 注册中心库 (`reg.db`) |
|---|---|---|
| **物理位置** | `internal/repository/` | `internal/regserver/` |
| **默认路径** | `./peerdrive.db` (`PEERDRIVE_DB_PATH`) | `<storageDir>/reg.db` (`PEERDRIVE_REG_DB`) |
| **表数量** | 13 张业务表 | 2 张基础设施表 (`users`, `relay_nodes`) |
| **索引数量** | 5 个 (集中于 `file_index` / `file_providers` / `sha_tags`) | 0 个 |
| **生命周期** | 进程级单例句柄 (`repository.GetDB()`) | 实例级句柄，挂载在 `*regserver.Server` 上 |
| **依赖驱动** | 双驱动支持 (`-tags nosqlite` 切换 pure Go) | 双驱动支持 (`driver_cgo.go` / `driver_pure.go`) |
| **版本跟踪** | 0 个 `PRAGMA user_version` (内嵌 ALTER TABLE) | 0 个 `PRAGMA user_version` |

---

## 2. 核心架构矛盾与合并可行性分析

### 2.1 数据模型与跨库依赖分析
- **领域职责异构**：
  - `peerdrive.db` 保存的是去中心化内容节点的数据（文件 CAS 元数据、合集、本地索引、SHA 标签等）。节点核心哲学强调 **前 6 阶段无需账号系统（纯 peerId 通信）**。
  - `reg.db` 保存的是中心化配套设施数据（注册用户鉴权账号、bcrypt 凭据、P2P 中继列表）。
- **零关联与零跨库事务**：
  - 两者之间**没有任何外键关联**，不存在任何跨库事务或 JOIN 查询需求。
  - 普通自托管边缘节点通常将 `PEERDRIVE_REG_SERVER` 指向云端中心服务器，本地完全不需要初始化或写入 `reg.db`。

### 2.2 与基础设施剥离路线图 (#90 / #137) 的冲突
- 根据 `doc/INFRASTRUCTURE.md`（PR #137）定案：
  - `regserver` 属于中心服务器角色，已制定了从 `back/` 主模块剥离为独立模块/二进制的演进路线（Phase 2-3）。
- **合并的严重反模式**：
  - 若此时将 `reg.db` 的 `users` 和 `relay_nodes` 硬编码合入 `peerdrive.db`，等于在物理层面将边缘节点数据与中心服务数据强耦合。
  - 后续执行 #90 剥离 `regserver` 时，将不得不再次对已合并的数据库做二次拆分，造成二次数据迁移风险。

---

## 3. 明确结论与架构决策

### 决策结论：**维持物理隔离（不合库），实施「统一管理保障」机制**

明确**不将两库合并为一个单一库文件**，理由如下：
1. **拓扑解耦**：边缘节点（纯消费或文件节点）保持轻量，数据库不被无用的用户鉴权表污染；中心节点（regserver）可按需挂载独立的持久卷。
2. **安全隔离**：用户账号与密码凭据（bcrypt 哈希）与文件元数据物理隔离，防止误分享或备份泄露。
3. **架构正交**：完全契合 #90 基础设施服务外移的大方向。

---

## 4. 「不合并但补保障」的实施方案

针对当前双库缺乏统筹管理引发的维护痛点，落地以下三项统一保障：

### 4.1 统一生命周期与退出编排（`peerdrive all` 模式）
在 `peerdrive all`（同进程运行主服务与注册服务）退出时，确保数据库有序平稳关闭，防止 Windows 下出现文件锁死与删除失败：
- **关闭顺序**：
  1. 停止入站 HTTP / WebSocket 监听（Drain connections）；
  2. 关闭 `regserver.Server` 句柄（`s.Close()` 执行 `s.db.Close()`）；
  3. 关闭主库连接（`repository.CloseDB()`）。
- 在 `services.go` 退出信号处理中统一注册 Cleanup 链。

### 4.2 统一备份与恢复策略
- **边缘节点备份**：只需备份单一文件：
  ```bash
  sqlite3 peerdrive.db ".backup 'backup-peerdrive-$(date +%F).db'"
  ```
- **注册/运营中枢备份**：
  ```bash
  sqlite3 peerdrive.db ".backup 'backup-peerdrive-$(date +%F).db'"
  sqlite3 storage/reg.db ".backup 'backup-reg-$(date +%F).db'"
  ```
- 在运维文档（`doc/guide/OPERATOR.md`）明确注明两库角色分工，避免管理员遗漏 `reg.db`。

### 4.3 独立的 Schema 版本演进 (#151 配套)
- 两库各自独立维护 `PRAGMA user_version` 版本号。
- `peerdrive.db` 设立专用的 migration 表或版本游标；`reg.db` 独立跟踪用户与中继表的迁移历史，互不越界。
