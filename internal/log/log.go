package log

import (
	"fmt"
	"time"
)

func Info(format string, args ...interface{}) {
	fmt.Printf("[%s] INFO  %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

func Warn(format string, args ...interface{}) {
	fmt.Printf("[%s] WARN  %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

func Error(format string, args ...interface{}) {
	fmt.Printf("[%s] ERROR %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}
