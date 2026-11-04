// webrtc-node 让本机进程成为一个 WebRTC 接收端，信令走 PeerJS。
//
// 纯出站：不监听端口、不提供 http/ws 服务。启动后把 PeerJS 分配的标识符
// 打在日志里，浏览器侧照着连即可。连上之后 DataChannel 上传的不是字符串
// 而是带类型的二进制帧——控制信号、metadata、文件本体、校验都能走。
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/Hana-ame/peerdrive/webrtc"
)

func main() {
	logger := log.New(os.Stderr, "", log.LstdFlags|log.Lmicroseconds)

	srv, err := webrtc.Listen(context.Background(), webrtc.Config{
		Logger: logger,
		OnConn: handle,
	})
	if err != nil {
		logger.Fatalf("启动失败: %v", err)
	}
	defer srv.Close()

	logger.Printf("")
	logger.Printf("=============== PEER ID ===============")
	logger.Printf("  %s", srv.ID())
	logger.Printf("=======================================")
	logger.Printf("把它填进页面即可连接；本进程不监听任何端口")
	logger.Printf("")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	logger.Printf("收到退出信号，关闭中")
}

// handle 处理一条新连接的完整生命周期。
func handle(c *webrtc.Conn) {
	log.Printf("接入 · %s · %s", c.Remote(), c.ID())

	// 收到的文件写到临时目录，便于本地验证。
	dir, err := os.MkdirTemp("", "webrtc-recv-*")
	if err != nil {
		log.Printf("建临时目录失败: %v", err)
		return
	}
	log.Printf("接收目录 · %s", dir)

	var received int64
	var lastMeta string

	for {
		f, err := c.Next(context.Background())
		if err != nil {
			log.Printf("断开 · %s: %v（累计收到 %d 字节）", c.Remote(), err, received)
			return
		}

		switch f.Kind {
		case webrtc.KindHello:
			log.Printf("← hello · %s", pretty(f.Data))

		case webrtc.KindControl:
			log.Printf("← control · %s", pretty(f.Data))
			// 收到 ping 就回 pong，证明控制通道是双向的。
			var body struct {
				Action string `json:"action"`
			}
			_ = json.Unmarshal(f.Data, &body)
			if body.Action == webrtc.ActionPing {
				_ = c.SendControl(f.Stream, webrtc.ActionPong, nil)
			}

		case webrtc.KindMetadata:
			lastMeta = string(f.Data)
			log.Printf("← metadata · %s", truncate(lastMeta, 400))
			// 回一个 ack，让对端知道清单已收到。
			_ = c.SendJSON(webrtc.KindAck, f.Stream, map[string]any{
				"stream": f.Stream,
				"kind":   "metadata",
				"ok":     true,
			})

		case webrtc.KindData:
			received += int64(len(f.Data))
			if err := writeBlob(dir, f.Stream, f.Data); err != nil {
				log.Printf("落盘失败 stream=%d: %v", f.Stream, err)
			}
			// 每跨过 4 MiB 边界报一次进度，避免日志刷屏。
			if received/(4<<20) > (received-int64(len(f.Data)))/(4<<20) {
				log.Printf("… 已收 %d 字节", received)
			}

		case webrtc.KindCheck:
			log.Printf("← check · %s", pretty(f.Data))
			// 把对端给的 digest 原样回执，附上本端算出来的结果。
			var chk webrtc.Check
			_ = json.Unmarshal(f.Data, &chk)
			ok := chk.Digest != ""
			_ = c.SendJSON(webrtc.KindCheck, f.Stream, webrtc.Check{
				Stream: f.Stream,
				Path:   chk.Path,
				Algo:   chk.Algo,
				Digest: chk.Digest,
				OK:     &ok,
				Note:   "ack from webrtc-node",
			})

		case webrtc.KindAck:
			log.Printf("← ack · %s", pretty(f.Data))

		case webrtc.KindError:
			log.Printf("← error · %s", pretty(f.Data))

		default:
			log.Printf("← 未知帧 %d · %d 字节", f.Kind, len(f.Data))
		}
	}
}

// writeBlob 把某个 stream 的数据块按 stream id 落到独立文件。
func writeBlob(dir string, stream uint32, b []byte) error {
	name := filepath.Join(dir, "stream-"+strconv.FormatUint(uint64(stream), 10)+".bin")
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(b)
	return err
}

func pretty(b []byte) string { return truncate(string(b), 400) }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
