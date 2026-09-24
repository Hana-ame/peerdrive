//go:build cgo

package repository

// 有 cgo 时用 mattn/go-sqlite3（C 绑定，体积小、与 CI/本机开发一致）。
//
// 为什么把驱动选择拆成两个文件：仓库的发布流程（.github/workflows/release.yml）
// 对**所有**平台都用 CGO_ENABLED=0 交叉编译（Windows/macOS 上配 mingw 太麻烦），
// 而 mattn/go-sqlite3 在无 cgo 时会退化成一个 stub 驱动——`sql.Open` 不报错，
// 第一次真正执行 SQL 才返回 "go-sqlite3 requires cgo to work"。
// 结果就是**发布出去的二进制一启动就死在 InitDB 的建表上**（之前 Windows 那
// 一格只 build 不 test，一直没暴露）。无 cgo 时改走纯 Go 驱动，见 db_driver_pure.go。

import _ "github.com/mattn/go-sqlite3"

const sqliteDriver = "sqlite3"

// dsnSuffix 返回连接串后缀（写锁等待 + 外键约束），两种驱动的语法不同，
// 所以各写一份——驱动是编译期二选一的，放错文件那份构建就静默失效。
//
// busy_timeout 为什么必须有：SQLite 默认在写锁冲突时**立刻**返回
// "database is locked"。本进程是并发写的（HTTP 登记 / 上传落库 / BT 完成
// 回调 / file_index 游标同步同时进行），没有它就靠运气：单跑永远不复现，
// 并发一上来才偶发 500。5s 足够覆盖正常事务时长。
//
// foreign_keys 为什么显式开：SQLite 默认**关闭**外键，而 schema 里写了
// ON DELETE CASCADE（collection_entries → collections 等）。不开这些级联
// 全是纸面约束——删了合集，条目成孤儿，列表里出现指向不存在内容的行。
func dsnSuffix() string { return "?_busy_timeout=5000&_foreign_keys=1" }
