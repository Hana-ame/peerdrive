//go:build windows

// Command btcheck is the smoke test for the out-of-process BitTorrent runtime.
//
// 存在理由：BT 运行时被拆进 peerdrive_bt.dll 之后，主二进制不再链接 torrent，
// 于是「BT 到底还能不能用」在编译期完全看不出来 —— 缺 DLL、DLL 与 exe 架构不符、
// 导出符号对不上、两个 Go 运行时共处的握手失败，全都只会在运行期炸。这个命令把
// 那条链路走一遍（LoadLibrary → pd_bt_open → 若干 op → pd_bt_close），用退出码
// 回答，适合放进安装包自检或发布前手工验证。
//
// 用法：
//
//	btcheck.exe              # 用默认配置跑一遍
//	btcheck.exe -dht         # 额外启动 DHT（会联网 bootstrap，较慢）
//	btcheck.exe -data <dir>  # 指定 torrent 数据目录
//
// 退出码：0 全部通过，1 加载/握手失败，2 某个 op 行为不符合预期。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"peerdrive/internal/btloader"
)

func main() {
	dht := flag.Bool("dht", false, "also start the mainline DHT (network, slow)")
	dataDir := flag.String("data", "", "torrent data directory (default: a temp dir)")
	flag.Parse()

	dir := *dataDir
	if dir == "" {
		tmp, err := os.MkdirTemp("", "btcheck-")
		if err != nil {
			fmt.Println("FAIL create temp dir:", err)
			os.Exit(1)
		}
		defer os.RemoveAll(tmp)
		dir = tmp
	}

	// Keep the smoke test off the network unless asked: the client otherwise
	// fetches the public tracker list, which is a 5s-timeout HTTP call.
	if os.Getenv("PEERDRIVE_BT_TRACKERS_DYNAMIC_ENABLE") == "" {
		_ = os.Setenv("PEERDRIVE_BT_TRACKERS_DYNAMIC_ENABLE", "false")
	}

	fmt.Println("runtime library available:", btloader.Available())

	start := time.Now()
	c, err := btloader.Open(btloader.Config{
		DataDir:       dir,
		StorageDir:    dir,
		DHTEnabled:    *dht,
		DHTListenAddr: ":6881",
	})
	if err != nil {
		fmt.Println("FAIL open:", err)
		os.Exit(1)
	}
	defer c.Close()
	fmt.Printf("opened in %s\n", time.Since(start).Round(time.Millisecond))

	st, _ := json.MarshalIndent(c.Status(), "  ", "  ")
	fmt.Println("status:", string(st))

	fail := 0
	check := func(name string, ok bool, detail any) {
		if ok {
			fmt.Printf("  ok   %s\n", name)
			return
		}
		fail++
		fmt.Printf("  FAIL %s: %v\n", name, detail)
	}

	check("download_dir", c.GetDownloadDir() != "", c.GetDownloadDir())
	check("list_downloads", len(c.ListDownloads()) == 0, len(c.ListDownloads()))
	check("list_seeders", len(c.ListSeeders()) == 0, len(c.ListSeeders()))
	check("get_download(unknown)", c.GetDownload("0000000000000000000000000000000000000000") == nil, "")
	// Error envelopes must survive the ABI, not just the happy path.
	check("pause(unknown) errors", c.PauseDownload("0000000000000000000000000000000000000000") != nil, "")
	check("add_magnet(\"\") errors", func() bool { _, err := c.AddMagnetURI(""); return err != nil }(), "")

	stats := c.GetGlobalStats()
	check("global_stats", stats.ActiveTorrents == 0, stats)

	dhtStatus := c.DHTStatus()
	if *dht {
		check("dht available", dhtStatus.Available, dhtStatus)
	} else {
		check("dht disabled", !dhtStatus.Available, dhtStatus)
	}

	if fail > 0 {
		fmt.Printf("%d check(s) failed\n", fail)
		os.Exit(2)
	}
	fmt.Println("all checks passed")
}
