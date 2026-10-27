// Package apperr scrubs driver/OS/socket internals out of error strings that
// are about to cross the HTTP boundary as 4xx/502 client-visible messages.
//
// Why this package exists (Issue #395, red-team R2, information disclosure):
// the controller layer (internal/controller/errors.go) answers 4xx/502 with
// err.Error() for client feedback. On this codebase that text is dominated by
// SQLite driver messages ("UNIQUE constraint failed: collections.collection_name",
// "database is locked", "no such table: file_index", "attempt to write a readonly
// database"), OS path errors ("open /srv/peerdrive/peerdrive.db: permission
// denied") and raw TCP socket errors ("read tcp 10.0.0.4:51022->203.0.113.7:443:
// i/o timeout"). Handing those verbatim to an unauthenticated caller leaks the
// schema, constraint names, filesystem layout and network topology.
//
// Sanitize is deliberately a heuristic allow-list-of-scrub-patterns: anything
// that does NOT match a driver/OS/socket pattern is passed through untouched so
// intentional domain validation messages ("invalid visibility", "passcode
// required", "collection not found") keep reaching the client. See the pattern
// tables in Sanitize for the exact scrub/allow split.
package apperr

import "strings"

// scrubPatterns are driver/OS/socket substrings whose presence means the whole
// error must be replaced by the caller's fallback. Kept as one table so the
// audit split (scrub vs allow) is a single read.
var scrubPatterns = []string{
	// SQLite driver internals (schema, constraint names, lock state).
	"UNIQUE constraint",
	"database is locked",
	"no such table",
	"readonly",
	// OS path errors: Go PathError text starts with the syscall name
	// ("open /abs/path: ..."); "permission denied" is the tail of many
	// PathError / syscall errors regardless of the operation name.
	"open ",
	"permission denied",
	// Raw TCP socket errors.
	"read tcp",
	"i/o timeout",
	"connection refused",
}

// Sanitize returns fallback when err's text matches any driver/OS/socket
// pattern, and err.Error() otherwise. A nil err is treated as nothing to say
// and returns fallback (the caller already decided the request failed).
//
// Why substring matching instead of errors.As: the errors here are mostly
// plain errors.New / fmt.Errorf("... %w") strings from the SQL driver and std
// library, not typed error values, so type assertion would miss the majority.
func Sanitize(err error, fallback string) string {
	if err == nil {
		return fallback
	}
	msg := err.Error()
	for _, pat := range scrubPatterns {
		if strings.Contains(msg, pat) {
			return fallback
		}
	}
	return msg
}

// Common application error codes for structured error responses (Issue #435).
const (
	CodeInternal        = "INTERNAL_ERROR"
	CodeInvalidArgument = "INVALID_ARGUMENT"
	CodeNotFound        = "NOT_FOUND"
	CodeUnauthorized    = "UNAUTHORIZED"
	CodeForbidden       = "FORBIDDEN"
	CodeConflict        = "CONFLICT"
)

// AppError represents a typed application error with code, HTTP status, and safe client message.
// The JSON key is "error" to maintain 100% backward compatibility with existing client error parsing.
type AppError struct {
	Code       string `json:"code,omitempty"`
	Message    string `json:"error"`
	HTTPStatus int    `json:"-"`
	Details    any    `json:"details,omitempty"`
}

// Error implements the standard error interface.
func (e *AppError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// New creates a new typed AppError.
func New(httpStatus int, code string, msg string) *AppError {
	return &AppError{
		HTTPStatus: httpStatus,
		Code:       code,
		Message:    msg,
	}
}

// Wrap creates an AppError by sanitizing an underlying error against driver/OS/socket internals.
func Wrap(httpStatus int, code string, err error, fallback string) *AppError {
	return &AppError{
		HTTPStatus: httpStatus,
		Code:       code,
		Message:    Sanitize(err, fallback),
	}
}

