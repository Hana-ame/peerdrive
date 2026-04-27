// Package p2p_bt 的日志函数直接委托给 peerdrive/internal/log 包。
package p2p_bt

import "peerdrive/internal/log"

// LogDebug delegates to peerdrive/internal/log.
func LogDebug(format string, args ...interface{}) { log.LogDebug(format, args...) }

// LogInfo delegates to peerdrive/internal/log.
func LogInfo(format string, args ...interface{}) { log.LogInfo(format, args...) }

// LogWarn delegates to peerdrive/internal/log.
func LogWarn(format string, args ...interface{}) { log.LogWarn(format, args...) }

// LogError delegates to peerdrive/internal/log.
func LogError(format string, args ...interface{}) { log.LogError(format, args...) }

// LogDuration delegates to peerdrive/internal/log.
func LogDuration(name string) func() { return log.LogDuration(name) }
