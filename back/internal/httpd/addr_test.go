package httpd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNormalizePort 端口拼法的历史兼容：部署脚本里 PORT 既可能是裸端口
// ("8080")，也可能是 ":8080" 或 "127.0.0.1:8080"。
//
// 发现背景：这套拼法是从 cmd/peerdrive 搬进来的，测试跟着代码一起搬——
// 搬之前它直接测 main 包里的未导出 normalizePort，现在测的是导出版本。
func TestNormalizePort(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ":4000"},
		{":8080", ":8080"},
		{"8080", ":8080"},
		{"127.0.0.1:3000", "127.0.0.1:3000"},
		{"127.0.0.1", "127.0.0.1:4000"},
		{":0", ":0"},
		{"0.0.0.0:9000", "0.0.0.0:9000"},
	}
	for _, c := range cases {
		t.Run("NormalizePort("+c.in+")", func(t *testing.T) {
			assert.Equal(t, c.want, NormalizePort(c.in))
		})
	}
}

// TestNormalizePortNeverDoublesPrefix 已经带主机的字符串不能被再拼一次前缀。
// 这是回归点：HOST=127.0.0.1 PORT=4000 不能被拼成 "127.0.0.1:127.0.0.1:4000"。
func TestNormalizePortNeverDoublesPrefix(t *testing.T) {
	cases := []string{
		"127.0.0.1:3000",
		"0.0.0.0:8080",
		":8080",
		"[::1]:4000",
		"host.example:8080",
	}
	for _, c := range cases {
		if got := NormalizePort(c); got != c {
			t.Errorf("NormalizePort(%q) = %q, want unchanged", c, got)
		}
	}
}

// TestIsLoopback 空 host 表示「所有网卡」，不是 loopback——这个不对称是
// validateAuthStartup 拒绝「非 loopback + 无鉴权」的依据。
func TestIsLoopback(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"", false},          // 空 = 0.0.0.0 = 所有网卡
		{"0.0.0.0", false},   // 显式所有网卡
		{"127.0.0.1", true},  // IPv4 loopback
		{"localhost", false}, // 主机名不是 IP，判不了 —— 保守当作非 loopback
		{"192.168.1.10", false},
		{"::1", true},              // IPv6 loopback
		{"::", false},              // 未指定地址
		{"not-an-ip", false},       // 解析失败同样返回 false
		{"::ffff:127.0.0.1", true}, // IPv4-mapped loopback
	}
	for _, c := range cases {
		t.Run("IsLoopback("+c.host+")", func(t *testing.T) {
			assert.Equal(t, c.want, IsLoopback(c.host))
		})
	}
}

// TestJoinHostPort 搬自 serverapp.ListenAddr 与 cmd/peerdrive 的拼接处。
//
// 发现背景：PEERDRIVE_HOST 可能是裸 IPv6（"::1"），不外包一层
// net.JoinHostPort 就会被监听成非法地址串（"::1:8080" 缺方括号），
// 合并模式与单独 serve 还会各拼一份、拼出不同的串。
func TestJoinHostPort(t *testing.T) {
	cases := []struct {
		host, port, want string
	}{
		{"", "8080", ":8080"},
		{"0.0.0.0", "8080", "0.0.0.0:8080"},
		{"127.0.0.1", "9000", "127.0.0.1:9000"},
		{"::1", "8080", "[::1]:8080"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, JoinHostPort(c.host, c.port), "JoinHostPort(%q, %q)", c.host, c.port)
	}
}
