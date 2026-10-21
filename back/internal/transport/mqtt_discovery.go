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

// MQTTDiscovery chunked-room discovery based on a public broker.
// Pattern: topics sharded by collection hash (peerdrive/v1/{hash}/nodes), nodes only
// subscribe to chunks they care about — subscription count and message volume scale with
// collections; public brokers can support large scale (global single topic fan-out is the
// bottleneck; sharding removes the limit).
//
// Extensibility: discovery only exchanges "node peer id"; actual transport still goes through
// PeerJS cloud signaling + WebRTC direct connection; changing signaling/transport doesn't
// affect this component.
type MQTTDiscovery struct {
	broker       string
	topicPrefix  string
	clientID     string
	onPeer       func(peerID string) // New node discovery callback (dedup guaranteed by caller)
	announceTick time.Duration

	client   mqtt.Client
	mu       sync.Mutex
	announce map[string]bool // already announced peer ids (same id not sent again)
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
}

// NewMQTTDiscovery creates the discovery component. onPeer is called when a new node is
// discovered.
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

// nodeTopic returns the sharded topic: peerdrive/v1/{collectionHash}/nodes
func (d *MQTTDiscovery) nodeTopic(hash string) string {
	return d.topicPrefix + "/" + hash + "/nodes"
}

// announceMsg node announce message body.
type announceMsg struct {
	PeerID string `json:"peerId"`
	TS     int64  `json:"ts"`
}

// Start connects to broker and publishes/subscribes to chunked topics. Asynchronous reconnection
// is handled internally by paho.
func (d *MQTTDiscovery) Start(collections []string) {
	opts := mqtt.NewClientOptions().
		AddBroker(d.broker).
		SetClientID(d.clientID).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetOnConnectHandler(func(c mqtt.Client) {
			// Re-subscribe after reconnection (paho doesn't preserve old subscriptions)
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

// onMessage calls onPeer after receiving a peer announce.
// M15: anyone on the public broker can send any payload — limit payload size (64KB) and
// peerID length (128) to prevent abnormal large messages / oversized ids from exhausting
// memory or polluting interconnection state.
func (d *MQTTDiscovery) onMessage(_ mqtt.Client, msg mqtt.Message) {
	raw := msg.Payload()
	if len(raw) > 64<<10 {
		return
	}
	var a announceMsg
	if err := json.Unmarshal(raw, &a); err != nil || a.PeerID == "" || len(a.PeerID) > 128 {
		return
	}
	// Late announce (peer id unknown collection) also reported, caller deduplicates
	d.onPeer(a.PeerID)
}

// Announce publishes this node's peer id to collection chunks.
// Idempotent: same peer+collection only sends one online announce + periodic heartbeat.
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

// loop periodic heartbeat announce (prevents broker cleanup + notifies late-arriving nodes).
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

// Stop disconnects from broker.
func (d *MQTTDiscovery) Stop() {
	d.cancel()
	<-d.done
	if d.client != nil && d.client.IsConnected() {
		d.client.Disconnect(100)
	}
}
