package update

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hexDigest(t *testing.T, data []byte) string {
	t.Helper()
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func TestParseChecksums(t *testing.T) {
	sum := hexDigest(t, []byte("aaa"))
	data := fmt.Sprintf("%s  peerdrive-linux-amd64\n%s peerdrive-darwin-arm64\n%s *peerdrive-linux-arm64\n\nnot-a-checksum line\n",
		sum, sum, sum)
	got := ParseChecksums([]byte(data))
	if len(got) != 3 {
		t.Fatalf("ParseChecksums parsed %d entries, want 3: %v", len(got), got)
	}
	if got["peerdrive-linux-amd64"] != sum {
		t.Errorf("two-space line name mismatch: %v", got)
	}
	if got["peerdrive-darwin-arm64"] != sum {
		t.Errorf("one-space line name mismatch: %v", got)
	}
	if got["peerdrive-linux-arm64"] != sum {
		t.Errorf("binary-mode (*) line should strip marker: %v", got)
	}
	if _, ok := got["not-a-checksum"]; ok {
		t.Errorf("junk line should be ignored: %v", got)
	}
}

func TestVerifySHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.bin")
	if err := os.WriteFile(path, []byte("hello update"), 0o644); err != nil {
		t.Fatal(err)
	}
	good := hexDigest(t, []byte("hello update"))
	bad := strings.Repeat("0", 64)

	if err := VerifySHA256(path, good); err != nil {
		t.Errorf("VerifySHA256(good) = %v", err)
	}
	if err := VerifySHA256(path, strings.ToUpper(good)); err != nil {
		t.Errorf("VerifySHA256(upper hex) = %v", err)
	}
	if err := VerifySHA256(path, bad); err == nil {
		t.Error("VerifySHA256(bad) expected error")
	}
	if err := VerifySHA256(filepath.Join(dir, "missing.bin"), good); err == nil {
		t.Error("VerifySHA256(missing file) expected error")
	}
}
