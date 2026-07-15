// Package xlog is a tiny, dependency-free leveled logger with colored output.
package xlog

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Level controls verbosity.
type Level int32

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
	LevelSilent
)

var (
	mu      sync.Mutex
	level   = LevelInfo
	colored = supportsColor()
)

// SetLevel parses a textual level ("debug","info","warn","error","silent").
func SetLevel(s string) {
	mu.Lock()
	defer mu.Unlock()
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug", "trace":
		level = LevelDebug
	case "warn", "warning":
		level = LevelWarn
	case "error", "err":
		level = LevelError
	case "silent", "off", "none":
		level = LevelSilent
	default:
		level = LevelInfo
	}
}

func supportsColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return os.Getenv("TERM") != "dumb"
}

func emit(l Level, tag, color, format string, args ...any) {
	mu.Lock()
	cur := level
	c := colored
	mu.Unlock()
	if l < cur {
		return
	}
	ts := time.Now().Format("15:04:05.000")
	msg := fmt.Sprintf(format, args...)
	if c {
		fmt.Fprintf(os.Stderr, "\x1b[90m%s\x1b[0m %s%-5s\x1b[0m %s\n", ts, color, tag, msg)
	} else {
		fmt.Fprintf(os.Stderr, "%s %-5s %s\n", ts, tag, msg)
	}
}

// Debugf logs at debug level.
func Debugf(format string, args ...any) { emit(LevelDebug, "DEBUG", "\x1b[36m", format, args...) }

// Infof logs at info level.
func Infof(format string, args ...any) { emit(LevelInfo, "INFO", "\x1b[32m", format, args...) }

// Warnf logs at warn level.
func Warnf(format string, args ...any) { emit(LevelWarn, "WARN", "\x1b[33m", format, args...) }

// Errorf logs at error level.
func Errorf(format string, args ...any) { emit(LevelError, "ERROR", "\x1b[31m", format, args...) }

// Fatalf logs at error level and exits the process.
func Fatalf(format string, args ...any) {
	emit(LevelError, "FATAL", "\x1b[35m", format, args...)
	os.Exit(1)
}
