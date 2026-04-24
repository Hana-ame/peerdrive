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