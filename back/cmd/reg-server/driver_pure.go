//go:build !cgo

package main

// 无 cgo（CGO_ENABLED=0、交叉编译、release 构建）时用 modernc.org/sqlite——
// 它把 SQLite 的 C 源码翻译成 Go，不需要 C 编译器。
//
// 代价：二进制更大、编译更慢、首次查询略慢。
// 好处：release 出的二进制真能建库（而不是像 mattn 那样一启动就崩）。
//
// 驱动名：modernc 注册 "sqlite"，mattn 注册 "sqlite3"——故 main.go 不硬编码
// 驱动名，用本文件的常量。

import _ "modernc.org/sqlite"

const sqliteDriver = "sqlite"

// dsnSuffix 见 driver_cgo.go 的同名函数（为什么需要 busy_timeout）。
// 语法差异：modernc 用 `_pragma=busy_timeout(5000)`，mattn 用 `_busy_timeout=5000`。
func dsnSuffix() string { return "?_pragma=busy_timeout(5000)" }
