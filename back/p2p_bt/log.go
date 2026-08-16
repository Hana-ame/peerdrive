// 本文件为 go-peerdrive-bt 独立库自带日志实现(package p2p_bt 内联函数)。
// 背景: 拆独立库后 p2p_bt 不能再 import 主模块 internal/log(Go internal 规则);
// 原来是委托 p2p_bt.LogDebug -> log.LogDebug, 现改为自实现, 签名/行为对齐
package p2p_bt

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"time"
)

// btLogLevel represents log severity.
type btLogLevel int

const (
	btDebug btLogLevel = iota
	btInfo
	btWarn
	btError
)

var btActiveLevel = btInfo

func init() {
	if v := os.Getenv("PEERDRIVE_LOG_LEVEL"); v != "" {
		switch v {
		case "DEBUG":
			btActiveLevel = btDebug
		case "INFO":
			btActiveLevel = btInfo
		case "WARN":
			btActiveLevel = btWarn
		case "ERROR":
			btActiveLevel = btError
		}
	}
}

func btLogf(level btLogLevel, format string, args ...interface{}) {
	if level < btActiveLevel {
		return
	}
	var prefix string
	switch level {
	case btDebug:
		prefix = "DEBUG"
	case btInfo:
		prefix = "INFO "
	case btWarn:
		prefix = "WARN "
	case btError:
		prefix = "ERROR"
	}
	_, file, line, _ := runtime.Caller(2)
	short := file[strings.LastIndex(file, "/")+1:]
	msg := fmt.Sprintf(format, args...)
	log.Printf("[%s] %s:%d %s", prefix, short, line, msg)
}

// LogDebug logs a debug-level message.
func LogDebug(format string, args ...interface{}) { btLogf(btDebug, format, args...) }

// LogInfo logs an info-level message.
func LogInfo(format string, args ...interface{}) { btLogf(btInfo, format, args...) }

// LogWarn logs a warning-level message.
func LogWarn(format string, args ...interface{}) { btLogf(btWarn, format, args...) }

// LogError logs an error-level message.
func LogError(format string, args ...interface{}) { btLogf(btError, format, args...) }

// LogDuration logs function entry and exit with elapsed time.
// Usage: defer LogDuration("FuncName")()
func LogDuration(name string) func() {
	start := time.Now()
	LogDebug("%s -> start", name)
	return func() {
		LogDebug("%s <- done (%v)", name, time.Since(start))
	}
}