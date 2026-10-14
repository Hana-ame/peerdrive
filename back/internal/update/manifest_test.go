package update

import (
	"strings"
	"testing"
)

func TestParseManifest(t *testing.T) {
	valid := `{
  "version": "v0.3.1",
  "assets": [
    {"name": "peerdrive-linux-amd64", "os": "linux", "arch": "amd64",
     "url": "https://example.com/dl/peerdrive-linux-amd64", "sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
    {"name": "peerdrive-windows-amd64.exe", "os": "windows", "arch": "amd64",
     "url": "https://example.com/dl/peerdrive-windows-amd64.exe", "sha256": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
  ]
}`
	m, err := ParseManifest([]byte(valid))
	if err != nil {
		t.Fatalf("ParseManifest(valid) = %v", err)
	}
	if m.Version != "v0.3.1" || len(m.Assets) != 2 {
		t.Fatalf("ParseManifest(valid) = %+v", m)
	}

	bad := []string{
		`{"version": "", "assets": []}`,
		`{"version": "v0.3.1", "assets": []}`,
		`{"version": "v0.3.1", "assets": [{"name": "x", "url": "", "sha256": "y"}]}`,
		`not json`,
	}
	for _, s := range bad {
		if _, err := ParseManifest([]byte(s)); err == nil {
			t.Errorf("ParseManifest(%q) expected error, got nil", s)
		}
	}
}

func TestAssetName(t *testing.T) {
	cases := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "peerdrive-linux-amd64"},
		{"linux", "arm64", "peerdrive-linux-arm64"},
		{"darwin", "arm64", "peerdrive-darwin-arm64"},
		{"windows", "amd64", "peerdrive-windows-amd64.exe"},
		{"windows", "arm64", "peerdrive-windows-arm64.exe"},
	}
	for _, c := range cases {
		if got := AssetName(c.goos, c.goarch); got != c.want {
			t.Errorf("AssetName(%q, %q) = %q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

func TestFindAsset(t *testing.T) {
	m := &Manifest{Version: "v0.3.1", Assets: []Asset{
		{Name: "peerdrive-linux-amd64", OS: "linux", Arch: "amd64", URL: "u1", SHA256: "s1"},
		{Name: "peerdrive-windows-amd64.exe", OS: "windows", Arch: "amd64", URL: "u2", SHA256: "s2"},
	}}
	a, err := m.FindAsset("linux", "amd64")
	if err != nil || a.URL != "u1" {
		t.Fatalf("FindAsset(linux/amd64) = %+v, %v", a, err)
	}
	if _, err := m.FindAsset("freebsd", "amd64"); err == nil {
		t.Error("FindAsset(freebsd) expected error")
	} else if !strings.Contains(err.Error(), "available") {
		t.Errorf("FindAsset error should list available assets, got: %v", err)
	}
}
