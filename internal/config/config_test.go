package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoad_Defaults(t *testing.T) {
	cfg := Load()

	assert.Equal(t, "3000", cfg.Port)
	assert.Equal(t, "./storage", cfg.StorageDir)
	assert.Equal(t, true, cfg.StorageEnable)
	assert.Equal(t, true, cfg.P2PEnable)
	assert.Equal(t, "/ip4/0.0.0.0/tcp/0", cfg.P2PListenAddr)
	assert.Equal(t, "", cfg.P2PBootstrapPeer)
	assert.Equal(t, true, cfg.P2PMDNSEnable)
	assert.Equal(t, false, cfg.P2PRelayEnable)
	assert.Equal(t, RelayClient, cfg.P2PRelayMode)
	assert.Equal(t, "", cfg.P2PStaticRelays)
	assert.Equal(t, true, cfg.P2PHolePunch)
	assert.Equal(t, false, cfg.P2PPublicReachable)
	assert.Equal(t, true, cfg.P2PAutoNAT)
	assert.Equal(t, false, cfg.P2PNATPortMap)

	assert.Equal(t, "stun:stun.moonchan.xyz:3478", cfg.STUNServer)
	assert.Equal(t, "", cfg.TURNServer)
	assert.Equal(t, "", cfg.TURNUser)
	assert.Equal(t, "", cfg.TURNPass)
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

func TestParseRelayMode_Server(t *testing.T) {
	assert.Equal(t, RelayServer, parseRelayMode("server"))
}

func TestParseRelayMode_Client(t *testing.T) {
	assert.Equal(t, RelayClient, parseRelayMode("client"))
}

func TestParseRelayMode_Off(t *testing.T) {
	assert.Equal(t, RelayOff, parseRelayMode("off"))
}

func TestParseRelayMode_UnknownDefaultsToClient(t *testing.T) {
	assert.Equal(t, RelayClient, parseRelayMode("unknown"))
	assert.Equal(t, RelayClient, parseRelayMode(""))
}
