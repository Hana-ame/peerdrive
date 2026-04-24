// Package hashutil provides SHA256 hash validation utilities.
// Usage: hashutil.IsValidSHA256(s) returns true if s is a 64-char lowercase hex string.

package hashutil

import (
	"encoding/hex"
	"strings"
)

func IsValidSHA256(s string) bool {
	s = strings.ToLower(s)
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
