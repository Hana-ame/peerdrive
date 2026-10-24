// peerdrive 默认入口（linux / darwin / windows 通用，无托盘）。
//
// 全部调度逻辑在 internal/cli；本文件只做「把 os.Args 递进去、把退出码
// 递出来」。Windows 上要托盘用 cmd/peerdrive-win，Win7 老机器用
// cmd/peerdrive-win7——三个入口共用同一份 cli/serverapp 代码，
// 差别只在启动时传的平台选项（见 internal/cli 的包注释）。
package main

import (
	"os"

	"peerdrive/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], cli.Options{}))
}
