package router

import (
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestMain sets gin's mode once for the whole package.
//
// Why this exists: gin.SetMode writes a package-level variable, so calling it from
// several tests that run in parallel is itself a data race — the tests in this
// package are parallel now that they no longer share mutable state, which is
// exactly the property the refactor was for. Setting it once here keeps the
// package race-clean even when a maintainer later runs `go test -race`.
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}
