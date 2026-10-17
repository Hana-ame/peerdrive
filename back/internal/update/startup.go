package update

import (
	"context"
	"time"

	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"peerdrive/internal/version"
)

// startupDelay 是首次检查前的延迟：不阻塞启动日志，也让监督者/面板先就位。
// 测试里可调短（同包变量）。
var startupDelay = 2 * time.Second

// StartupCheck 是 serve/all 启动时的可选检查入口。
//
// 默认关：cfg.AutoUpdate 为 false 时**立即返回，不起 goroutine、不发任何网络
// 请求**——不开 PEERDRIVE_AUTO_UPDATE 的部署与升级前行为完全一致。
//
// 打开后（PEERDRIVE_AUTO_UPDATE=true）：后台 goroutine 延迟 startupDelay 后
// 查一次新版本并打日志（只提示，不替换），之后按 cfg.UpdateIntervalHours 周期
// 复查；间隔为 0 时只在启动时查一次。
func StartupCheck(cfg *config.Config) {
	if !cfg.AutoUpdate {
		return
	}
	go func() {
		sleep(startupDelay)
		for {
			checkAndNotify(cfg)
			interval := time.Duration(cfg.UpdateIntervalHours) * time.Hour
			if interval <= 0 {
				return
			}
			sleep(interval)
		}
	}()
}

// checkAndNotify 拉一次版本并打日志。检查失败只警告、不影响运行——
// 更新检查是增强能力，绝不能因为更新源不可达让主服务起不来。
func checkAndNotify(cfg *config.Config) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := Check(ctx, version.Version, cfg.UpdateURL)
	if err != nil {
		log.LogWarn("update: 检查新版本失败（不影响运行）: %v", err)
		return
	}
	if res.HasUpdate {
		log.LogWarn("update: 检测到新版本 %s（当前 %s）— 运行 `peerdrive update` 自动更新，或手动下载 %s",
			res.Latest, res.Current, res.Asset.URL)
		return
	}
	log.LogInfo("update: 已是最新版本 %s", res.Current)
}
