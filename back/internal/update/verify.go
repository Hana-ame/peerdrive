package update

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// ParseChecksums 解析 sha256sum -c 兼容的清单（release.yml 生成的 checksums.txt）：
// 每行 "<hex>  <filename>"（两个空格）或 "<hex> <filename>"（一个空格）。
// 不匹配格式的行（注释、空行、GNU 输出里夹在 hex 和文件名之间的小写标记行）
// 被跳过。返回 filename → hex 的映射。
func ParseChecksums(data []byte) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		// sha256sum 标准输出用两个空格分隔；"-c" 校验回显的行带 "OK" 等标记，
		// 这里只认「以 64 位 hex 开头的行」。
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		hexPart, name := fields[0], fields[len(fields)-1]
		if !isHex64(hexPart) {
			continue
		}
		// sha256sum -b 输出在文件名前带 '*'（二进制模式标记），剥掉。
		out[strings.TrimPrefix(name, "*")] = hexPart
	}
	return out
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// VerifySHA256 校验 path 文件的 sha256 是否等于 wantHex（hex 字符串）。
func VerifySHA256(path, wantHex string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != strings.ToLower(strings.TrimSpace(wantHex)) {
		return fmt.Errorf("sha256 mismatch: file %s = %s, want %s", path, got, wantHex)
	}
	return nil
}
