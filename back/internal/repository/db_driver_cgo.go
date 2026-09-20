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
