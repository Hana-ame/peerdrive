package config

// 注：本文件属于 legacy 代码（见 doc/LEGACY.md，待删/待迁移）的测试，未逐一标注发现背景；「发现背景」规范对新代码生效。

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoad_Defaults(t *testing.T) {
	// 确保测试的是默认值，不受外部环境变量影响
	os.Unsetenv("PEERDRIVE_P2P_ENABLE")
	os.Unsetenv("PEERDRIVE_BT_DHT_ENABLE")
	os.Unsetenv("PEERDRIVE_STORAGE_ENABLE")
	cfg := Load()

	assert.Equal(t, "3000", cfg.Port)
	assert.Equal(t, "./storage", cfg.StorageDir)
	assert.Equal(t, true, cfg.StorageEnable)

	assert.Equal(t, "stun:stun.l.google.com:19302", cfg.WebRTCSTUNServer)
	assert.Equal(t, "", cfg.WebRTCTURNServer)
}

func TestGetEnv_DefaultWhenNotSet(t *testing.T) {
	os.Unsetenv("TEST_GET_ENV_KEY")
	assert.Equal(t, "fallback", getEnv("TEST_GET_ENV_KEY", "fallback"))
}

func TestGetEnv_ReturnsEnvValue(t *testing.T) {
	os.Setenv("TEST_GET_ENV_KEY", "custom-value")
	defer os.Unsetenv("TEST_GET_ENV_KEY")
	assert.Equal(t, "custom-value", getEnv("TEST_GET_ENV_KEY", "fallback"))
}

func TestGetEnvBool_Default(t *testing.T) {
	os.Unsetenv("TEST_GET_ENV_BOOL_KEY")
	assert.Equal(t, true, getEnvBool("TEST_GET_ENV_BOOL_KEY", true))
	assert.Equal(t, false, getEnvBool("TEST_GET_ENV_BOOL_KEY", false))
}

func TestGetEnvBool_ParsesTrueValues(t *testing.T) {
	os.Setenv("TEST_GET_ENV_BOOL_KEY", "true")
	defer os.Unsetenv("TEST_GET_ENV_BOOL_KEY")
	assert.Equal(t, true, getEnvBool("TEST_GET_ENV_BOOL_KEY", false))

	os.Setenv("TEST_GET_ENV_BOOL_KEY", "1")
	assert.Equal(t, true, getEnvBool("TEST_GET_ENV_BOOL_KEY", false))
}

func TestGetEnvBool_ParsesFalseValues(t *testing.T) {
	os.Setenv("TEST_GET_ENV_BOOL_KEY", "false")
	defer os.Unsetenv("TEST_GET_ENV_BOOL_KEY")
	assert.Equal(t, false, getEnvBool("TEST_GET_ENV_BOOL_KEY", true))

	os.Setenv("TEST_GET_ENV_BOOL_KEY", "0")
	assert.Equal(t, false, getEnvBool("TEST_GET_ENV_BOOL_KEY", true))
}
