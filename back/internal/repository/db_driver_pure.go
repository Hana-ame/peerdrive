//go:build !cgo

package repository

// 无 cgo（CGO_ENABLED=0，交叉编译 / Windows CI / 发布构建）时走纯 Go 的
// modernc.org/sqlite——它把 SQLite 的 C 源码翻译成了 Go，不需要任何 C 编译器。
//
// 代价：二进制更大、编译更慢、首次查询略慢。收益：发布出去的 Windows/macOS
// 二进制真的能建库（此前是 stub，一跑就挂），而且 Windows CI 上能真跑测试
// —— 路径语义那一堆 Windows 专属分支终于不是只靠 t.Skip 假装通过。
//
// 注意驱动名：modernc 注册的是 "sqlite"，mattn 注册的是 "sqlite3"。
// 所以 db.go 里不能写死驱动名，用这里的常量。

import _ "modernc.org/sqlite"

const sqliteDriver = "sqlite"
