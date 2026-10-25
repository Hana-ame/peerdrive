// peerdrive-win7 — Windows 7 入口（托盘版）。
//
// 与 cmd/peerdrive-win 的关系：**同一份 internal/cli 调度 + 同一个托盘**，
// 零逻辑复制；差别只在构建方式——
//
//  1. 工具链：必须用 XTLS/go-win7 补丁版 SDK 构建（见 scripts/build-win7.sh）。
//     官方 Go 1.21.5+ 运行时启动阶段调用 bcryptprimitives.dll!ProcessPrng，
//     Win7 没有该 DLL，进程直接以 Exception 0xc0000005 崩在起跑线上——
//     这就是「官方最后支持 Win7 的 Go 是 1.21」的由来。补丁 SDK 打掉这个
//     API 依赖，back/go.mod（go 1.26.2）与依赖树完全不用动。
//  2. 架构：386 为主（老机器主力），同时出 amd64（Win7 x64）。
//     补丁产物在 Win7~11 上都能跑。
//
// 历史备注：本入口曾经是「最小引用」形态（只 import gin + serverapp，
// 无托盘）。那个约束的前提是「只能用 Go ≤1.21 老工具链，新依赖编不了」——
// 补丁 SDK 让前提消失后，没有理由再阉割功能：托盘代码（Shell_NotifyIconW
// + NOTIFYICON_VERSION_4，后者正是 Win7 引入的气球行为）在 Win7 上完全
// 合法。服务化场景仍可用 PEERDRIVE_TRAY=0 强制关闭托盘。
package main

import (
	"os"

	"peerdrive/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], cli.Options{Tray: true}))
}
