package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLoad_SwaggerFlagContract pins PEERDRIVE_SWAGGER's documented contract:
// default on, "off" disables (README.md / README.zh-CN.md flag tables).
//
// Regression guard: before 2026-10-14 the check was inverted
// (DisableSwagger: os.Getenv("PEERDRIVE_SWAGGER") == "on"), which made
// PEERDRIVE_SWAGGER=off *enable* the Swagger UI and =on disable it — so the
// escape hatch every README points at for public deployments did the opposite
// of what it promised. The unset case keeps the existing default (on).
func TestLoad_SwaggerFlagContract(t *testing.T) {
	t.Setenv("PEERDRIVE_SWAGGER", "off")
	assert.True(t, Load().DisableSwagger, "PEERDRIVE_SWAGGER=off must disable /swagger/*")

	t.Setenv("PEERDRIVE_SWAGGER", "on")
	assert.False(t, Load().DisableSwagger, "PEERDRIVE_SWAGGER=on must enable /swagger/*")

	os.Unsetenv("PEERDRIVE_SWAGGER")
	assert.False(t, Load().DisableSwagger, "unset keeps the documented default: swagger on")
}
