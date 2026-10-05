package serverapp

// Tests for the startup-time "volume root" configuration guard (doc/NETDISK.md §11.3).
//
// Why this test exists: this kind of configuration **passes** every runtime
// boundary check -- when root is set to `/`, `/etc/passwd` genuinely "is inside
// the root", so pathutil.Within returning true is the correct behavior. The only
// place that can stop it is at startup, and startup logic is the easiest thing
// to delete during a refactor. So we test it as an API, rather than only testing
// pathutil.IsUnsafeRoot (which already has unit tests of its own).

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"peerdrive/internal/config"
)

func cfgWith(storage, download, shareDirs string) *config.Config {
	cfg := config.Load()
	cfg.StorageDir = storage
	cfg.DownloadDir = download
	cfg.ShareDirs = shareDirs
	return cfg
}

func TestCheckUnsafeRoots_DefaultConfigIsFine(t *testing.T) {
	// The default configuration (./storage, ./downloads, no share dirs) must never
	// be blocked, otherwise nobody's node can start.
	if err := checkUnsafeRoots(cfgWith("./storage", "./downloads", "")); err != nil {
		t.Fatalf("default config should not be rejected: %v", err)
	}
	if err := checkUnsafeRoots(cfgWith("/data/peerdrive/storage", "/data/peerdrive/dl", "/data/pub,/mnt/media")); err != nil {
		t.Fatalf("normal subdirectories should not be rejected: %v", err)
	}
}

func TestCheckUnsafeRoots_RejectsFilesystemRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this test case uses POSIX root '/'; see TestCheckUnsafeRoots_WindowsDriveRoot for the Windows branch")
	}
	cases := []struct {
		name string
		cfg  *config.Config
		want string // the config item that must be named in the error message
	}{
		{"storage=/", cfgWith("/", "/downloads", ""), "PEERDRIVE_STORAGE"},
		{"download=/", cfgWith("/storage", "/", ""), "PEERDRIVE_DOWNLOAD_DIR"},
		{"share mixed with /", cfgWith("/storage", "/downloads", "/data/pub,/"), "PEERDRIVE_SHARE_DIRS[1]"},
		{"multiple all wrong", cfgWith("/", "/", "/"), "PEERDRIVE_STORAGE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkUnsafeRoots(tc.cfg)
			if err == nil {
				t.Fatalf("config %s should be rejected, but was allowed", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error message should name %s, actual: %v", tc.want, err)
			}
		})
	}
}

func TestCheckUnsafeRoots_EscapeHatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX root '/'")
	}
	t.Setenv("PEERDRIVE_ALLOW_UNSAFE_ROOT", "1")
	if err := checkUnsafeRoots(cfgWith("/", "/", "")); err != nil {
		t.Fatalf("should not reject after setting the escape hatch: %v", err)
	}
}

// On Windows the volume root is a drive letter: `C:\`, `d:`, `\\?\C:\`. On
// Linux, filepath.Abs doesn't recognize drive letters (`C:\` gets treated as an
// ordinary subdirectory named `C:\` under the current directory), so this branch
// can only really run on Windows -- the windows matrix in CI handles it (see
// .github/workflows).
func TestCheckUnsafeRoots_WindowsDriveRoot(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("drive letter semantics only apply on Windows")
	}
	p := filepath.Join("C:", string(filepath.Separator))
	if err := checkUnsafeRoots(cfgWith(p, filepath.Join("C:", string(filepath.Separator), "storage"), "")); err == nil {
		t.Fatal("storage configured as C:\\ should be rejected")
	}
	// drive letter + subdirectory is normal
	if err := checkUnsafeRoots(cfgWith(filepath.Join("C:", string(filepath.Separator), "data", "storage"), "", "")); err != nil {
		t.Fatalf("C:\\data\\storage should not be rejected: %v", err)
	}
}

// The escape hatch reads from an environment variable, so tests must not pollute each other.
func TestMain(m *testing.M) {
	_ = os.Unsetenv("PEERDRIVE_ALLOW_UNSAFE_ROOT")
	os.Exit(m.Run())
}
