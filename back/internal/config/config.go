// Package config 从环境变量加载全部配置项（端口、存储目录、PeerJS/WebRTC、BT DHT、转发等）。
// Load() 读取 PEERDRIVE_* 系列环境变量并返回 *Config。
package config

import (
	"fmt"
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
	Port   string
	DBPath string // PEERDRIVE_DB_PATH：SQLite 元数据库路径（默认 ./peerdrive.db）

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

	PeerJSEnable bool // PEERDRIVE_PEERJS_ENABLE, 默认 true
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
	FolderMaxDepth      int // PEERDRIVE_FOLDER_MAX_DEPTH：register_folder 递归最大深度（默认 1=只扫当前目录）
	MaxPeers            int
	DownloadOrder       string
	DownloadTimeoutSecs int

	ForwardRules string // PEERDRIVE_FORWARD_RULES: "key1:8080,key2:8443"（转发授权白名单,key 即凭证,配置文件建议 chmod 600）

	// ── HTTP 加固（见 internal/router/middleware.go）──
	// RateLimitRPS 每 IP 请求速率上限（PEERDRIVE_RATE_LIMIT_RPS，0 = 不限）。
	// 默认 30：够管理台正常用（列表轮询 + 手动操作远不到这个量），又能挡住
	// "一个脚本刷接口"。上传/跨节点拉取这类要真花带宽的口子也一并受它保护。
	RateLimitRPS float64
	// DisableCSP 关闭 Content-Security-Policy（PEERDRIVE_CSP=off）。
	// 只在嵌入第三方页面/老浏览器兼容出问题时的逃生阀，默认开。
	DisableCSP bool
	// DisableSwagger 关闭 /swagger/*（PEERDRIVE_SWAGGER=off）。默认开：
	// 它会把全部端点与参数结构公开出来，公网部署等于送一份攻击地图。
	DisableSwagger bool
	// Host 监听地址（PEERDRIVE_HOST，默认空 = 监听所有网卡）。
	//
	// 为什么值得配：管理面（/ws/peer）没有账号体系，边界就是"谁能连到这个
	// 端口"。默认听 0.0.0.0 意味着同一局域网内的人都能连上并当管理员。
	// 只在本机用管理台的话，设成 127.0.0.1 是成本最低的一道墙。
	Host string

	// TrustedProxies 可信反向代理（PEERDRIVE_TRUSTED_PROXIES，逗号分隔 IP/CIDR）。
	//
	// 为什么必须显式配：gin 默认**信任所有**代理，ClientIP() 直接取
	// X-Forwarded-For——而这个头是客户端能伪造的，等于限流和日志里的 IP 全
	// 由攻击者填。空 = 只认 RemoteAddr（直连部署的正确选择）；反代后面务必
	// 填上那一跳的地址，否则所有人会被当成一个 IP 一起限流。
	TrustedProxies string

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
		DBPath:             getEnv("PEERDRIVE_DB_PATH", "./peerdrive.db"),
		StorageDir:         getEnv("PEERDRIVE_STORAGE", "./storage"),
		StorageEnable:      getEnvBool("PEERDRIVE_STORAGE_ENABLE", true),
		AllowedOrigins:     getEnv("PEERDRIVE_ALLOWED_ORIGINS", "http://localhost:5173,https://peerdrive.moonchan.xyz,https://peerdrive.pages.dev,https://*.pages.dev"),
		PublicAccessDomain: getEnv("PEERDRIVE_PUBLIC_DOMAIN", ""),
		RegistrationServer: getEnv("PEERDRIVE_REG_SERVER", ""),
		NodeAuthToken:      getEnv("PEERDRIVE_AUTH_TOKEN", ""),
		RegServerURL:       getEnv("PEERDRIVE_REG_SERVER_URL", ""),
		MaxUploadBytes:     getEnvInt64("PEERDRIVE_MAX_UPLOAD_BYTES", 100*1024*1024),     // 100MB default
		MaxUploadBytesAnon: getEnvInt64("PEERDRIVE_MAX_UPLOAD_ANON_BYTES", 10*1024*1024), // 10MB for anonymous
		BTDHTEnabled:       getEnvBool("PEERDRIVE_BT_DHT_ENABLE", false),                 // 默认禁用：DHT 初始化阻塞启动，按需手动启用

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
		FolderMaxDepth: getEnvInt("PEERDRIVE_FOLDER_MAX_DEPTH", 1),
		MaxPeers:    getEnvInt("PEERDRIVE_MAX_PEERS", 8),

		ShareEnable:      getEnvBool("PEERDRIVE_SHARE_ENABLE", false),
		ShareCollections: getEnv("PEERDRIVE_SHARE_COLLECTIONS", ""),
		ShareDirs:        getEnv("PEERDRIVE_SHARE_DIRS", ""),
		ShareFriends:     getEnv("PEERDRIVE_SHARE_FRIENDS", ""),

		DownloadOrder:       getEnv("PEERDRIVE_DOWNLOAD_ORDER", "local,ipfs,ipfsgw,btdht,http"),
		DownloadTimeoutSecs: getEnvInt("PEERDRIVE_DOWNLOAD_TIMEOUT", 30),

		ForwardRules: getEnv("PEERDRIVE_FORWARD_RULES", ""),

		RateLimitRPS:    getEnvFloat("PEERDRIVE_RATE_LIMIT_RPS", 30),
		DisableCSP:      os.Getenv("PEERDRIVE_CSP") == "off",
		DisableSwagger:  os.Getenv("PEERDRIVE_SWAGGER") == "off",
		Host:            getEnv("PEERDRIVE_HOST", ""),
		TrustedProxies:  getEnv("PEERDRIVE_TRUSTED_PROXIES", ""),
	}
}

// Validate 在启动期把"配错了但不会报错"的配置挡掉。
//
// 为什么要它：环境变量是字符串，拼错一个字符不会让进程失败，只会让行为跑偏
// （PORT=300o → 监听失败；PEERDRIVE_STORAGE= 空 → 文件落进当前工作目录），
// 等到用户发现时，落点已经是一堆找不回来的文件了。
// 原则是"快速失败"：启动期一次说清，好过运行期慢慢错。
func Validate(c *Config) error {
	var errs []string

	if n, err := strconv.Atoi(c.Port); err != nil || n <= 0 || n > 65535 {
		errs = append(errs, fmt.Sprintf("PORT=%q 不是合法端口（1-65535）", c.Port))
	}
	if strings.TrimSpace(c.DBPath) == "" {
		errs = append(errs, "PEERDRIVE_DB_PATH 不能为空")
	}
	if strings.TrimSpace(c.StorageDir) == "" {
		errs = append(errs, "PEERDRIVE_STORAGE 不能为空")
	}
	if strings.TrimSpace(c.DownloadDir) == "" {
		errs = append(errs, "PEERDRIVE_DOWNLOAD_DIR 不能为空")
	}
	if p := strings.TrimSpace(c.PeerJSPort); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n <= 0 || n > 65535 {
			errs = append(errs, fmt.Sprintf("PEERDRIVE_PEERJS_PORT=%q 不是合法端口", p))
		}
	}
	if c.MaxPeers <= 0 {
		errs = append(errs, fmt.Sprintf("PEERDRIVE_MAX_PEERS=%d 必须为正数", c.MaxPeers))
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("配置校验失败：\n  - %s", strings.Join(errs, "\n  - "))
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

func getEnvFloat(key string, defaultVal float64) float64 {
	if val, ok := os.LookupEnv(key); ok {
		f, err := strconv.ParseFloat(val, 64)
		if err == nil && f >= 0 {
			return f
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
