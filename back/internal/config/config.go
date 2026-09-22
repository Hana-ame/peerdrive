// Package config 从环境变量加载全部配置项（端口、存储目录、PeerJS/WebRTC、BT DHT、转发等）。
// Load() 读取 PEERDRIVE_* 系列环境变量并返回 *Config。
package config

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

// 项目公共信令（所有人都连它，不需要自己部署）。
// 环境变量可以覆盖（自托管 / 内网调试用），但**默认值必须是这一对**——
// 早先默认是 PeerJS 公共云 0.peerjs.com/peerjs，导致"照默认跑"的节点和面板
// 互相找不到（分属两个信令，discover 也拿不到任何节点）。
const (
	DefaultSignalHost = "peersignal.moonchan.xyz"
	DefaultSignalPort = "443"
	DefaultSignalKey  = "pd-signal-b9447b406828e500"
	// DefaultDiscoverURL 公共信令的发现 API（announce + 节点列表）。
	DefaultDiscoverURL = "https://peersignal.moonchan.xyz"
)

type Config struct {
	Port          string
	StorageDir    string
	StorageEnable bool

	AllowedOrigins     string
	PublicAccessDomain string
	RegistrationServer string
	NodeAuthToken      string // persistent auth token for node identity

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
	// 默认指向项目自己的公共信令 peersignal.moonchan.xyz（不是 PeerJS 公共云）。
	// 环境变量仍然可覆盖（自托管 / 内网调试），但教程不教改它。
	PeerJSHost   string // PEERDRIVE_PEERJS_HOST, 默认 peersignal.moonchan.xyz
	PeerJSPort   string // PEERDRIVE_PEERJS_PORT, 默认 443
	PeerJSKey    string // PEERDRIVE_PEERJS_KEY, 默认 pd-signal-b9447b406828e500
	PeerJSID     string // PEERDRIVE_PEERJS_ID, 空则生成 peerdrive-<random>
	PeerJSSecure bool   // PEERDRIVE_PEERJS_SECURE, 默认 true
	PeerJSPeers  string // PEERDRIVE_PEERJS_PEERS, 逗号分隔对端节点 peer id，启动自动互联

	// PeerPSK 节点访问预共享密钥（PEERDRIVE_PSK，默认空 = 开放模式）。
	//
	// 设了之后：任何对端必须先在这条连接上出示**同样的**密钥，本节点才会应答
	// 它的数据请求（req/share/list/create/upload/info/delete/sync/转发）。
	// 没设 = 完全向后兼容的老行为（谁连上都服务）。
	//
	// 边界：它验的是「对端知不知道这个密钥」，不是「对方是谁」——
	// 不做身份、不做授权分级，所有持钥者对节点有同等访问权。
	// 想按人区分权限得走注册服务器鉴权（doc/modules/auth），不是这里。
	PeerPSK string

	MQTTEnable      bool   // PEERDRIVE_MQTT_ENABLE, 默认 false（MQTT 分片房间发现）
	MQTTBroker      string // PEERDRIVE_MQTT_BROKER, 默认 tcp://broker.emqx.io:1883
	MQTTTopicPref   string // PEERDRIVE_MQTT_TOPIC_PREFIX, 默认 peerdrive/v1
	MQTTCollections string // PEERDRIVE_MQTT_COLLECTIONS, 逗号分隔关注的 collection hash 分片
	DiscoverURL     string // PEERDRIVE_DISCOVER_URL, 自托管信令服务器的发现 API（设置后优先于 MQTT）

	// DiscoverPresence 节点级「存在房间」发现（PEERDRIVE_DISCOVER_PRESENCE，默认 true）。
	// 打开后节点额外加入一个固定的公共房间，使「没有任何共享 collection hash」的
	// 两个节点也能互相发现并直连（互联层的基础能力）。
	// 关闭后回归纯内容分片发现（只有声明了同一 collection 的节点才会碰面）。
	// 隐私取舍：开 = 发现服务端与同房间节点能看到本节点在线及其 peerId；
	// 关 = 仅在共享集合的房间里可见。仅对 HTTP 发现（自托管信令）生效，
	// 公共 MQTT broker 不加存在房间（公共 broker 上做全局房间等于广播）。
	DiscoverPresence bool

	// URLSourceTemplate 统一 source 体系的 URL 源模板（PEERDRIVE_URL_SOURCE_TEMPLATE）。
	// 空则不注册 url source。%s = sha256 hash；含 %d 时（%d 依次为 offset,size）
	// 声明 CapStream（Range 分片），否则 CapFile（整体拉取）。
	// 示例: https://example.com/ipfs/%s 或 https://example.com/f/%s?off=%d&size=%d
	URLSourceTemplate string

	DownloadDir         string
	MaxPeers            int
	DownloadOrder       string
	DownloadTimeoutSecs int

	ForwardRules string // PEERDRIVE_FORWARD_RULES: "key1:8080,key2:8443"（转发授权白名单,key 即凭证,配置文件建议 chmod 600）

	// ── 节点共享范围（PEERDRIVE_SHARE_*，doc/NETDISK.md M2 / ROADMAP 阶段 5）──
	//
	// ShareEnable 共享总开关（PEERDRIVE_SHARE_ENABLE，默认 **false**）。
	// 为什么默认关：对端经 share 帧能列举本节点"提供了什么"，开启即等于对外
	// 公开内容清单。默认全盘分享是隐私事故，必须运营者显式开启。
	ShareEnable bool
	// ShareCollections 对外共享的合集（PEERDRIVE_SHARE_COLLECTIONS，逗号分隔）：
	// 64hex 合集 hash，或 "all" = 所有 public 合集。受限/私有合集即使写在这里
	// 也会被跳过（无身份可校验，第 7 阶段前无法安全共享）。
	ShareCollections string
	// ShareDirs 对外共享的目录（PEERDRIVE_SHARE_DIRS，逗号分隔）。
	// 语义：file_index 中路径落在这些目录下的文件进入共享清单。
	// 空 = 不按目录共享（只有合集共享）。仅相对/绝对路径前缀匹配，
	// 真正的越权读仍由 file_index.IsPathAllowed（上传根目录）兜底。
	ShareDirs string
	// ShareFriends 好友节点 ID（PEERDRIVE_SHARE_FRIENDS，逗号分隔）：
	// private 级别的内容放行给这些节点（见 model.LevelPrivate）。
	//
	// 注意：peer id 由对端自报，信令不校验身份。好友名单只在**已通过 PSK 准入**
	// 的连接上才有意义——没设 PSK 时任何人都能连上并自称是好友。要强身份得等
	// 账号体系（ROADMAP 第 7 阶段）。这里只是运行时状态的初值，之后在管理台改。
	ShareFriends string
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
		// 子域名通配：支持 "*.example.com" 和 "https://*.example.com" 两种写法
		if strings.HasPrefix(o, "*.") || strings.Contains(o, "://*.") {
			pattern := o
			if idx := strings.Index(o, "://*"); idx >= 0 {
				pattern = o[idx+3:] // "https://*.example.com" → "*.example.com"
			}
			if strings.HasSuffix(origin, pattern[1:]) {
				return true
			}
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
		AllowedOrigins:     getEnv("PEERDRIVE_ALLOWED_ORIGINS", "http://localhost:5173,https://peerdrive.moonchan.xyz,https://peerdrive.pages.dev,https://*.pages.dev"),
		PublicAccessDomain: getEnv("PEERDRIVE_PUBLIC_DOMAIN", ""),
		RegistrationServer: getEnv("PEERDRIVE_REG_SERVER", ""),
		NodeAuthToken:      getEnv("PEERDRIVE_AUTH_TOKEN", ""),
		RegServerURL:       getEnv("PEERDRIVE_REG_SERVER_URL", ""),
		MaxUploadBytes:     getEnvInt64("PEERDRIVE_MAX_UPLOAD_BYTES", 100*1024*1024),     // 100MB default
		MaxUploadBytesAnon: getEnvInt64("PEERDRIVE_MAX_UPLOAD_ANON_BYTES", 10*1024*1024), // 10MB for anonymous
		BTDHTEnabled:       getEnvBool("PEERDRIVE_BT_DHT_ENABLE", false), // 默认禁用：DHT 初始化阻塞启动，按需手动启用

		BTDHTListenAddr:   getEnv("PEERDRIVE_BT_DHT_LISTEN", ":6881"),
		IPFSGatewayEnable: getEnvBool("PEERDRIVE_IPFS_GATEWAY_ENABLE", true),
		IPFSGateways:      getEnv("PEERDRIVE_IPFS_GATEWAYS", "https://ipfs.io,https://cloudflare-ipfs.com,https://dweb.link"),

		WebRTCSTUNServer: getEnv("PEERDRIVE_WEBRTC_STUN", "stun:stun.l.google.com:19302"),
		WebRTCTURNServer: getEnv("PEERDRIVE_WEBRTC_TURN", ""),

		PeerJSEnable: getEnvBool("PEERDRIVE_PEERJS_ENABLE", true),
		PeerJSHost:   getEnv("PEERDRIVE_PEERJS_HOST", DefaultSignalHost),
		PeerJSPort:   getEnv("PEERDRIVE_PEERJS_PORT", DefaultSignalPort),
		PeerJSKey:    getEnv("PEERDRIVE_PEERJS_KEY", DefaultSignalKey),
		PeerJSID:     getEnv("PEERDRIVE_PEERJS_ID", ""),
		PeerJSSecure: getEnvBool("PEERDRIVE_PEERJS_SECURE", true),
		PeerJSPeers:  getEnv("PEERDRIVE_PEERJS_PEERS", ""),
		PeerPSK:      getEnv("PEERDRIVE_PSK", ""),

		MQTTEnable:        getEnvBool("PEERDRIVE_MQTT_ENABLE", false),
		MQTTBroker:        getEnv("PEERDRIVE_MQTT_BROKER", "tcp://broker.emqx.io:1883"),
		MQTTTopicPref:     getEnv("PEERDRIVE_MQTT_TOPIC_PREFIX", "peerdrive/v1"),
		MQTTCollections:   getEnv("PEERDRIVE_MQTT_COLLECTIONS", ""),
		DiscoverURL:       getEnv("PEERDRIVE_DISCOVER_URL", DefaultDiscoverURL),
		DiscoverPresence:  getEnvBool("PEERDRIVE_DISCOVER_PRESENCE", true),
		URLSourceTemplate: getEnv("PEERDRIVE_URL_SOURCE_TEMPLATE", ""),

		DownloadDir: getEnv("PEERDRIVE_DOWNLOAD_DIR", "./downloads"),
		MaxPeers:    getEnvInt("PEERDRIVE_MAX_PEERS", 8),

		ShareEnable:      getEnvBool("PEERDRIVE_SHARE_ENABLE", false),
		ShareCollections: getEnv("PEERDRIVE_SHARE_COLLECTIONS", ""),
		ShareDirs:        getEnv("PEERDRIVE_SHARE_DIRS", ""),
		ShareFriends:     getEnv("PEERDRIVE_SHARE_FRIENDS", ""),

		DownloadOrder:       getEnv("PEERDRIVE_DOWNLOAD_ORDER", "local,ipfs,ipfsgw,btdht,http"),
		DownloadTimeoutSecs: getEnvInt("PEERDRIVE_DOWNLOAD_TIMEOUT", 30),

		ForwardRules: getEnv("PEERDRIVE_FORWARD_RULES", ""),
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
