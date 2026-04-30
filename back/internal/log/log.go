// Package log 提供分级日志输出（DEBUG/INFO/WARN/ERROR），日志级别由 PEERDRIVE_LOG_LEVEL 环境变量控制。
// LogDuration 自动记录函数调用耗时。
package log

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"time"
)

// Level represents log severity.
type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
)

var activeLevel = INFO

func init() {
	switch strings.ToUpper(os.Getenv("PEERDRIVE_LOG_LEVEL")) {
	case "DEBUG":
		activeLevel = DEBUG
	case "INFO":
		activeLevel = INFO
	case "WARN":
		activeLevel = WARN
	case "ERROR":
		activeLevel = ERROR
	}
}

func logf(level Level, format string, args ...interface{}) {
	if level < activeLevel {
		return
	}
	var prefix string
	switch level {
	case DEBUG:
		prefix = "DEBUG"
	case INFO:
		prefix = "INFO "
	case WARN:
		prefix = "WARN "
	case ERROR:
		prefix = "ERROR"
	}
	_, file, line, _ := runtime.Caller(2)
	short := file[strings.LastIndex(file, "/")+1:]
	msg := fmt.Sprintf(format, args...)
	log.Printf("[%s] %s:%d %s", prefix, short, line, msg)
}

// LogDebug logs a debug-level message.
func LogDebug(format string, args ...interface{}) { logf(DEBUG, format, args...) }

// LogInfo logs an info-level message.
func LogInfo(format string, args ...interface{}) { logf(INFO, format, args...) }

// LogWarn logs a warning-level message.
func LogWarn(format string, args ...interface{}) { logf(WARN, format, args...) }

// LogError logs an error-level message.
func LogError(format string, args ...interface{}) { logf(ERROR, format, args...) }

// LogDuration logs function entry and exit with elapsed time.
// Usage: defer LogDuration("FuncName")()
func LogDuration(name string) func() {
	start := time.Now()
	LogDebug("%s -> start", name)
	return func() {
		LogDebug("%s <- done (%v)", name, time.Since(start))
	}
}
