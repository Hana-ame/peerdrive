package config

import (
	"os"
)

type Config struct {
	StorageDir    string
	StorageEnable bool
}

func Load() *Config {
	return &Config{
		StorageDir:    getEnv("PEERDRIVE_STORAGE", "./storage"),
		StorageEnable: getEnv("PEERDRIVE_STORAGE_ENABLE", "true") == "true",
	}
}

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}