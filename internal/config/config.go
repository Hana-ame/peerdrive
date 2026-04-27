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

	AllowedOrigins      string
	PublicAccessDomain  string
	RegistrationServer  string

	P2PEnable            bool
	P2PListenAddr        string
	P2PListenAddrV6      string
	P2PBootstrapPeer     string
	P2PMDNSEnable        bool
	P2PRelayEnable       bool
	P2PRelayMode         RelayMode
	P2PStaticRelays      string
	P2PHolePunch         bool
	P2PPublicReachable   bool
	P2PAutoNAT           bool
	P2PNATPortMap        bool

	BTDHTEnabled    bool
	BTDHTListenAddr string

	STUNServer string
	TURNServer string
	TURNUser   string
	TURNPass   string
}

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

func DefaultRootPath() string {
	if runtime.GOOS == "windows" {
		return "C:\\"
	}
	return "/"
}

func Load() *Config {
	return &Config{
		Port:                getEnv("PORT", "3000"),
		StorageDir:          getEnv("PEERDRIVE_STORAGE", "./storage"),
		StorageEnable:       getEnvBool("PEERDRIVE_STORAGE_ENABLE", true),
		AllowedOrigins:      getEnv("PEERDRIVE_ALLOWED_ORIGINS", "http://localhost:5173,https://peerdrive.moonchan.xyz,https://peerdrive.pages.dev"),
		PublicAccessDomain:  getEnv("PEERDRIVE_PUBLIC_DOMAIN", ""),
		RegistrationServer:  getEnv("PEERDRIVE_REG_SERVER", ""),
		P2PEnable:           getEnvBool("PEERDRIVE_P2P_ENABLE", true),
		P2PListenAddr:       getEnv("PEERDRIVE_P2P_LISTEN", "/ip4/0.0.0.0/tcp/0"),
		P2PListenAddrV6:     getEnv("PEERDRIVE_P2P_LISTEN_V6", "/ip6/::/tcp/0"),
		P2PBootstrapPeer:    getEnv("PEERDRIVE_BOOTSTRAP_PEER", ""),
		P2PMDNSEnable:       getEnvBool("PEERDRIVE_MDNS_ENABLE", true),
		P2PRelayEnable:      getEnvBool("PEERDRIVE_RELAY_ENABLE", false),
		P2PRelayMode:        parseRelayMode(getEnv("PEERDRIVE_RELAY_MODE", "client")),
		P2PStaticRelays:     getEnv("PEERDRIVE_STATIC_RELAYS", ""),
		P2PHolePunch:        getEnvBool("PEERDRIVE_HOLE_PUNCH", true),
		P2PPublicReachable:  getEnvBool("PEERDRIVE_PUBLIC_REACHABLE", false),
		P2PAutoNAT:          getEnvBool("PEERDRIVE_AUTO_NAT", true),
		P2PNATPortMap:       getEnvBool("PEERDRIVE_NAT_PORTMAP", false),
		BTDHTEnabled:        getEnvBool("PEERDRIVE_BT_DHT_ENABLE", true),
		BTDHTListenAddr:     getEnv("PEERDRIVE_BT_DHT_LISTEN", ":6881"),
		STUNServer:          getEnv("PEERDRIVE_STUN_SERVER", "stun:stun.moonchan.xyz:3478"),
		TURNServer:          getEnv("PEERDRIVE_TURN_SERVER", ""),
		TURNUser:            getEnv("PEERDRIVE_TURN_USER", ""),
		TURNPass:            getEnv("PEERDRIVE_TURN_PASS", ""),
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
