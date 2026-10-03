// Package hashutil provides SHA256 hash value format validation utilities.
// IsValidSHA256(s) determines whether a string is a 64-character lowercase hexadecimal hash value.
// Validation steps: lowercase → check length is 64 → verify with hex.DecodeString.

package hashutil

import (
	"encoding/hex"
	"strings"

	"github.com/ipfs/go-cid"
	mh "github.com/multiformats/go-multihash"
)

// IsValidSHA256 determines whether a string is a valid 64-character hexadecimal SHA256 hash value.
func IsValidSHA256(s string) bool {
	s = strings.ToLower(s)
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// SHA256ToCID converts a 64-character hexadecimal SHA256 hash value to CIDv1 (base32 encoded).
// For example "bafkreihk7nxx...". Returns an empty string for invalid input.
func SHA256ToCID(sha256hex string) string {
	if len(sha256hex) != 64 {
		return ""
	}
	raw, err := hex.DecodeString(sha256hex)
	if err != nil {
		return ""
	}
	mhash, err := mh.Encode(raw, mh.SHA2_256)
	if err != nil {
		return ""
	}
	c := cid.NewCidV1(cid.Raw, mhash)
	return c.String()
}

// CIDToSHA256 parses a CID string (CIDv1/CIDv0) into a 64-character SHA-256 hexadecimal digest.
// Only supports sha2-256 multihash; returns an empty string on mismatch.
func CIDToSHA256(cidStr string) string {
	c, err := cid.Decode(cidStr)
	if err != nil {
		return ""
	}
	mhash := c.Hash()
	dec, err := mh.Decode(mhash)
	if err != nil {
		return ""
	}
	if dec.Code != mh.SHA2_256 || dec.Length != 32 || len(dec.Digest) != 32 {
		return ""
	}
	return hex.EncodeToString(dec.Digest)
}
// IsStrictSHA256 performs strict validation: accepts only 64-character lowercase hexadecimal SHA256 (used for
// transport-layer peer hash: allowing uppercase would cause lookups against the lowercase table to miss and broaden behavior,
// effectively relaxing input validation).
func IsStrictSHA256(s string) bool {
	if s == "" || len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
