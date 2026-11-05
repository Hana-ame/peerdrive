package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestWriteBlobAppendsPerStream(t *testing.T) {
	dir := t.TempDir()

	// 同一个 stream 的多个数据块必须追加到同一个文件：
	// e2e 里的 8 MiB 大块会被拆成上百片，每片都 writeBlob 一次。
	chunks := [][]byte{[]byte("hello "), []byte("webrtc "), []byte("world")}
	for _, c := range chunks {
		if err := writeBlob(dir, 7, c); err != nil {
			t.Fatalf("writeBlob: %v", err)
		}
	}

	got, err := os.ReadFile(filepath.Join(dir, "stream-7.bin"))
	if err != nil {
		t.Fatalf("读取落地文件: %v", err)
	}
	if string(got) != "hello webrtc world" {
		t.Errorf("文件内容 = %q，want %q", got, "hello webrtc world")
	}
}

func TestWriteBlobSeparatesStreams(t *testing.T) {
	dir := t.TempDir()

	if err := writeBlob(dir, 1, []byte("AAA")); err != nil {
		t.Fatal(err)
	}
	if err := writeBlob(dir, 2, []byte("BBB")); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		stream uint32
		want   string
	}{{1, "AAA"}, {2, "BBB"}} {
		p := filepath.Join(dir, "stream-"+strconv.FormatUint(uint64(c.stream), 10)+".bin")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("stream %d: %v", c.stream, err)
		}
		if string(b) != c.want {
			t.Errorf("stream %d = %q，want %q", c.stream, b, c.want)
		}
	}
}

func TestWriteBlobBinarySafe(t *testing.T) {
	// 文件本体是任意二进制，含 NUL 与高位字节都不能被截断或改写。
	dir := t.TempDir()
	body := []byte{0x00, 0xff, 0x00, 0x7f, 0x80, 0x00}
	if err := writeBlob(dir, 3, body); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "stream-3.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Errorf("二进制内容被破坏: %v", got)
	}
}

func TestWriteBlobMissingDir(t *testing.T) {
	// 目录不存在时要返回错误，不能静默吞掉——否则「落盘失败」会被
	// 误当成「对端没发数据」。
	p := filepath.Join(t.TempDir(), "no-such-dir")
	if err := writeBlob(p, 1, []byte("x")); err == nil {
		t.Error("目录不存在时应返回错误")
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is longer than ten", 10, "this is lo…"},
		{"", 5, ""},
	}
	for _, c := range cases {
		if got := truncate(c.in, c.n); got != c.want {
			t.Errorf("truncate(%q, %d) = %q，want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestTruncateMarksCut(t *testing.T) {
	// 截断必须留下可见标记，否则日志里看不出这条是被砍过的。
	got := truncate(strings.Repeat("a", 500), 400)
	if !strings.HasSuffix(got, "…") {
		t.Error("截断后应带省略号")
	}
	// 400 个 a + 1 个省略号（UTF-8 三字节）。
	if len([]rune(got)) != 401 {
		t.Errorf("长度 = %d 个字符，want 401", len([]rune(got)))
	}
}

func TestPretty(t *testing.T) {
	if got := pretty([]byte(`{"a":1}`)); got != `{"a":1}` {
		t.Errorf("pretty = %q", got)
	}
	// 与 truncate 共用 400 的上限，避免日志里塞进整个 manifest。
	long := strings.Repeat("x", 1000)
	if got := pretty([]byte(long)); len([]rune(got)) != 401 {
		t.Errorf("pretty 未按 400 截断，长度 %d", len([]rune(got)))
	}
}
