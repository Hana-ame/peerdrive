//go:build cgo && !nosqlite

package regserver

// 有 cgo 时用 mattn/go-sqlite3（C 绑定，体积小，与本地开发/CI 一致）。
//
// 为什么按 cgo 切分两个驱动文件：release.yml 对所有平台都以 CGO_ENABLED=0
// 交叉编译（Windows/macOS 配 mingw 太麻烦），而 mattn/go-sqlite3 在无 cgo
// 时会退化成一个桩驱动——sql.Open 不报错，直到第一条 SQL 才返回
// "go-sqlite3 requires cgo to work"。后果是发布出的二进制一建表就死。
// 无 cgo 时改用纯 Go 驱动，见 driver_pure.go。
//
// 口径与 internal/repository/db_driver_cgo.go 一致（那里有完整理由说明）。

import _ "github.com/mattn/go-sqlite3"

const sqliteDriver = "sqlite3"

// dsnSuffix 返回连接串后缀。驱动语法不同，故两个文件各写一份——
// 把驱动放错文件的后果是构建静默出错（能编译，跑起来才炸）。
func dsnSuffix() string { return "?_busy_timeout=5000" }
