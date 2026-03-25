package logger

import (
	"log/slog"
	"os"
	"sync"
)

var (
	Log  *slog.Logger
	once sync.Once
)

func init() {
	// Initialize with a default nop-like logger or minimal stdout logger
	// so that packages using Log don't panic before InitLogger is called.
	Log = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// InitLogger initializes the global structured logger using the standard library's slog.
func InitLogger() {
	once.Do(func() {
		handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
			// You can add more options here if needed, like AddSource
		})
		Log = slog.New(handler)
		slog.SetDefault(Log)
	})
}

// Sync is a no-op for slog, kept for compatibility with existing code.
func Sync() {}
