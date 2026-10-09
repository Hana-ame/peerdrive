package config

import (
	"os"
	"strconv"
	"strings"

	"peerdrive/internal/log"
)

// resolvePort reads PEERDRIVE_PORT first, falling back to legacy PORT with a warning.
func resolvePort() string {
	if p := os.Getenv("PEERDRIVE_PORT"); p != "" {
		return p
	}
	if p := os.Getenv("PORT"); p != "" {
		log.LogWarn("config: PORT is deprecated, use PEERDRIVE_PORT instead")
		return p
	}
	return "3000"
}

// resolveMainDBPath reads PEERDRIVE_DB_PATH first, falling back to legacy DB_PATH with a warning.
func resolveMainDBPath() string {
	if p := os.Getenv("PEERDRIVE_DB_PATH"); p != "" {
		return p
	}
	if p := os.Getenv("DB_PATH"); p != "" {
		log.LogWarn("config: DB_PATH is deprecated, use PEERDRIVE_DB_PATH instead")
		return p
	}
	return "./peerdrive.db"
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return defaultVal
}

// getAdminToken resolves the admin token from either PEERDRIVE_ADMIN_TOKEN (env var)
// or PEERDRIVE_ADMIN_TOKEN_FILE (path to a file containing the token on line 1).
// Env var takes precedence; file is a fallback for secrets stored on disk (chmod 600).
// The file content is trimmed of leading/trailing whitespace and only the first line is used.
func getAdminToken() string {
	if tok := getEnv("PEERDRIVE_ADMIN_TOKEN", ""); tok != "" {
		return tok
	}
	filePath := getEnv("PEERDRIVE_ADMIN_TOKEN_FILE", "")
	if filePath == "" {
		return ""
	}
	// 审计 R2 MEDIUM（2026-10-08）：文件权限与路径校验。
	// 注释推荐 chmod 600——检查并在过宽时告警，但不阻断启动
	//（权限修复需要 root，强制拒绝会让整个节点无法启动）。
	if fi, err := os.Stat(filePath); err == nil {
		perm := fi.Mode().Perm()
		if perm&0077 != 0 {
			log.LogWarn("config: PEERDRIVE_ADMIN_TOKEN_FILE %s permissions %o — recommended 600 (secret file); group/other-readable mode weakens secret isolation", filePath, perm)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			log.LogWarn("config: PEERDRIVE_ADMIN_TOKEN_FILE %s is a symlink — consider using a regular file to avoid symlink-follow attacks", filePath)
		}
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		// 审计 R2 MEDIUM：文件读取失败必须记日志——之前的注释声称
		// "logged at startup (app.go)" 但实际没有，静默返回空串
		// 会让操作员误以为 admin auth 已启用。
		log.LogError("config: PEERDRIVE_ADMIN_TOKEN_FILE %s read failed: %v — admin auth disabled (token empty)", filePath, err)
		return ""
	}
	// Trim whitespace and take the first line only.
	tok := strings.TrimSpace(string(data))
	if idx := strings.IndexByte(tok, '\n'); idx >= 0 {
		tok = tok[:idx]
	}
	return strings.TrimSpace(tok)
}

func getEnvBool(key string, defaultVal bool) bool {
	if val, ok := os.LookupEnv(key); ok {
		b, err := strconv.ParseBool(val)
		if err == nil {
			return b
		}
	}
	return defaultVal
}

func getEnvInt64(key string, defaultVal int64) int64 {
	if val, ok := os.LookupEnv(key); ok {
		n, err := strconv.ParseInt(val, 10, 64)
		if err == nil && n > 0 {
			return n
		}
	}
	return defaultVal
}

func getEnvFloat(key string, defaultVal float64) float64 {
	if val, ok := os.LookupEnv(key); ok {
		f, err := strconv.ParseFloat(val, 64)
		if err == nil && f >= 0 {
			return f
		}
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val, ok := os.LookupEnv(key); ok {
		n, err := strconv.Atoi(val)
		if err == nil && n > 0 {
			return n
		}
	}
	return defaultVal
}
