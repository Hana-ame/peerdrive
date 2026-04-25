package config

import (
	"os"
	"strconv"
)

type Config struct {
	Port          string
	StorageDir    string
	StorageEnable bool

	P2PEnable        bool
	P2PListenAddr    string
	P2PBootstrapPeer string
	P2PMDNSEnable    bool
	P2PRelayEnable   bool
}

func Load() *Config {
	return &Config{
		Port:             getEnv("PORT", "3000"),
		StorageDir:       getEnv("PEERDRIVE_STORAGE", "./storage"),
		StorageEnable:    getEnvBool("PEERDRIVE_STORAGE_ENABLE", true),
		P2PEnable:        getEnvBool("PEERDRIVE_P2P_ENABLE", true),
		P2PListenAddr:    getEnv("PEERDRIVE_P2P_LISTEN", "/ip4/0.0.0.0/tcp/0"),
		P2PBootstrapPeer: getEnv("PEERDRIVE_BOOTSTRAP_PEER", ""),
		P2PMDNSEnable:    getEnvBool("PEERDRIVE_MDNS_ENABLE", true),
		P2PRelayEnable:   getEnvBool("PEERDRIVE_RELAY_ENABLE", false),
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
