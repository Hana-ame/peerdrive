# AGENTS.md

> 本仓是 peerdrive **按模块拆分**后的入口 / 索引仓。默认分支 `main` 只承载说明与索引，不承载实现代码。

## &#x20;hash to files

## interface

TODO：

```go
func Stat(sha string) (FileMetadata, error)      // 按 hash 取元数据
func Index(root string) ([]FileMetadata, error)  // 扫描并登记，幂等
func Scan(root string) ([]FileMetadata, error)   // 只在数据库中取数据

```

包括sqlite3的持久化实现，缓存，等。

以及测试CICD，gh actions，都做好。

### FileMetadata

| Field      | Type      | 说明                                                                                            |
| ---------- | --------- | --------------------------------------------------------------------------------------------- |
| `SHA256`   | string    | 内容哈希，内容身份                                                                                     |
| `Filename` | string    | 文件名（不含路径）                                                                                     |
| `FullPath` | string    | 内容当前所在路径，不是句柄，绝对路径                                                                            |
| `MimeType` | string    | MIME 类型                                                                                       |
| `Size`     | int64     | 内容字节数                                                                                         |
| `Ctime`    | time.Time | inode 元数据变化时更新：写内容、`chmod`、`chown`、改 mtime、增删硬链接。用户态**不可改**；改内容再把 mtime 写回原值会顶起它，四个时间戳里只有它露破绽 |
| `Mtime`    | time.Time | 内容写入时更新。可被 `touch -m` / `utimes` 改写；`rsync` / `make` 的增量依据，不是篡改证据                             |
| `Btime`    | time.Time | inode 创建时写一次。Linux 用户态不可设（`debugfs` 可绕），NTFS 可设；仅 `statx` 暴露，`syscall.Stat_t` 里没有             |
| `Atime`    | time.Time | 读取内容时更新。`relatime` 下更新条件宽松、`noatime` 下永不更新，不可信                                                |

#### win / linux 兼容

- [ ] FileMetadata
