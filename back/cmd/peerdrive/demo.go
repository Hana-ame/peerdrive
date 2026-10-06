// demo.go —— `peerdrive demo`：一条命令跑通全链路，不需要读任何文档。
//
// 为什么需要它：`peerdrive serve` 起一个节点是零配置的，但「一个人用」看不出
// 网盘这个项目干了什么——网盘的意义在于**两个节点之间**传文件。
// 原来这件事只有一个入口：scripts/netdisk-local-demo.sh，那是给开发者的脚本，
// 依赖仓库、依赖 Go 工具链、依赖三个端口空闲。下载二进制的人没有这些。
//
// 这里把同样的链路（信令 + 两个节点 + 市场 → 加入 → 清单 → 拉取 → 校验）
// 塞进二进制：`peerdrive demo` 一条命令，自己挑端口、自己造数据、
// 跑完打印逐步结论。不需要 Go、不需要仓库、不需要装任何东西。
//
// ⚠️ 链路步骤与 scripts/netdisk-local-demo.sh **保持一致**（同样的路由、同样的
// 环境变量、同样的校验口径）。改这里就要同步改那边，否则两个「演示」会各自
// 漂移，读者看到的现象不一样，那比没有更糟。
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// demoPorts 演示环境用的端口。刻意避开 3000/9000/4000：
// 用户机器上这三个很可能已经被自己的实例占着，撞端口会让 demo 直接起不来。
const (
	demoSignalPort = 19100
	demoNodeAPort  = 19101
	demoNodeBPort  = 19102
)

// demoEnv 造一个节点的完整环境变量（与 netdisk-local-demo.sh 的 COMMON_ENV 对齐）。
//
// PEERDRIVE_PEERJS_ID 固定：演示要的是「重启后 id 不变」这个可观察事实，
// 随机 id 的话每步打印的 id 都对不上，读者无法判断前后是不是同一个节点。
func demoEnv(dir, id string, sigPort int, peers, shareDirs string) []string {
	env := append(os.Environ(),
		"PEERDRIVE_STORAGE="+filepath.Join(dir, "root"),
		"PEERDRIVE_DOWNLOAD_DIR="+filepath.Join(dir, "root", "downloads"),
		// ⚠️ 主服务读的是 PORT，不是 PEERDRIVE_PORT（config.go:213）。
		// 写成后者会被静默忽略、节点照常起在默认 3000 —— 演示里表现为
		// "bind: address already in use"。与 reg 子命令的 PORT 坑同一类。
		"PORT="+strconv.Itoa(sigPort),
		"PEERDRIVE_PEERJS_ENABLE=true",
		"PEERDRIVE_PEERJS_HOST=127.0.0.1",
		"PEERDRIVE_PEERJS_PORT="+strconv.Itoa(demoSignalPort),
		"PEERDRIVE_PEERJS_KEY=peerjs",
		"PEERDRIVE_PEERJS_SECURE=false",
		"PEERDRIVE_PEERJS_ID="+id,
		"PEERDRIVE_DISCOVER_URL=http://127.0.0.1:"+strconv.Itoa(demoSignalPort),
		"PEERDRIVE_BT_DHT_ENABLE=false",
		"PEERDRIVE_IPFS_GATEWAY_ENABLE=false",
		"PEERDRIVE_SHARE_ENABLE=true",
		"PEERDRIVE_SWAGGER=off",
	)
	// ⚠️ 共享目录必须显式声明：未声明的目录一律拒绝（这是防任意文件读写的边界，
	// 不会因为「目录存在」就放行）。只声明可读不够，登记侧也要认。
	if shareDirs != "" {
		env = append(env, "PEERDRIVE_SHARE_DIRS="+shareDirs)
	}
	return env
}

type demoProc struct {
	name string
	cmd  *exec.Cmd
	log  *os.File
}

func (p *demoProc) stop() {
	if p == nil {
		return
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		_, _ = p.cmd.Process.Wait()
	}
	if p.log != nil {
		_ = p.log.Close()
	}
}

// selfPath 取自身真实路径。
// 去符号链接是关键：面板示例/文档里常写 `peerdrive serve`，实际经 PATH 调用时
// os.Executable() 给的可能是链接，demo 用它 exec 自己会失败。
func selfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil
}

func waitHTTP(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			last = fmt.Errorf("%s 返回 %d", url, resp.StatusCode)
		} else {
			last = err
		}
		time.Sleep(300 * time.Millisecond)
	}
	if last == nil {
		last = fmt.Errorf("超时未就绪")
	}
	return last
}

// postJSON / getJSON 只做演示要用的最小封装。
func postJSON(url string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("返回 %d: %s", resp.StatusCode, truncate(string(data), 160))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func getJSON(url string, out any) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("返回 %d: %s", resp.StatusCode, truncate(string(data), 160))
	}
	return json.Unmarshal(data, out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

type nodeStatus struct {
	ID    string   `json:"id"`
	Peers []string `json:"peers"`
}

type sharesResp struct {
	Files []struct {
		Hash string `json:"hash"`
		Name string `json:"name"`
		Path string `json:"path"`
		Size int64  `json:"size"`
	} `json:"files"`
}

var demoFailed bool

func demoStep(ok bool, format string, a ...any) {
	if ok {
		fmt.Printf("  [通过] "+format+"\n", a...)
		return
	}
	demoFailed = true
	fmt.Printf("  [失败] "+format+"\n", a...)
}

func runDemo(args []string) int {
	dir := "/tmp/peerdrive-demo"
	for i, a := range args {
		if a == "--dir" && i+1 < len(args) {
			dir = args[i+1]
		}
	}

	demoFailed = false
	for _, d := range []string{
		filepath.Join(dir, "a"), filepath.Join(dir, "b"),
	} {
		if err := os.RemoveAll(d); err != nil {
			fmt.Fprintf(os.Stderr, "清理旧演示目录失败: %v\n", err)
			return 1
		}
	}
	shareDir := filepath.Join(dir, "a", "root", "downloads", "shared")
	for _, d := range []string{
		shareDir,
		filepath.Join(dir, "a", "root"),
		filepath.Join(dir, "b", "root", "downloads"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "准备目录失败: %v\n", err)
			return 1
		}
	}

	// 造一个待分享的文件——没有真实数据的话，「传了一个文件」是空话。
	payload := []byte("peerdrive demo：这份内容会从节点 A 传给节点 B，并按 sha256 校验。\n")
	src := filepath.Join(shareDir, "hello.txt")
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "造测试文件失败: %v\n", err)
		return 1
	}
	sum := sha256.Sum256(payload)
	wantHash := hex.EncodeToString(sum[:])

	exe, err := selfPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "找不到自身路径: %v\n", err)
		return 1
	}

	fmt.Println()
	fmt.Println("  peerdrive 演示：信令 + 两个节点，跑通「市场 → 加入 → 清单 → 拉取 → 校验」")
	fmt.Println("  ────────────────────────────────────────────────────────────────")

	var procs []*demoProc
	defer func() {
		for i := len(procs) - 1; i >= 0; i-- {
			procs[i].stop()
		}
	}()

	// ⚠️ 每个节点必须有自己的工作目录：peerdrive.db 落在 cwd 下，
	// 共用一个 cwd 就等于两个节点共用一个数据库，joined_nodes / 节点目录互相污染，
	// 演示会表现得像「市场里看不到对方」。
	start := func(name, sub, cwd string, env []string) (*demoProc, error) {
		f, err := os.Create(filepath.Join(dir, name+".log"))
		if err != nil {
			return nil, err
		}
		cmd := exec.Command(exe, sub)
		cmd.Dir = cwd
		cmd.Env = env
		cmd.Stdout = f
		cmd.Stderr = f
		if err := cmd.Start(); err != nil {
			_ = f.Close()
			return nil, err
		}
		p := &demoProc{name: name, cmd: cmd, log: f}
		procs = append(procs, p)
		return p, nil
	}

	urlA := fmt.Sprintf("http://127.0.0.1:%d", demoNodeAPort)
	urlB := fmt.Sprintf("http://127.0.0.1:%d", demoNodeBPort)
	urlSig := fmt.Sprintf("http://127.0.0.1:%d", demoSignalPort)

	// ── 1) 信令 ──
	if _, err := start("signal", "signal", dir, append(os.Environ(),
		"PEERSIGNAL_ADDR=127.0.0.1:"+strconv.Itoa(demoSignalPort),
		// ⚠️ 不设 PEERJS_TOKENS：它把信号变成「必须带 token」，而节点侧
		// 没有对应字段可发 → WebSocket 握手直接 bad handshake，
		// 表现为节点反复 "connect failed (retry in 2s)"、
		// 信令 /discover/nodes 永远是空。脚本 netdisk-local-demo.sh 同样不设。
		// 本机演示没有对外暴露面，不需要这层门禁。
		"PEERSIGNAL_CORS=*",
	)); err != nil {
		fmt.Fprintf(os.Stderr, "起信令失败: %v\n", err)
		return 1
	}
	// /peerjs/id 是唯一免鉴权的信令端点（/status 要 token，会误判成没起来），
	// 它返回随机 id，能证明信令真的在服务而不只是端口开着。
	if err := waitHTTP(urlSig+"/peerjs/id", 30*time.Second); err != nil {
		demoStep(false, "信令就绪（127.0.0.1:%d）：%v", demoSignalPort, err)
		return finishDemo(dir)
	}
	demoStep(true, "信令就绪（127.0.0.1:%d）", demoSignalPort)

	// ── 2) 节点 A（共享方）──
	if _, err := start("node-a", "serve", filepath.Join(dir, "a"), demoEnv(filepath.Join(dir, "a"), "demo-node-a", demoNodeAPort, "", shareDir)); err != nil {
		fmt.Fprintf(os.Stderr, "起节点 A 失败: %v\n", err)
		return 1
	}
	if err := waitHTTP(urlA+"/ping", 60*time.Second); err != nil {
		demoStep(false, "节点 A 就绪（127.0.0.1:%d）：%v", demoNodeAPort, err)
		return finishDemo(dir)
	}
	demoStep(true, "节点 A 就绪（127.0.0.1:%d）", demoNodeAPort)

	// ── 3) 节点 B（消费方）──
	// ⚠️ 不设 PEERDRIVE_PEERJS_PEERS：设了会在 B 启动那一刻就去连 A，
	// 而 A 还没在信令上注册完，直接 "not connected"，之后 32 秒才重试——
	// 演示的等待窗口比这短，于是表现为「B 发现不了 A」。
	// 脚本 netdisk-local-demo.sh 同样不设，连接靠下面的 join 步骤建立。
	if _, err := start("node-b", "serve", filepath.Join(dir, "b"), demoEnv(filepath.Join(dir, "b"), "demo-node-b", demoNodeBPort, "", "")); err != nil {
		fmt.Fprintf(os.Stderr, "起节点 B 失败: %v\n", err)
		return 1
	}
	if err := waitHTTP(urlB+"/ping", 60*time.Second); err != nil {
		demoStep(false, "节点 B 就绪（127.0.0.1:%d）：%v", demoNodeBPort, err)
		return finishDemo(dir)
	}
	demoStep(true, "节点 B 就绪（127.0.0.1:%d）", demoNodeBPort)

	// 与脚本同口径：announce + WebRTC 握手要时间，起完先等一手再断言。
	time.Sleep(5 * time.Second)

	// ── 4) B 能发现 A（市场）──
	discovered := false
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		var mk struct {
			Nodes []struct {
				PeerID string `json:"peer_id"`
			} `json:"nodes"`
		}
		if getJSON(urlB+"/peerjs/nodes", &mk) == nil {
			for _, n := range mk.Nodes {
				if n.PeerID == "demo-node-a" {
					discovered = true
				}
			}
		}
		if discovered {
			break
		}
		time.Sleep(600 * time.Millisecond)
	}
	demoStep(discovered, "B 在节点市场里发现了 A")

	// ── 5) B 加入 A ──
	var joined struct {
		Status string `json:"status"`
	}
	if err := postJSON(urlB+"/peerjs/nodes/join", map[string]string{"peer": "demo-node-a"}, &joined); err != nil {
		demoStep(false, "B 加入 A：%v", err)
	} else {
		demoStep(joined.Status == "joined", "B 加入 A（status=%s）", joined.Status)
	}

	// ── 6) A 登记共享目录 ──
	var reg map[string]any
	if err := postJSON(urlA+"/files/register_folder", map[string]string{"folder_path": shareDir}, &reg); err != nil {
		demoStep(false, "A 登记共享目录：%v", err)
	} else {
		demoStep(true, "A 登记共享目录 %s", shareDir)
	}

	// ── 7) B 经 share 帧拿清单 ──
	var sh sharesResp
	found := false
	deadline = time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if getJSON(urlB+"/peerjs/nodes/demo-node-a/shares", &sh) == nil && len(sh.Files) > 0 {
			found = true
			break
		}
		time.Sleep(700 * time.Millisecond)
	}
	demoStep(found, "B 从 A 拿到共享清单（%d 个文件）", len(sh.Files))
	if !found {
		return finishDemo(dir)
	}

	var target struct {
		Hash string
		Name string
	}
	target.Hash, target.Name = sh.Files[0].Hash, sh.Files[0].Name
	if target.Name == "" {
		target.Name = filepath.Base(sh.Files[0].Path)
	}
	fmt.Printf("         清单首项：%s（%d 字节，sha256 %s…）\n",
		target.Name, sh.Files[0].Size, truncate(target.Hash, 16))

	// ── 8) B 跨节点拉取 ──
	var pull map[string]any
	if err := postJSON(urlB+"/p2p/pull", map[string]string{
		"peer": "demo-node-a", "hash": target.Hash, "name": target.Name,
	}, &pull); err != nil {
		demoStep(false, "B 向 A 发起拉取：%v", err)
	} else {
		demoStep(true, "B 向 A 发起拉取（hash=%s…）", truncate(target.Hash, 16))
	}

	// ── 9) 校验落盘内容 ──
	// ⚠️ 落盘目录是 downloads/pulled/，不是 downloads/：
	// 拉取走的是「节点市场」的落盘口径，与历史布局不同。
	// 写错的话最后一步必然 no such file，而前面 8 步全绿——极误导。
	dest := filepath.Join(dir, "b", "root", "downloads", "pulled", target.Name)
	verifyDeadline := time.Now().Add(60 * time.Second)
	var got []byte
	var vErr error
	for time.Now().Before(verifyDeadline) {
		got, vErr = os.ReadFile(dest)
		if vErr == nil && len(got) > 0 {
			break
		}
		time.Sleep(600 * time.Millisecond)
	}
	if vErr != nil {
		demoStep(false, "B 落盘校验：%v（期望 %s）", vErr, dest)
		return finishDemo(dir)
	}
	gotSum := sha256.Sum256(got)
	gotHash := hex.EncodeToString(gotSum[:])
	demoStep(gotHash == wantHash,
		"B 落盘内容 sha256 与源一致（%s，%d 字节）", truncate(gotHash, 16), len(got))

	return finishDemo(dir)
}

func finishDemo(dir string) int {
	fmt.Println("  ────────────────────────────────────────────────────────────────")
	fmt.Println()
	if demoFailed {
		fmt.Printf("  演示未全绿。日志在 %s/{signal,node-a,node-b}.log\n", dir)
		return 1
	}
	fmt.Println("  演示全绿：市场 → 加入 → 清单 → 拉取 → 校验")
	fmt.Println()
	fmt.Println("  接下来自己试：")
	fmt.Println()
	fmt.Println("    peerdrive serve")
	fmt.Println("      然后用浏览器打开它打印的面板地址（零配置，不用填任何参数）")
	fmt.Println()
	return 0
}