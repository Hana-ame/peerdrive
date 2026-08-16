// Package config 从环境变量加载全部配置项（端口、存储目录、P2P、中继、WebRTC、WebDAV 等）。
// Load() 读取 PEERDRIVE_* 系列环境变量并返回 *Config。
package config

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

type RelayMode string

const (
	RelayOff    RelayMode = "off"
	RelayClient RelayMode = "client"
	RelayServer RelayMode = "server"
)

type Config struct {
	Port          string
	StorageDir    string
	StorageEnable bool

	AllowedOrigins     string
	PublicAccessDomain string
	RegistrationServer string
	NodeAuthToken      string // persistent auth token for node identity

	P2PEnable          bool
	P2PListenAddr      string
	P2PListenAddrV6    string
	P2PBootstrapPeer   string
	P2PMDNSEnable      bool
	P2PRelayEnable     bool
	P2PRelayMode       RelayMode
	P2PStaticRelays    string
	P2PHolePunch       bool
	P2PPublicReachable bool
	P2PReadOnly        bool
	P2PAutoNAT         bool
	P2PNATPortMap      bool

	BTDHTEnabled    bool
	BTDHTListenAddr string

	IPFSGatewayEnable bool
	IPFSGateways      string

	RegServerURL string

	MaxUploadBytes     int64 // 0 = unlimited
	MaxUploadBytesAnon int64

	WebRTCSTUNServer string
	WebRTCTURNServer string

	PeerJSEnable bool   // PEERDRIVE_PEERJS_ENABLE, 默认 true
	PeerJSHost   string // PEERDRIVE_PEERJS_HOST, 默认 0.peerjs.com（公共云信令）
	PeerJSPort   string // PEERDRIVE_PEERJS_PORT, 默认 443
	PeerJSKey    string // PEERDRIVE_PEERJS_KEY, 默认 peerjs
	PeerJSID     string // PEERDRIVE_PEERJS_ID, 空则生成 peerdrive-<random>
	PeerJSSecure bool   // PEERDRIVE_PEERJS_SECURE, 默认 true
	PeerJSPeers  string // PEERDRIVE_PEERJS_PEERS, 逗号分隔对端节点 peer id，启动自动互联

	MQTTEnable      bool   // PEERDRIVE_MQTT_ENABLE, 默认 false（MQTT 分片房间发现）
	MQTTBroker      string // PEERDRIVE_MQTT_BROKER, 默认 tcp://broker.emqx.io:1883
	MQTTTopicPref   string // PEERDRIVE_MQTT_TOPIC_PREFIX, 默认 peerdrive/v1
	MQTTCollections string // PEERDRIVE_MQTT_COLLECTIONS, 逗号分隔关注的 collection hash 分片
	DiscoverURL     string // PEERDRIVE_DISCOVER_URL, 自托管信令服务器的发现 API（设置后优先于 MQTT）

	// URLSourceTemplate 统一 source 体系的 URL 源模板（PEERDRIVE_URL_SOURCE_TEMPLATE）。
	// 空则不注册 url source。%s = sha256 hash；含 %d 时（%d 依次为 offset,size）
	// 声明 CapStream（Range 分片），否则 CapFile（整体拉取）。
	// 示例: https://example.com/ipfs/%s 或 https://example.com/f/%s?off=%d&size=%d
	URLSourceTemplate string

	DownloadDir         string
	MaxPeers            int
	DownloadOrder       string
	DownloadTimeoutSecs int

	RelayStorageMB int
	RelayVersion   string

	WebDAVEnable bool
	ForwardRules string // PEERDRIVE_FORWARD_RULES: "key1:8080,key2:8443"（转发授权白名单,key 即凭证,配置文件建议 chmod 600）

	IPFSCompatEnable bool   // PEERDRIVE_IPFS_COMPAT, 默认 false（opt-in）
	IPFSBlockstore   string // PEERDRIVE_IPFS_BLOCKSTORE, 默认 "<storageDir>/ipfs-blocks"

	P2PKeyFile string // libp2p 私钥持久化路径；空且有 AuthToken 时默认 <StorageDir>/libp2p.key
}

// IsOriginAllowed 检查给定的 Origin 是否在允许列表中，支持通配符（*）和子域名通配（*.example.com）。
func (c *Config) IsOriginAllowed(origin string) bool {
	if c.AllowedOrigins == "*" || c.AllowedOrigins == "" {
		return true
	}
	for _, o := range strings.Split(c.AllowedOrigins, ",") {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if o == "*" || strings.EqualFold(origin, o) {
			return true
		}
		if strings.HasPrefix(o, "*.") && strings.HasSuffix(origin, o[1:]) {
			return true
		}
	}
	return false
}

// DefaultRootPath 返回当前操作系统的根路径（Windows 为 C:\，其他为 /）。
func DefaultRootPath() string {
	if runtime.GOOS == "windows" {
		return "C:\\"
	}
	return "/"
}

// Load 读取 PEERDRIVE_* 环境变量并返回完整配置结构体，未设置的项使用默认值。
func Load() *Config {
	return &Config{
		Port:               getEnv("PORT", "3000"),
		StorageDir:         getEnv("PEERDRIVE_STORAGE", "./storage"),
		StorageEnable:      getEnvBool("PEERDRIVE_STORAGE_ENABLE", true),
		AllowedOrigins:     getEnv("PEERDRIVE_ALLOWED_ORIGINS", "http://localhost:5173,https://peerdrive.moonchan.xyz,https://peerdrive.pages.dev"),
		PublicAccessDomain: getEnv("PEERDRIVE_PUBLIC_DOMAIN", ""),
		RegistrationServer: getEnv("PEERDRIVE_REG_SERVER", ""),
		NodeAuthToken:      getEnv("PEERDRIVE_AUTH_TOKEN", ""),
		P2PEnable:          getEnvBool("PEERDRIVE_P2P_ENABLE", true),
		P2PListenAddr:      getEnv("PEERDRIVE_P2P_LISTEN", "/ip4/0.0.0.0/tcp/0"),
		P2PListenAddrV6:    getEnv("PEERDRIVE_P2P_LISTEN_V6", "/ip6/::/tcp/0"),
		P2PBootstrapPeer:   getEnv("PEERDRIVE_BOOTSTRAP_PEER", ""),
		P2PMDNSEnable:      getEnvBool("PEERDRIVE_MDNS_ENABLE", true),
		P2PRelayEnable:     getEnvBool("PEERDRIVE_RELAY_ENABLE", false),
		P2PRelayMode:       parseRelayMode(getEnv("PEERDRIVE_RELAY_MODE", "client")),
		P2PStaticRelays:    getEnv("PEERDRIVE_STATIC_RELAYS", ""),
		P2PHolePunch:       getEnvBool("PEERDRIVE_HOLE_PUNCH", true),
		RegServerURL:       getEnv("PEERDRIVE_REG_SERVER_URL", ""),
		MaxUploadBytes:     getEnvInt64("PEERDRIVE_MAX_UPLOAD_BYTES", 100*1024*1024),     // 100MB default
		MaxUploadBytesAnon: getEnvInt64("PEERDRIVE_MAX_UPLOAD_ANON_BYTES", 10*1024*1024), // 10MB for anonymous
		P2PPublicReachable: getEnvBool("PEERDRIVE_PUBLIC_REACHABLE", false),
		P2PAutoNAT:         getEnvBool("PEERDRIVE_AUTO_NAT", true),
		P2PNATPortMap:      getEnvBool("PEERDRIVE_NAT_PORTMAP", false),
		BTDHTEnabled:       getEnvBool("PEERDRIVE_BT_DHT_ENABLE", true),

		BTDHTListenAddr:   getEnv("PEERDRIVE_BT_DHT_LISTEN", ":6881"),
		IPFSGatewayEnable: getEnvBool("PEERDRIVE_IPFS_GATEWAY_ENABLE", true),
		IPFSGateways:      getEnv("PEERDRIVE_IPFS_GATEWAYS", "https://ipfs.io,https://cloudflare-ipfs.com,https://dweb.link"),

		WebRTCSTUNServer: getEnv("PEERDRIVE_WEBRTC_STUN", "stun:stun.l.google.com:19302"),
		WebRTCTURNServer: getEnv("PEERDRIVE_WEBRTC_TURN", ""),

		PeerJSEnable: getEnvBool("PEERDRIVE_PEERJS_ENABLE", true),
		PeerJSHost:   getEnv("PEERDRIVE_PEERJS_HOST", "0.peerjs.com"),
		PeerJSPort:   getEnv("PEERDRIVE_PEERJS_PORT", "443"),
		PeerJSKey:    getEnv("PEERDRIVE_PEERJS_KEY", "peerjs"),
		PeerJSID:     getEnv("PEERDRIVE_PEERJS_ID", ""),
		PeerJSSecure: getEnvBool("PEERDRIVE_PEERJS_SECURE", true),
		PeerJSPeers:  getEnv("PEERDRIVE_PEERJS_PEERS", ""),

		MQTTEnable:        getEnvBool("PEERDRIVE_MQTT_ENABLE", false),
		MQTTBroker:        getEnv("PEERDRIVE_MQTT_BROKER", "tcp://broker.emqx.io:1883"),
		MQTTTopicPref:     getEnv("PEERDRIVE_MQTT_TOPIC_PREFIX", "peerdrive/v1"),
		MQTTCollections:   getEnv("PEERDRIVE_MQTT_COLLECTIONS", ""),
		DiscoverURL:       getEnv("PEERDRIVE_DISCOVER_URL", ""),
		URLSourceTemplate: getEnv("PEERDRIVE_URL_SOURCE_TEMPLATE", ""),

		DownloadDir: getEnv("PEERDRIVE_DOWNLOAD_DIR", "./downloads"),
		MaxPeers:    getEnvInt("PEERDRIVE_MAX_PEERS", 8),

		DownloadOrder:       getEnv("PEERDRIVE_DOWNLOAD_ORDER", "local,ipfs,ipfsgw,btdht,http"),
		DownloadTimeoutSecs: getEnvInt("PEERDRIVE_DOWNLOAD_TIMEOUT", 30),

		RelayStorageMB: getEnvInt("PEERDRIVE_RELAY_STORAGE_MB", 0),
		RelayVersion:   getEnv("PEERDRIVE_RELAY_VERSION", "peerdrive/1.0.0"),

		WebDAVEnable: getEnvBool("PEERDRIVE_WEBDAV_ENABLE", true),
		ForwardRules: getEnv("PEERDRIVE_FORWARD_RULES", ""),

		IPFSCompatEnable: getEnvBool("PEERDRIVE_IPFS_COMPAT", false),
		IPFSBlockstore:   getEnv("PEERDRIVE_IPFS_BLOCKSTORE", ""),
		P2PKeyFile:       getEnv("PEERDRIVE_P2P_KEY_FILE", ""),
		P2PReadOnly:      getEnvBool("PEERDRIVE_P2P_READ_ONLY", false),
	}
}

func parseRelayMode(s string) RelayMode {
	switch s {
	case "server":
		return RelayServer
	case "off":
		return RelayOff
	default:
		return RelayClient
	}
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	if val, ok := os.LookupEnv(key); ok {
		b, err := strconv.ParseBool(val)
		if err == nil {
			return b
		}
	}
	return defaultVal
}

func getEnvInt64(key string, defaultVal int64) int64 {
	if val, ok := os.LookupEnv(key); ok {
		n, err := strconv.ParseInt(val, 10, 64)
		if err == nil && n > 0 {
			return n
		}
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val, ok := os.LookupEnv(key); ok {
		n, err := strconv.Atoi(val)
		if err == nil && n > 0 {
			return n
		}
	}
	return defaultVal
}
