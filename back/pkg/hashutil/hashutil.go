// Package hashutil 负责 SHA256 ↔ IPFS CID 的格式转换（多形式 hash 编解码）。
//
// 校验语义（IsValidSHA256 / IsStrictSHA256）**不在本包**：它已迁入
// peerdrive/internal/hashmap——校验与 CID 转换是两件事，分开后 hashmap
// 可以只依赖标准库（不背 ipfs 依赖），本包则只保留 CID 相关的第三方依赖。
// 下面两个函数保留为薄委托，目的是**消除此前两处各自实现的重复定义**
// （同一语义两份实现，改一处漏一处）；20+ 个调用点无需改动即可继续编译，
// 逐步迁移到 hashmap 即可。
package hashutil

import (
	"encoding/hex"

	"github.com/ipfs/go-cid"
	mh "github.com/multiformats/go-multihash"

	"peerdrive/internal/hashmap"
)

// IsValidSHA256 determines whether a string is a valid 64-character hexadecimal
// SHA256 hash value (case-insensitive). 委托 hashmap：校验语义的唯一定义处。
func IsValidSHA256(s string) bool { return hashmap.IsValidSHA256(s) }

// IsStrictSHA256 performs strict validation: accepts only 64-character lowercase
// hexadecimal SHA256 (used for transport-layer peer hash: allowing uppercase would
// cause lookups against the lowercase table to miss, effectively relaxing input
// validation). 委托 hashmap：校验语义的唯一定义处。
func IsStrictSHA256(s string) bool { return hashmap.IsStrictSHA256(s) }

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
// Only supports sha-2-256 multihash; returns an empty string on mismatch.
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
