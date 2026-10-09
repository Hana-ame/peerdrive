// 信令服务器的唯一真相源：本目录。
// module 名统一为 go-peerserver —— 与线上消费者（wintools cmd/peerfs-server）
// 早已在用的 import 路径一致，避免同名两份代码各自发版。原 go-peersignal 一名
// 只存在于本仓内部，无外部消费者，故统一到 go-peerserver 而非反向。
module github.com/Hana-ame/go-peerserver

go 1.26.2

require (
	github.com/Hana-ame/go-signalframe v0.0.0
	github.com/gorilla/websocket v1.5.3
)

replace github.com/Hana-ame/go-signalframe => ../signalframe

require (
	github.com/mattn/go-sqlite3 v1.14.52
	github.com/stretchr/testify v1.11.1
	golang.org/x/crypto v0.58.0
	modernc.org/sqlite v1.60.1
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.49.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	modernc.org/libc v1.77.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)
