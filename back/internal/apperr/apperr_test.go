package apperr

// Unit tests for Sanitize, the 4xx/502 error-scrubbing heuristic (Issue #395).
//
// 发现背景：controller 层 70+ 个调用点把 err.Error() 当 4xx/502 的客户端可见
// 文案，而本代码库的 err 文本被 SQLite 驱动串（表名/约束名/锁状态）、OS 绝对
// 路径、原始 TCP socket 串主导——这些会让未认证调用方重构出 schema 与磁盘布局。
// 这组用例把「哪些串必须清洗、哪些必须放行」钉死，防止将来往 scrubPatterns
// 里加宽泛子串误伤领域校验文案（如 "invalid visibility"、访问清单/passcode 提示）。

import (
	"errors"
	"testing"
)

func TestSanitize_ScrubsDriverInternals(t *testing.T) {
	cases := []string{
		"UNIQUE constraint failed: collections.collection_name",
		"database is locked",
		"no such table: file_index",
		"attempt to write a readonly database",
		"open /srv/peerdrive/storage/ab/abcdef: permission denied",
		"open /home/lumin/peerdrive/peerdrive.db: no such file or directory",
		"read tcp 10.0.0.4:51022->203.0.113.7:443: i/o timeout",
		"dial tcp 203.0.113.7:443: connection refused",
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			if got := Sanitize(errors.New(msg), "fallback-msg"); got != "fallback-msg" {
				t.Fatalf("Sanitize(%q) = %q, want the fallback", msg, got)
			}
		})
	}
}

func TestSanitize_PassesThroughDomainMessages(t *testing.T) {
	cases := []string{
		"invalid visibility",
		"access_list required",
		"invalid access policy",
		"passcode required",
		"psk required",
		"collection not found",
		"path required",
		"invalid request body",
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			if got := Sanitize(errors.New(msg), "fallback-msg"); got != msg {
				t.Fatalf("Sanitize(%q) = %q, want the original message to pass through", msg, got)
			}
		})
	}
}

// TestSanitize_NilErrReturnsFallback — writeClientError / sanitizeErr call
// sites can pass a nil err on defensive paths; Sanitize must not panic on
// err.Error() and should answer the generic fallback.
func TestSanitize_NilErrReturnsFallback(t *testing.T) {
	if got := Sanitize(nil, "fallback-msg"); got != "fallback-msg" {
		t.Fatalf("Sanitize(nil) = %q, want the fallback", got)
	}
}

// TestSanitize_WrappedErrorsStillScrubbed — call sites wrap driver errors with
// fmt.Errorf("... %w", err); the driver text survives in err.Error(), so the
// scrub must still trigger.
func TestSanitize_WrappedErrorsStillScrubbed(t *testing.T) {
	inner := errors.New("UNIQUE constraint failed: collections.collection_name")
	wrapped := errors.New("create failed: " + inner.Error())
	if got := Sanitize(wrapped, "fallback-msg"); got != "fallback-msg" {
		t.Fatalf("Sanitize(wrapped driver error) = %q, want the fallback", got)
	}
}
