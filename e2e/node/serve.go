package node

import (
	"context"
	"encoding/json"
	"log"

	"github.com/Hana-ame/peerdrive/webrtc"
)

// HandleConn 处理一条数据通道的完整生命周期。
//
// file 请求都起独立 goroutine：浏览器的多线程下载就是多条 stream 同时来，
// 串行处理会让并行退化成排队。collection 很小，直接在当前协程发。
func HandleConn(c *webrtc.Conn, svc *Service, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
	defer c.Close()
	logger.Printf("数据通道接入 · %s · %s", c.Remote(), c.ID())

	for {
		f, err := c.Next(context.Background())
		if err != nil {
			logger.Printf("数据通道断开 · %s: %v", c.Remote(), err)
			return
		}

		switch f.Kind {
		case webrtc.KindControl:
			var body struct {
				Action string `json:"action"`
			}
			if err := json.Unmarshal(f.Data, &body); err != nil {
				logger.Printf("控制帧解析失败 stream=%d: %v", f.Stream, err)
				continue
			}
			switch body.Action {
			case ActionCollection:
				if err := svc.ServeCollection(c, f.Stream); err != nil {
					logger.Printf("collection 失败 stream=%d: %v", f.Stream, err)
				}
			case ActionFile:
				var req FileRequest
				if err := json.Unmarshal(f.Data, &req); err != nil {
					logger.Printf("file 请求解析失败 stream=%d: %v", f.Stream, err)
					continue
				}
				go func(stream uint32) {
					if err := svc.ServeFile(c, stream, req); err != nil {
						// 这里多半是连接已关；打一条就够，不重试。
						logger.Printf("file 失败 sha=%.12s stream=%d: %v", req.SHA256, stream, err)
					}
				}(f.Stream)
			case ActionPing:
				if err := c.SendControl(f.Stream, ActionPong, nil); err != nil {
					logger.Printf("pong 失败 stream=%d: %v", f.Stream, err)
				}
			}

		case webrtc.KindHello, webrtc.KindAck:
			// hello 已由 webrtc 侧自动发出/打印，这里不需要额外动作。

		case webrtc.KindCheck, webrtc.KindMetadata, webrtc.KindData:
			// 这条通道上节点只应答，不收文件；忽略并留一行日志便于排查。
			logger.Printf("忽略入站 %s stream=%d len=%d", f.Kind, f.Stream, len(f.Data))
		}
	}
}
