//go:build golden

package transport

// Golden-vector generator. Writes the raw wire bytes the current code
// produces so a refactor can be proven byte-for-byte equivalent.
//
// 发现背景：wsconn 拆分要求行为逐字节不变；本测试用来重新生成 fixture。
// 默认只打印不写盘，避免误覆盖已提交的 testdata/ws_vectors.json——
// 必须显式传 -out。
//
// Usage from back/:
//
//	go test -tags "nosqlite golden" -run TestGenerateGolden \
//	  -args -out=/tmp/after.json ./internal/transport/

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Declared at package level: the test binary parses flags before any test
// function runs, so a flag registered inside the function is too late.
var goldenOut = flag.String("out", "", "where to write the fixture (empty = print to stdout, write nothing)")

func TestGenerateGolden(t *testing.T) {
	out := *goldenOut
	vecs := captureAll(t)

	b, err := json.MarshalIndent(goldenFile{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		GoVersion:   runtime.Version(),
		GorillaVer:  "v1.5.3",
		Vectors:     vecs,
	}, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if out == "" {
		t.Log("no -out given: printed the fixture to stdout and wrote nothing")
		_, _ = os.Stdout.Write(b)
		return
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(out, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", out, err)
	}
	t.Logf("wrote %d vectors to %s", len(vecs), out)
}
