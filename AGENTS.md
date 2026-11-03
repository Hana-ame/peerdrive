# AGENTS.md

> 本仓是 peerdrive **按模块拆分**后的入口 / 索引仓。默认分支 `main` 只承载说明与索引，不承载实现代码。

## &#x20;hash to files

## interface

已实现，见 `metadata/`（package `metadata`）。`interface/` 保持纯契约：只有 `FileMetadata`，不引入 sqlite 依赖，免得只想用结构体的模块都被拖进一个数据库驱动。

### API

```go
func Stat(sha string) (FileMetadata, error)      // 按 hash 取元数据
func Index(root string) ([]FileMetadata, error)  // 扫描并登记，幂等
// 如果是文件夹，那么登记的文件 = 其下属[]FileMetadata json之后的hash。json的顺序按照ctime降序排列
func List(root string) ([]FileMetadata, error)   // 只访问sqlite3中持久化的部分，列出
```

签名照旧，落在 `metadata` 包：`metadata.Stat/Index/List` 走默认库（`$PEERDRIVE_METADATA_DB`，缺省在用户缓存目录下），`metadata.Open(path)` 拿到 `*Store` 后调同名方法即可用自己的库。

### 实现约定

- **存储**：SQLite（`modernc.org/sqlite`，纯 Go，无 cgo）。表 `files` 以 `full_path` 为主键——一个路径同时只装一份内容；`sha256`、`parent` 各建索引。同一份内容可以有多条路径，`Stat` 取 ctime 最新的那条。
- **路径**：入库前一律转绝对路径，`FullPath` 不依赖调用方的 cwd。
- **目录**：内容是直接子项的 `[]FileMetadata` JSON。排序 ctime 降序，同刻先比文件名再比路径——只按 ctime 排，同刻多个文件时 hash 会飘。`Size` = 这段 JSON 的字节数，`MimeType` = `inode/directory`。空目录编码成 `[]` 而不是 `null`，两者 hash 不同。
- **`List(dir)`** 返回直接子项，顺序与 manifest 一致：把返回值再 JSON 一遍就应当等于父目录的 hash（`TestIndexRegistersTree` 钉住这条）。`List(file)` 返回它自己；没登记过的路径返回空切片，不是错误。
- **`Index`**：先把整棵树读完，再单事务写入，失败不留半套数据；重复扫描结果逐字节一致。目录下已从磁盘消失的行会被删掉，否则 `List` 会和 manifest 对不上。符号链接、设备等一律跳过：它们没有自己的内容，跟进去还会绕圈。
- **防自扰**：`Index` 读文件会顶起 atime，列目录会顶起目录的 atime，而 atime 在 manifest 里。所以 (size, mtime, ctime) 都没变的文件直接复用已登记的哈希、不再读；目录在 ctime/mtime 没变时沿用上次记下的 atime。ctime 拿不到的平台上这条捷径关闭，宁可重算。
- **时间戳**：统一存 UTC 的 UnixNano；取不到就留零值，不拿别的字段顶替。

### 已知取舍

目录 hash 是按注释字面实现的，`FileMetadata` 里的一切都进身份：绝对路径、四个时间戳，一个不落。所以同一棵树换个路径、或者换个文件系统复制一遍，hash 就不同——它是"这份内容当前的这个位置"的身份，不是 git tree 那种可搬迁的 hash。

### FileMetadata

| Field      | Type      | 说明                                                                                            |
| ---------- | --------- | --------------------------------------------------------------------------------------------- |
| `SHA256`   | string    | 内容哈希，内容身份                                                                                     |
| `Filename` | string    | 文件名（不含路径）                                                                                     |
| `FullPath` | string    | 内容当前所在路径，不是句柄                                                                                 |
| `MimeType` | string    | MIME 类型                                                                                       |
| `Size`     | int64     | 内容字节数                                                                                         |
| `Ctime`    | time.Time | inode 元数据变化时更新：写内容、`chmod`、`chown`、改 mtime、增删硬链接。用户态**不可改**；改内容再把 mtime 写回原值会顶起它，四个时间戳里只有它露破绽 |
| `Mtime`    | time.Time | 内容写入时更新。可被 `touch -m` / `utimes` 改写；`rsync` / `make` 的增量依据，不是篡改证据                             |
| `Btime`    | time.Time | inode 创建时写一次。Linux 用户态不可设（`debugfs` 可绕），NTFS 可设；仅 `statx` 暴露，`syscall.Stat_t` 里没有             |
| `Atime`    | time.Time | 读取内容时更新。`relatime` 下更新条件宽松、`noatime` 下永不更新，不可信                                                |

#### win / linux 兼容

- [x] FileMetadata
  - Linux：`statx` 取 Btime（`syscall.Stat_t` 里没有），失败退回 `Stat_t` 的 Ctim/Mtim/Atim。
  - Windows：`GetFileInformationByHandleEx(FILE_BASIC_INFO)` 取 ChangeTime 当 Ctime、CreationTime 当 Btime；失败退回 `Win32FileAttributeData`，此时 Ctime 留零。
  - 其余平台：只有 Mtime。
  - 注意 `FILE_BASIC_INFO` 是 40 字节（四个 FILETIME + `FileAttributes`）：少给 4 字节会得到 `ERROR_BAD_LENGTH`，而不是截断的数据。
