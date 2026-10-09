package urlguard

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGuardExternalURL 发现背景：防止任意外部输入的 URL 进行 SSRF 攻击，统一拦截私网/回环/localhost/带认证信息的 URL。
func TestGuardExternalURL(t *testing.T) {
	assert.NoError(t, GuardExternalURL("https://example.com/file.zip"))
	assert.NoError(t, GuardExternalURL("http://example.com/a.bin"))

	// Scheme
	assert.Error(t, GuardExternalURL("ftp://example.com/file.zip"))
	assert.Error(t, GuardExternalURL("file:///etc/passwd"))

	// User info
	assert.Error(t, GuardExternalURL("http://user:pass@example.com/file.zip"))

	// Localhost
	assert.Error(t, GuardExternalURL("http://localhost:8080/test"))
	assert.Error(t, GuardExternalURL("http://127.0.0.1:8080/test"))
	assert.Error(t, GuardExternalURL("http://[::1]:8080/test"))

	// Private ranges
	assert.Error(t, GuardExternalURL("http://10.0.0.1/test"))
	assert.Error(t, GuardExternalURL("http://192.168.1.1/test"))
	assert.Error(t, GuardExternalURL("http://172.16.0.1/test"))
	assert.Error(t, GuardExternalURL("http://169.254.169.254/latest/meta-data"))
}

// TestGuardPullIP 发现背景：验证直接 IP 判定的边界条件，包括 IPv4-mapped IPv6。
func TestGuardPullIP(t *testing.T) {
	assert.Error(t, GuardPullIP(net.ParseIP("127.0.0.1")))
	assert.Error(t, GuardPullIP(net.ParseIP("::1")))
	assert.Error(t, GuardPullIP(net.ParseIP("10.1.2.3")))
	assert.Error(t, GuardPullIP(net.ParseIP("172.20.1.1")))
	assert.Error(t, GuardPullIP(net.ParseIP("192.168.0.1")))
	assert.Error(t, GuardPullIP(net.ParseIP("169.254.1.1")))
	assert.Error(t, GuardPullIP(net.ParseIP("::ffff:127.0.0.1")))
	assert.Error(t, GuardPullIP(net.ParseIP("::ffff:10.0.0.1")))

	assert.NoError(t, GuardPullIP(net.ParseIP("8.8.8.8")))
	assert.NoError(t, GuardPullIP(net.ParseIP("1.1.1.1")))
}
