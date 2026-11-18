package logger

import (
	"log/slog"
	"os"
	"sync"
)

var (
	mu            sync.RWMutex
	defaultLogger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
)

// SetLevel sets the global log level. It is safe for concurrent use.
func SetLevel(level slog.Level) {
	mu.Lock()
	defaultLogger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	mu.Unlock()
}

func current() *slog.Logger {
	mu.RLock()
	l := defaultLogger
	mu.RUnlock()
	return l
}

func Debug(msg string, args ...interface{}) { current().Debug(msg, args...) }
func Info(msg string, args ...interface{})  { current().Info(msg, args...) }
func Warn(msg string, args ...interface{})  { current().Warn(msg, args...) }
func Error(msg string, args ...interface{}) { current().Error(msg, args...) }
