package webrtc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNewManifestSumsOnlyFiles(t *testing.T) {
	// 目录的 Size 必须被排除：真实场景里目录的 Size 是 st.Size()，
	// 在 ext4 / NTFS 上是 4096 之类的块大小，累加进去总额就偏了。
	entries := []FileInfo{
		{Path: "a.txt", Size: 100},
		{Path: "dir", IsDir: true, Size: 4096},
		{Path: "b.bin", Size: 250},
	}
	m := NewManifest("/root", entries)

	if m.Root != "/root" {
		t.Errorf("Root = %q", m.Root)
	}
	if m.Count != 3 {
		t.Errorf("Count = %d，want 3（目录也要计数）", m.Count)
	}
	if m.Total != 350 {
		t.Errorf("Total = %d，want 350（不应含目录的 4096）", m.Total)
	}
	if m.At <= 0 {
		t.Errorf("At 应该是 Unix 毫秒时间戳，实得 %d", m.At)
	}
}

func TestNewManifestEmpty(t *testing.T) {
	m := NewManifest("", nil)
	if m.Count != 0 || m.Total != 0 {
		t.Errorf("空清单 Count/Total = %d/%d", m.Count, m.Total)
	}
}

func TestManifestJSONShape(t *testing.T) {
	// 浏览器 demo 直接读这些键名，改名就是协议破坏。
	m := NewManifest("/demo", []FileInfo{{Path: "x", Size: 1}})
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, k := range []string{"root", "entries", "total_bytes", "count", "at"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("清单 JSON 缺少键 %q", k)
		}
	}
}

func TestCheckOKOmittedWhenNil(t *testing.T) {
	// OK 用指针，nil 时必须完全不出现：发出的 check 是「请对方校验」，
	// 带上 ok=false 会被误读成「校验没通过」。
	var c Check
	c.Algo = "sha256"
	c.Digest = "abc"
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := raw["ok"]; ok {
		t.Error("OK 为 nil 时不应出现在 JSON 里")
	}

	yes := true
	c.OK = &yes
	b, _ = json.Marshal(c)
	raw = map[string]any{}
	_ = json.Unmarshal(b, &raw)
	if raw["ok"] != true {
		t.Errorf("OK=&true 应序列化为 true，实得 %v", raw["ok"])
	}
}

func TestHashBytesEmptyAndKnown(t *testing.T) {
	if got := HashBytes(nil); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("空串 sha256 = %q", got)
	}
	// "abc" 的 sha256 是标准测试向量。
	if got := HashBytes([]byte("abc")); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf(`"abc" sha256 = %q`, got)
	}
}

func TestHashFileMatchesHashBytes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.bin")
	body := []byte("hello webrtc\n")
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}

	digest, size, err := HashFile(p)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	if size != int64(len(body)) {
		t.Errorf("size = %d，want %d", size, len(body))
	}
	if digest != HashBytes(body) {
		t.Errorf("HashFile 与 HashBytes 不一致: %q vs %q", digest, HashBytes(body))
	}
}

func TestHashFileMissing(t *testing.T) {
	if _, _, err := HashFile(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("不存在的文件应返回错误")
	}
}

func TestInfoOfFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "doc.md")
	body := []byte("# hi\n")
	if err := os.WriteFile(p, body, 0o640); err != nil {
		t.Fatal(err)
	}

	fi, err := InfoOf(p, "docs/doc.md")
	if err != nil {
		t.Fatalf("InfoOf: %v", err)
	}
	if fi.Path != "docs/doc.md" {
		t.Errorf("Path 应沿用传入的 rel，实得 %q", fi.Path)
	}
	if fi.Name != "doc.md" {
		t.Errorf("Name = %q", fi.Name)
	}
	if fi.Size != int64(len(body)) {
		t.Errorf("Size = %d", fi.Size)
	}
	if fi.IsDir {
		t.Error("IsDir 应为 false")
	}
	if fi.SHA != HashBytes(body) {
		t.Errorf("SHA = %q", fi.SHA)
	}
	if fi.MTime <= 0 {
		t.Errorf("MTime 应是 Unix 微秒，实得 %d", fi.MTime)
	}
	// 权限位在不同平台上未必完全保真（Windows 不区分 0640），只校验非零。
	if fi.Mode == 0 {
		t.Error("Mode 不应为 0")
	}
}

func TestInfoOfDirSkipsHash(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	fi, err := InfoOf(sub, "sub")
	if err != nil {
		t.Fatalf("InfoOf: %v", err)
	}
	if !fi.IsDir {
		t.Error("IsDir 应为 true")
	}
	// 目录不算 sha，否则每个空目录都是同一个空串摘要，噪声且无意义。
	if fi.SHA != "" {
		t.Errorf("目录不应有 SHA，实得 %q", fi.SHA)
	}
	if fi.Size != 0 {
		t.Errorf("目录 Size 应为 0，实得 %d", fi.Size)
	}
}

func TestInfoOfMissing(t *testing.T) {
	if _, err := InfoOf(filepath.Join(t.TempDir(), "nope"), "nope"); err == nil {
		t.Error("不存在的路径应返回错误")
	}
}

func TestControlActionsDistinct(t *testing.T) {
	// 这些字符串是对端约定，重复或空值都会让控制信号歧义。
	all := []string{
		ActionBegin, ActionProgress, ActionPause, ActionResume,
		ActionCancel, ActionEnd, ActionPing, ActionPong,
	}
	seen := map[string]bool{}
	for _, a := range all {
		if a == "" {
			t.Error("控制动作不应为空串")
		}
		if seen[a] {
			t.Errorf("控制动作重复: %q", a)
		}
		seen[a] = true
	}
	// 这两个在 cmd 里按字面比较，改了就对不上。
	if ActionPing != "ping" || ActionPong != "pong" {
		t.Errorf("ping/pong 字面值变了: %q / %q", ActionPing, ActionPong)
	}
}
