package serverapp

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"peerdrive/internal/config"
	"peerdrive/internal/echproxy/twimg"
	"peerdrive/internal/log"
	"peerdrive/internal/pathutil"
)

// roots.go — Startup root checking and ECH proxy configuration builders.

// configuredDirs returns all config-specified directories and their source env var names.
func configuredDirs(cfg *config.Config) []struct {
	name string
	val  string
} {
	candidates := []struct {
		name string
		val  string
	}{
		{"PEERDRIVE_STORAGE", cfg.StorageDir},
		{"PEERDRIVE_DOWNLOAD_DIR", cfg.DownloadDir},
	}
	for i, d := range pathutil.SplitList(cfg.ShareDirs) {
		candidates = append(candidates, struct {
			name string
			val  string
		}{fmt.Sprintf("PEERDRIVE_SHARE_DIRS[%d]", i), d})
	}
	return candidates
}

func checkUnsafeRoots(cfg *config.Config) error {
	if os.Getenv("PEERDRIVE_ALLOW_UNSAFE_ROOT") == "1" {
		log.LogWarn("main: PEERDRIVE_ALLOW_UNSAFE_ROOT=1, skipping volume-root configuration check")
		return nil
	}
	var bad []string
	for _, c := range configuredDirs(cfg) {
		if pathutil.IsUnsafeRoot(c.val) {
			bad = append(bad, fmt.Sprintf("%s=%q", c.name, c.val))
		}
	}
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("refusing to start: a directory was configured as a filesystem volume root (%s). This would make all files under it readable/writable externally. "+
		"Please change it to a specific subdirectory. To confirm and run anyway, set PEERDRIVE_ALLOW_UNSAFE_ROOT=1",
		strings.Join(bad, ", "))
}

// warnUnsupportedRoots is a startup self-check: whether these directories can establish a
// secure boundary via os.Root.
//
// Why say it early: when os.Root fails, the symptom is subtle — **files in that directory
// simply can't be shared** — and the log only shows an errno, so operators will chase
// unrelated issues (chmod/chown/path reconfiguration, all the wrong direction). Here we
// clearly report the conclusion and next steps for each directory at startup.
func warnUnsupportedRoots(cfg *config.Config) {
	for _, c := range configuredDirs(cfg) {
		if strings.TrimSpace(c.val) == "" {
			continue
		}
		if err := pathutil.ProbeRootSupport(c.val); err != nil {
			// The directory not existing on first startup is normal — don't report it as "misconfigured"
			if errors.Is(err, os.ErrNotExist) {
				log.LogInfo("main: %s=%s does not exist yet, will be created automatically on first write", c.name, c.val)
				continue
			}
			log.LogWarn("main: %s=%s cannot be used as a secure root directory: %s",
				c.name, c.val, pathutil.ExplainRootFailure(c.val, err))
		}
	}
}

// buildECHProxyModule translates the PEERDRIVE_ECH_PROXY_* env settings into a
// twimg.Module. Kept separate from buildRouter so the flag/env mapping is
// testable in isolation.
//
// Only the IP family setting is carried over. The in-process ECH client needs
// no entry host, listen address, TLS-skip flag, release channel or install
// directory — it spawns no subprocess and binds no address.
func buildECHProxyModule(cfg *config.Config) (*twimg.Module, error) {
	return twimg.New(twimg.Config{
		SrcHost: twimg.DefaultSrcHost,
		IPMode:  cfg.ECHProxyIPMode,
	})
}

// exhentaiAuthHeaders turns the single config-auth env value into the header map
// the exhentai module attaches to its config fetches. An empty value yields nil
// so the module does not send an empty Authorization header.
func exhentaiAuthHeaders(value string) map[string]string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return map[string]string{"Authorization": value}
}
