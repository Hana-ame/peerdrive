package config

import (
	"os"
	"strconv"
)

type Config struct {
	Port          string
	StorageDir    string
	StorageEnable bool
}

func Load() *Config {
	return &Config{
		Port:          getEnv("PORT", "3000"),
		StorageDir:    getEnv("PEERDRIVE_STORAGE", "./storage"),
		StorageEnable: getEnvBool("PEERDRIVE_STORAGE_ENABLE", true),
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