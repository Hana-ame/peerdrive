# SQLite WAL 模式启用评估

> 对应 Issue: #277  
> 状态: 评估完成，**建议启用**（已实施）  
> 关联 Issue: #151 (user_version 版本化迁移入口已就位), #154 (多 SQLite 库收敛评估)

---

## 0. 摘要

SQLite 默认使用 **DELETE** journal mode：写入时必须独占整库，
所有读者被迫等待写者释放锁。在 peerdrive 的"多节点访问 + 频繁上传 /
下载 / 同步"场景下，这是锁竞争的主要瓶颈。

**结论**：切换到 **WAL**（Write-Ahead Logging）journal mode，
并搭配 SQLite 官方推荐的 `synchronous=NORMAL`。

- 写吞吐提升：读者不再阻塞写者，写者也不再阻塞读者。
- 代价：额外 `-wal` / `-shm` 两个侧车文件（默认每 1000 页自动
  checkpoint 回主库，磁盘占用可控）。
- 兼容两个驱动（CGO `mattn/go-sqlite3` 与 pure-Go
  `modernc.org/sqlite`），通过 DSN 后缀驱动方言各写一份实现。
- 已覆盖生产迁移路径：旧库（DELETE 模式）在下次 `OpenDB` 时自动
  原地迁移到 WAL。

---

## 1. 现状：为什么 DELETE 是瓶颈

SQLite 的 DELETE journal mode（默认）用**热备份删除日志**实现事务：

1. 写事务开始前，整库被一个**排他锁**占住。
2. 期间的读者要么等锁释放，要么触发 SQLITE_BUSY。
3. 事务提交时，将主库快照写入日志，再一次性写回主库。

peerdrive 的写入来源高度并发（同一进程内）：

| 写入来源 | 触发频率 |
|---|---|
| HTTP 上传落盘后 `file_index` 登记 | 每次上传 |
| 分片上传完成回调 | 每次分片 |
| BT 完成回调（`p2p_bt`） | 每次 BT 下载 |
| `file_index` 游标增量同步 | 周期性 |
| 注册服务 `users` / `relay_nodes` 写入 | 心跳 + 登录 |
| 文件下载进度表 `download_progress` | 高频 |

读者也不少：Web 前端拉清单、跨节点 `share` 帧响应、`file_index` 查询、
`sha_tags` 查询。任何一个读者在 DELETE 模式下都可能撞上写锁。

当前已经在 DSN 后缀里设了 `busy_timeout=5000`，这是**排队等待**，
不是**并发执行**——写事务仍然串行，读者仍然要等。

---

## 2. WAL 是什么

WAL 把"写事务"从"阻塞整库"改成"追加到日志"：

1. 写事务开始时，只拿一个**写锁**（不阻塞读锁）。
2. 所有写操作追加到 `-wal` 侧车文件，不动主库。
3. 提交时 fsync 一下 WAL，写锁释放。
4. 读者看到主库 + WAL 的合并视图，无需等待。
5. 当 WAL 达到阈值（默认 1000 页 ≈ 4 MB），后台自动 checkpoint，
   把 WAL 内容合并回主库并截断 WAL。

**核心收益**：读写不再互相阻塞；多个读者可以同时读；写者之间仍串行
（SQLite 仍然只有一个写者），但读者不再拖累写者。

---

## 3. 兼容性评估

### 3.1 驱动支持

| 驱动 | 版本 | WAL 支持 |
|---|---|---|
| `mattn/go-sqlite3` (CGO) | v1.14.52 | ✅ 完整支持（DSN `_journal_mode=WAL`） |
| `modernc.org/sqlite` (pure-Go) | v1.60.1 | ✅ 完整支持（DSN `_pragma=journal_mode(WAL)`） |

**实测验证**（`back/internal/repository/db_wal_test.go`）：

- `TestJournalModeWAL`：`PRAGMA journal_mode` 返回 `"wal"`。
- `TestSynchronousNormalOnWAL`：`PRAGMA synchronous` 返回 `1`（NORMAL）。
- `TestJournalModePersistedAcrossOpen`：关闭重开后 WAL 持久。
- `TestLegacyDeleteDBMigratedToWAL`：旧 DELETE 库在 OpenDB 时原地迁移。

两个驱动均通过（分别用 `go test ./internal/repository/` 与
`go test -tags nosqlite ./internal/repository/` 各跑一遍）。

### 3.2 驱动差异（关键细节）

`mattn/go-sqlite3` 在解析到 `_journal_mode=WAL` 时，**会自动把
synchronous 提升为 NORMAL**（见其源码 `case "WAL": synchronousMode = "NORMAL"`）。
`modernc.org/sqlite` 没有这个自动提升。

因此本次改动在两个驱动的 DSN 里都**显式**写了
`_synchronous=NORMAL`，不依赖自动提升——保持行为一致，也防止将来
`mattn` 修改默认策略时静默漂移。

### 3.3 多进程访问的已知限制

WAL 依赖 **POSIX 共享内存**（`shm_open`）来协调 `-shm` 文件。
这意味着：

1. **网络文件系统（NFS / SMB / 9p）**：WAL 模式在 NFS 上**不可靠**，
   跨主机的 shm 协调可能失效。peerdrive 生产部署一律是**本地磁盘**
   （storage 根目录），不落在网络文件系统上——**不构成本项目的约束**。
2. **同主机多进程**：两个 peerdrive 进程指向同一个 `peerdrive.db`
   时，WAL 允许一个写者 + 任意多个读者并发。这反而比 DELETE 模式
   更适合"同机多节点"的调试场景。
3. **跨主机多进程**：WAL **不允许**跨主机共享同一个数据库文件。
   peerdrive 的多节点通过 P2P 互联，每个节点有自己独立的 `peerdrive.db`，
   不存在跨主机 SQLite 共享——**不适用**。
4. **`-shm` 文件权限**：SQLite 用 `shm_open` 创建，受 umask 影响。
   默认 0600 足够（同用户进程共享）。

**结论**：peerdrive 的部署形态（每节点一个本地 SQLite 文件 + P2P 互联）
完全在 WAL 的舒适区内。多进程限制**不影响本项目**。

### 3.4 设置时机：DB 初始化 vs 迁移

`PRAGMA journal_mode=WAL` 是**持久化**在数据库文件头部的——设置一次，
永久生效（除非显式改回 DELETE）。因此有两种可选策略：

| 策略 | 优点 | 缺点 |
|---|---|---|
| **A. 仅迁移层** | 一次性，干净 | 老库若从未跑过迁移则永远留在 DELETE |
| **B. 每次 OpenDB 确认** | 老库下次打开自动迁移；新库出生即 WAL | 每次 open 多一条 PRAGMA（成本 ≈ 微秒级） |

**本次采用 B**（DSN 后缀 + 每次连接确认）。理由：

1. 生产上已经存在 `peerdrive.db`（DELETE 模式）的节点。策略 A 要求
   显式跑一次迁移脚本，运维成本高；策略 B 零运维成本。
2. 每次连接的 `PRAGMA journal_mode=WAL` 对已经是 WAL 的库是 **no-op**
   （SQLite 内部直接返回当前值），成本可忽略。
3. 与现有的 `busy_timeout` / `foreign_keys` 走 DSN 后缀的既有模式
   保持一致（见 `db_driver_cgo.go` / `db_driver_pure.go` 的历史注释）。
4. `synchronous` 是 **per-connection**（不持久），**只能**通过 DSN
   后缀或每次连接 Exec 设置——与 B 策略天然契合。

迁移层（#151 引入的 `schema_migrations` + `PRAGMA user_version`）**不
需要**为此加新条目：WAL 是数据库级配置，不是 schema 变更。

### 3.5 WAL 文件生命周期

WAL 会创建两个侧车文件：

- `peerdrive.db-wal`：预写日志，追加写，大小随写流量增长。
- `peerdrive.db-shm`：共享内存映射，固定大小（默认 32 KB，最大可扩展）。

**自动 checkpoint**：SQLite 默认每写入 1000 页（≈ 4 MB）触发一次
**被动 checkpoint**（`wal_autocheckpoint=1000`）。被动 checkpoint 不
阻塞读者，但也不会等读者释放——未读完的页会留在 WAL 里。

**主动 checkpoint**（`PRAGMA wal_checkpoint(FULL)` 或 `TRUNCATE`）：
- 可以在进程关闭时手动调用，确保 WAL 完全合并回主库。
- 本次改动**没有**加这个，理由：
  - peerdrive 是长驻服务，重启频率低。
  - 自动 checkpoint 已经足够，WAL 文件不会无限增长。
  - 加主动 checkpoint 需要在 `CloseDB` 里加逻辑，而 `CloseDB` 当前
    语义是"仅测试用"，生产路径直接进程退出（OS 回收句柄）。

**磁盘占用上限**：在默认配置下，WAL 文件大小 ≈ 4 MB（一个自动
checkpoint 周期）。对于 peerdrive 的写入量级，这个量级完全可忽略。

**崩溃恢复**：进程崩溃后，下次 `sql.Open` 会自动跑 WAL 恢复——把
上次未 checkpoint 的 WAL 内容合并回主库。这一行为是**自动的**，
不需要 peerdrive 侧任何代码。

**孤儿 -wal / -shm**：如果手工删除了 `peerdrive.db` 但忘了删
`-wal` / `-shm`，下次 OpenDB 时 SQLite 会**忽略**孤儿的 WAL（因为没有
匹配的主库）——不会报错，但也不会用到那些孤立数据。

---

## 4. 实施细节

### 4.1 改动点

**主库（`back/internal/repository/`）**：

- `db_driver_cgo.go`：DSN 后缀追加 `_journal_mode=WAL&_synchronous=NORMAL`。
- `db_driver_pure.go`：DSN 后缀追加 `_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)`。

**注册服务库（`back/signalserver/regserver/`）**：

- `driver_cgo.go`：DSN 后缀追加 `_journal_mode=WAL&_synchronous=NORMAL`。
- `driver_pure.go`：DSN 后缀追加 `_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)`。

保持两个库配置一致，避免后续排查时的"为什么这个库 WAL 那个不是"。

**未改动**：

- `dsn()` 函数：`:memory:` / `file:` 前缀路径仍然跳过 DSN 后缀
  （WAL 对内存库无意义，SQLite 内部用 memory journal）。
- 迁移表 `schema_migrations` / `PRAGMA user_version`：WAL 不是 schema
  变更，无需新条目。
- 连接池配置：`SetMaxOpenConns(8)` / `SetMaxIdleConns(4)` 不变。WAL
  下 8 个连接并发读完全可行；写者仍串行（SQLite 本身保证），`busy_timeout`
  兜底排队。

### 4.2 测试

新增 `back/internal/repository/db_wal_test.go`，5 个用例：

1. `TestJournalModeWAL` — 文件库 `PRAGMA journal_mode` 返回 `"wal"`。
2. `TestSynchronousNormalOnWAL` — `PRAGMA synchronous` 返回 `1`（NORMAL）。
3. `TestJournalModePersistedAcrossOpen` — 关闭重开后 WAL 持久。
4. `TestMemoryDBNotForcedWAL` — `:memory:` 不被误开 WAL（断言 `dsn()`
   的 `isMemoryDB` 分支未被改坏）。
5. `TestLegacyDeleteDBMigratedToWAL` — 旧 DELETE 库在 OpenDB 时原地迁移，
   迁移后数据读写正常。

两个驱动（CGO mattn 与 pure-Go modernc）分别跑通：

```bash
cd back && go test -run "TestJournalMode|TestSynchronous|TestMemoryDB|TestLegacyDelete" ./internal/repository/
cd back && go test -tags nosqlite -run "TestJournalMode|TestSynchronous|TestMemoryDB|TestLegacyDelete" ./internal/repository/
```

---

## 5. 权衡与不做的事

### 5.1 为什么没有显式 `db.Exec("PRAGMA journal_mode=WAL")`

Issue 文本建议用 `db.Exec("PRAGMA journal_mode=WAL")`，但本仓库既有
模式是把 pragma 写在 DSN 后缀里（`busy_timeout` / `foreign_keys`）。
DSN 后缀的优势：

1. 与连接 open 原子发生，无竞态窗口。
2. 连接池每次新建连接都自动重新应用，无需 pool hook。
3. 单一位置定义所有连接级 pragma，易于审查。
4. 两个驱动方言差异被隔离在各自的 `db_driver_*.go` 文件里。

**测试验证**：5 个测试用例覆盖了 DSN 后缀的生效与持久化语义。

### 5.2 为什么没有加 `PRAGMA wal_autocheckpoint`

默认 1000 页对 peerdrive 的写入量级已经足够（WAL 文件 ≈ 4 MB 上限）。
显式设置只是把默认值写出来，没有实际收益。

### 5.3 为什么没有加关闭时主动 checkpoint

- peerdrive 是长驻服务，重启频率低，WAL 自动 checkpoint 已足够。
- `CloseDB` 当前仅测试用，生产路径走进程退出，OS 回收句柄后
  SQLite 会在下次 open 时自动恢复 WAL。
- 加主动 checkpoint 需要改 `CloseDB` 语义，超出本 Issue 范围。

### 5.4 为什么没动 `busy_timeout`

已经设为 5000ms，覆盖了正常事务时长。WAL 下写者之间仍然串行（SQLite
本身保证），`busy_timeout` 继续作为写者排队等待的上限——语义不变。

### 5.5 为什么没动连接池大小

WAL 下读者并发能力大幅提升，8 个连接对当前负载足够。若未来读吞吐
瓶颈，可以单独评估 `SetMaxOpenConns` 调整，但那是另一个 Issue。

---

## 6. 风险与回滚

**风险**：

1. **极旧的 SQLite 版本**（< 3.7.0，2010 年发布）不支持 WAL。本仓库
   两个驱动分别基于 SQLite 3.44+ / 3.46+，远超阈值。
2. **NFS 文件系统**：见 §3.3，peerdrive 部署不使用 NFS。
3. **手工清理数据目录时漏删 -wal / -shm**：SQLite 会忽略孤立 WAL，
   不报错但会"丢"那部分未 checkpoint 的数据。运维文档应提醒同时清理。

**回滚**：把 DSN 后缀里的 `_journal_mode=WAL&_synchronous=NORMAL` 删掉
即可。已经迁移到 WAL 的库会保留 WAL 模式（journal_mode 持久化），
但功能上完全正常——SQLite 在 WAL 模式下行为是稳定的。

---

## 7. 后续（不在本 Issue 范围）

- **#154 收敛评估**：主库 + 注册库都是 SQLite，未来可考虑合并成单库
  （当前两个库物理分离是历史包袱，但语义上已经统一 WAL 配置）。
- **WAL 监控**：可以暴露 `PRAGMA wal_checkpoint` 统计信息到 `/ready`
  或 metrics 端点，便于运维观察 WAL 堆积。
- **关闭时 checkpoint**：若未来引入 graceful shutdown，可在 `CloseDB`
  里加 `PRAGMA wal_checkpoint(TRUNCATE)` 把 WAL 合并回主库。
