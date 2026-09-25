package app

import (
	"log/slog"
	"os"
	"strings"
)

func initLogger(debug bool, level string) {
	var slogLevel slog.Level
	switch strings.ToLower(level) {
	case "debug":
		slogLevel = slog.LevelDebug
	case "info":
		slogLevel = slog.LevelInfo
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}

	if !debug {
		logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slogLevel,
		}))
		slog.SetDefault(logger)
	} else {
		slog.SetLogLoggerLevel(slogLevel)
	}

	slog.Info("Logger initialized with level '" + strings.ToLower(slogLevel.String()) + "'")
}
