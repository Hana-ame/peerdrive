package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"peerdrive/internal/log"
	"peerdrive/pkg/hashutil"
)

// MQTTDiscovery 基于公共 broker 的分片房间发现。
// 模式：topic 按 collection hash 分片（peerdrive/v1/{hash}/nodes），
// 节点只订阅自己关注的分片——订阅数与消息量随集合摊开，公共 broker
// 可支持大规模（全局单 topic 的 fan-out 是瓶颈，分片后无上限）。
//
// 扩展性：发现只交换「节点 peer id」，实际传输仍走 PeerJS 云信令 +
// WebRTC 直连；换信令/传输不影响本组件。
type MQTTDiscovery struct {
	broker       string
	topicPrefix  string
	clientID     string
	onPeer       func(peerID string) // 发现新节点回调（去重由调用方保证）
	announceTick time.Duration

	client   mqtt.Client
	mu       sync.Mutex
	announce map[string]bool // 已 announce 的 peer id（同 id 不去重发）
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
}

// NewMQTTDiscovery 创建发现组件。onPeer 在发现新节点时回调。
func NewMQTTDiscovery(broker, topicPrefix, clientID string, onPeer func(peerID string)) *MQTTDiscovery {
	if broker == "" {
		broker = "tcp://broker.emqx.io:1883"
	}
	if topicPrefix == "" {
		topicPrefix = "peerdrive/v1"
	}
	if clientID == "" {
		clientID = fmt.Sprintf("peerdrive-disc-%d", time.Now().UnixNano())
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &MQTTDiscovery{
		broker:       broker,
		topicPrefix:  strings.Trim(topicPrefix, "/"),
		clientID:     clientID,
		onPeer:       onPeer,
		announceTick: 60 * time.Second,
		announce:     make(map[string]bool),
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
	}
}

// nodeTopic 返回分片 topic：peerdrive/v1/{collectionHash}/nodes
func (d *MQTTDiscovery) nodeTopic(hash string) string {
	return d.topicPrefix + "/" + hash + "/nodes"
}

// announceMsg 节点 announce 消息体。
type announceMsg struct {
	PeerID string `json:"peerId"`
	TS     int64  `json:"ts"`
}

// Start 连接 broker 并发布/订阅分片 topic。异步重连由 paho 内部处理。
func (d *MQTTDiscovery) Start(collections []string) {
	opts := mqtt.NewClientOptions().
		AddBroker(d.broker).
		SetClientID(d.clientID).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetOnConnectHandler(func(c mqtt.Client) {
			// 断线重连后重新订阅（paho 不保留旧订阅）
			for _, h := range collections {
				h = strings.TrimSpace(h)
				if !hashutil.IsStrictSHA256(h) {
					continue
				}
				c.Subscribe(d.nodeTopic(h), 0, d.onMessage)
			}
		})
	d.client = mqtt.NewClient(opts)
	if tok := d.client.Connect(); tok.WaitTimeout(15*time.Second) && tok.Error() != nil {
		log.LogWarn("mqtt: connect %s failed: %v", d.broker, tok.Error())
	}
	go d.loop()
}

// onMessage 收到对端 announce 后回调 onPeer。
// M15：公共 broker 上任何人都能发任意 payload——限制 payload 大小（64KB）
// 与 peerID 长度（128），防异常大消息/超长 id 打爆内存或污染互联状态。
func (d *MQTTDiscovery) onMessage(_ mqtt.Client, msg mqtt.Message) {
	raw := msg.Payload()
	if len(raw) > 64<<10 {
		return
	}
	var a announceMsg
	if err := json.Unmarshal(raw, &a); err != nil || a.PeerID == "" || len(a.PeerID) > 128 {
		return
	}
	// 迟到 announce（peer id 未知归属集合）也上报，由调用方去重
	d.onPeer(a.PeerID)
}

// Announce 发布本节点 peer id 到集合分片。
// 幂等：同一 peer+集合只发一次上线 announce + 定时心跳。
func (d *MQTTDiscovery) Announce(peerID string, collections []string) {
	for _, h := range collections {
		h = strings.TrimSpace(h)
		if !hashutil.IsStrictSHA256(h) {
			continue
		}
		d.announceOnce(peerID, h)
	}
}

func (d *MQTTDiscovery) announceOnce(peerID, hash string) {
	key := peerID + "/" + hash
	d.mu.Lock()
	if d.announce[key] {
		d.mu.Unlock()
		return
	}
	d.announce[key] = true
	d.mu.Unlock()
	d.publish(peerID, hash)
}

func (d *MQTTDiscovery) publish(peerID, hash string) {
	if d.client == nil || !d.client.IsConnected() {
		return
	}
	payload, _ := json.Marshal(announceMsg{PeerID: peerID, TS: time.Now().Unix()})
	d.client.Publish(d.nodeTopic(hash), 0, false, payload)
}

// loop 定时心跳 announce（防 broker 清理 + 通知迟到节点）。
func (d *MQTTDiscovery) loop() {
	defer close(d.done)
	t := time.NewTicker(d.announceTick)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			d.mu.Lock()
			keys := make([]string, 0, len(d.announce))
			for k := range d.announce {
				keys = append(keys, k)
			}
			d.mu.Unlock()
			for _, k := range keys {
				parts := strings.SplitN(k, "/", 2)
				if len(parts) == 2 {
					d.publish(parts[0], parts[1])
				}
			}
		case <-d.ctx.Done():
			return
		}
	}
}

// Stop 断开 broker。
func (d *MQTTDiscovery) Stop() {
	d.cancel()
	<-d.done
	if d.client != nil && d.client.IsConnected() {
		d.client.Disconnect(100)
	}
}
