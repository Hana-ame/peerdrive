// peerdrive Windows 入口：与默认入口同一份 internal/cli 调度，
// 唯一差别是 Options.Tray=true——serve/all 起来之后挂系统托盘
// （打开面板 / 打开网盘 / 检查更新 / 退出）。
//
// 托盘编译期只在 Windows 存在（internal/tray 的 build tag），
// 运行期可用 PEERDRIVE_TRAY=0 强制关闭（服务化、CI、纯远程场景）。
package main

import (
	"os"

	"peerdrive/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], cli.Options{Tray: true}))
}
