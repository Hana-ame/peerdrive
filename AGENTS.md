# AGENTS.md

> 本仓是 peerdrive **按模块拆分**后的入口 / 索引仓。默认分支 `main` 只承载说明与索引，不承载实现代码。

## &#x20;hash to files

## interface

已实现，见 `metadata/`（package `metadata`）。`interface/` 保持纯契约：只有 `FileMetadata`，不引入 sqlite 依赖，免得只想用结构体的模块都被拖进一个数据库驱动。

错误单独成包：`errors/`（package `errors`）。调用方判断"是没找到还是真出错"时只 import 它，不用为了 `errors.Is` 去链 SQLite。`metadata` 里保留同名别名指向同一批值，所以 `metadata.ErrNotFound` 照旧可用，两边 `errors.Is` 都认。

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
- **目录**：`IsDir` 为真，内容是直接子项的 `[]FileMetadata` JSON。排序 ctime 降序，同刻先比文件名再比路径——只按 ctime 排，同刻多个文件时 hash 会飘。`Size` = 这段 JSON 的字节数，`MimeType` = `inode/directory`。空目录编码成 `[]` 而不是 `null`，两者 hash 不同。
- **`List(dir)`** 返回直接子项，顺序与 manifest 一致：把返回值再 JSON 一遍就应当等于父目录的 hash（`TestIndexRegistersTree` 钉住这条）。`List(file)` 返回它自己；没登记过的路径返回空切片，不是错误。
- **`Index`**：先把整棵树读完，再单事务写入，失败不留半套数据；重复扫描结果逐字节一致。目录下已从磁盘消失的行会被删掉，否则 `List` 会和 manifest 对不上。符号链接、设备等一律跳过：它们没有自己的内容，跟进去还会绕圈。
- **防自扰**：`Index` 读文件会顶起 atime，列目录会顶起目录的 atime，而 atime 在 manifest 里。所以 (size, mtime, ctime) 都没变的文件直接复用已登记的哈希、不再读；目录在 ctime/mtime 没变时沿用上次记下的 atime。ctime 拿不到的平台上这条捷径关闭，宁可重算。
- **时间戳**：统一存 UTC 的 UnixNano；取不到就留零值，不拿别的字段顶替。
- **错误**：`ErrNotFound`、`ErrInvalidHash`、`ErrInvalidPath`、`ErrUnsupportedType` 定义在 `errors` 包，实现侧一律 `fmt.Errorf("...: %w", ...)` 包装，别拼字符串给调用方猜。

### 已知取舍

目录 hash 是按注释字面实现的，`FileMetadata` 里的一切都进身份：绝对路径、四个时间戳，一个不落。所以同一棵树换个路径、或者换个文件系统复制一遍，hash 就不同——它是"这份内容当前的这个位置"的身份，不是 git tree 那种可搬迁的 hash。

### FileMetadata

| Field      | Type      | 说明                                                                                            |
| ---------- | --------- | --------------------------------------------------------------------------------------------- |
| `SHA256`   | string    | 内容哈希，内容身份                                                                                     |
| `Filename` | string    | 文件名（不含路径）                                                                                     |
| `FullPath` | string    | 内容当前所在路径，不是句柄                                                                                 |
| `MimeType` | string    | MIME 类型                                                                                       |
| `Size`     | int64     | 内容字节数；目录是 manifest 的字节数                                                                     |
| `IsDir`    | bool      | 是不是目录。要分支就认这个字段，`MimeType` 只是展示串                                                  |
| `Ctime`    | time.Time | inode 元数据变化时更新：写内容、`chmod`、`chown`、改 mtime、增删硬链接。用户态**不可改**；改内容再把 mtime 写回原值会顶起它，四个时间戳里只有它露破绽 |
| `Mtime`    | time.Time | 内容写入时更新。可被 `touch -m` / `utimes` 改写；`rsync` / `make` 的增量依据，不是篡改证据                             |
| `Btime`    | time.Time | inode 创建时写一次。Linux 用户态不可设（`debugfs` 可绕），NTFS 可设；仅 `statx` 暴露，`syscall.Stat_t` 里没有             |
| `Atime`    | time.Time | 读取内容时更新。`relatime` 下更新条件宽松、`noatime` 下永不更新，不可信                                                |

#### win / linux 兼容

- [x] FileMetadata
- [x] Windows：`syscall.NewLazyDLL("kernel32.dll")` 直调，不用 cgo、不引 `x/sys/windows`。三档降级：

  | 档 | API | 最低系统 | 拿到几个时间 |
  | -- | --- | -------- | ------------ |
  | 1 | `GetFileInformationByHandleEx(FileBasicInfo)` | Vista / Server 2008 | 四个（含 ChangeTime、CreationTime） |
  | 2 | `GetFileInformationByHandle` | NT 4.0 | 三个，缺 ChangeTime |
  | 3 | `Win32FileAttributeData`（`os.FileInfo` 自带） | 一直有 | 三个，缺 ChangeTime |

  - `LazyProc.Call` 找不到符号会 panic，所以统一走 `safeCallProc`：先 `Find()` 再 `Call()`，缺符号按失败处理。
  - `FILE_BASIC_INFO` 是 **40 字节**（四个 FILETIME + `FileAttributes`）。386 下 Go 对 int64 只做 4 字节对齐，不加尾部填充会缩成 36，Windows 直接返回 `ERROR_BAD_LENGTH`——不是截断数据，是整个调用失败、四个时间静默变零。文件里用两个数组长度断言把尺寸钉死，偏大偏小都编译不过。
  - FILETIME 换算用标准库 `Filetime.Nanoseconds()`，不自己算 1601→1970 的偏移。
- [x] Linux：`statx` 一次拿全四个，`syscall.Stat_t` 兜底（老内核 `ENOSYS` 时保住三个，缺 Btime）。只认 `statx` 返回的 mask 位：文件系统没记 Btime 时读它是零值，不是 1970。
- [x] 其余平台：只有 Mtime。
- Win7 分两层看，别混：
  - **源码层**：只用 Vista 就有的 API（`GetFileInformationByHandleEx` 是 Vista+），386/amd64 的结构体布局都钉死，所以 Win7 在覆盖范围内。
  - **运行时层**：官方 Go 1.21.5+ 把熵源换成 `bcryptprimitives.dll!ProcessPrng`（Win8+ 才有），Win7 上**启动阶段**就崩（`Exception 0xc0000005, PC=0x0 in asmstdcall`），业务代码没机会跑。解法不是降级，是用 [XTLS/go-win7](https://github.com/XTLS/go-win7) 的同版本补丁 SDK 出 Windows 产物——它把随机数/控制台/文件系统调用换回 Win7 有实现的版本，版本号跟着官方走。Xray 的 `release-win7.yml` 直接把 `GOROOT` 内容替换掉；ech-proxy 解到 `/tmp` 再指 `GOROOT` + `GOTOOLCHAIN=local`。本仓 CI 的 `win7` job 用的是后者。
