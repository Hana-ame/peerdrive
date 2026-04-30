// Package hashutil 提供 SHA256 哈希值格式验证工具。
// IsValidSHA256(s) 判断字符串是否为 64 位十六进制小写哈希值。
// 验证步骤：转小写 → 检查长度是否为 64 → hex.DecodeString 解码验证。

package hashutil

import (
	"encoding/hex"
	"strings"

	"github.com/ipfs/go-cid"
	mh "github.com/multiformats/go-multihash"
)

// IsValidSHA256 判断字符串是否为有效的 64 字符十六进制 SHA256 哈希值。
func IsValidSHA256(s string) bool {
	s = strings.ToLower(s)
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// SHA256ToCID 将 64 字符十六进制 SHA256 哈希值转换为 CIDv1（base32 编码）。
// 例如 "bafkreihk7nxx..."。输入无效时返回空字符串。
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
