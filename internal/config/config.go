package config

import "os"

type Config struct {
	Port      string
	Storage   string
	P2PEnable bool
	Version   string
}

func Load() *Config {
	return &Config{
		Port:      getEnv("PORT", "3000"),
		Storage:   getEnv("PEERDRIVE_STORAGE", "./storage"),
		P2PEnable: getEnvBool("PEERDRIVE_P2P_ENABLE"),
		Version:   "3.0-base",
	}
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getEnvBool(k string) bool {
	v := os.Getenv(k)
	return v == "1" || v == "true" || v == "yes"
}
