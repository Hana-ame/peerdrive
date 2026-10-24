package cli

// check-update / update 两个子命令的实现。
//
// 分工（对应 issue #383 的「提示 + 显式应用」方案）：
//
//	peerdrive check-update   一次性检查：只比对版本并打印结果，不下载不替换。
//	peerdrive update         检查 + 下载 + sha256 校验 + 原子替换（旧版留 .bak，
//	                          失败回滚），完成后提示重启——替换由操作者显式触发，
//	                          绝不自动发生在 serve/all 运行期间。

import (
	"context"
	"fmt"
	"os"
	"time"

	"peerdrive/internal/config"
	"peerdrive/internal/update"
	"peerdrive/internal/version"
)

// runCheckUpdate 实现 `peerdrive check-update`。
//
// 退出码约定（便于脚本分支）：
//
//	0 = 已是最新
//	1 = 检查失败（网络 / 清单解析 / 缺本平台资产）
//	3 = 有新版本
func runCheckUpdate() int {
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := update.Check(ctx, version.Version, cfg.UpdateURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "peerdrive check-update: %v\n", err)
		return 1
	}
	if res.HasUpdate {
		fmt.Printf("peerdrive %s → 最新 %s（资产 %s）\n下载: %s\n",
			res.Current, res.Latest, res.Asset.Name, res.Asset.URL)
		return 3
	}
	fmt.Printf("peerdrive %s — 已是最新版本\n", res.Current)
	return 0
}

// runUpdate 实现 `peerdrive update`：检查 + 下载 + 校验 + 原子替换。
//
// 安全链在 internal/update.Apply 内部：下载到临时文件 → 校验 sha256（与
// latest.json / checksums.txt 同源）→ 备份旧版为 .bak → 两步 rename 换入 →
// 替换后复验；任何一步失败都回滚，原二进制不动。替换完成后**需要重启**才生效
// （服务器在 systemd/docker 监督下，进程内自重启会跟监督者打架）。
func runUpdate() int {
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	res, err := update.Check(ctx, version.Version, cfg.UpdateURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "peerdrive update: %v\n", err)
		return 1
	}
	if !res.HasUpdate {
		fmt.Printf("peerdrive %s — 已是最新版本，无需更新\n", res.Current)
		return 0
	}
	fmt.Printf("peerdrive %s → %s：下载 %s …\n", res.Current, res.Latest, res.Asset.URL)
	if err := update.Apply(ctx, res.Asset); err != nil {
		fmt.Fprintf(os.Stderr, "peerdrive update: 更新失败（原二进制未被改动）: %v\n", err)
		return 1
	}
	fmt.Printf("peerdrive 已更新为 %s（旧版本保留为 <当前二进制>.bak）\n请重启 peerdrive 使新版本生效\n", res.Latest)
	return 0
}
