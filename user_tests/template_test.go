package user_tests

import (
	"os"
	"testing"
)

// User Test Template / 用户 Go 测试模板
//
// Rules:
// 1. This test is formulated exclusively by the user.
// 2. AI Agents MUST NOT alter, comment out, or disable this test.
// 3. To run this test:
//      go test user_tests/template_test.go -v
//    Or run the whole user layer:
//      make test-user
func TestUser_Template(t *testing.T) {
	// Example assertion: check that required root Makefile exists
	if _, err := os.Stat("../Makefile"); err != nil {
		t.Fatalf("expected Makefile to exist: %v", err)
	}
}
