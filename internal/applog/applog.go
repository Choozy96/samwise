// Package applog configures structured (JSON) logging for the orchestrator.
//
// A single
// process-wide slog.Logger is installed as the default; callers use slog
// directly or take a *slog.Logger from New.
package applog

import (
	"log/slog"
	"os"
	"strings"
)

// New builds a JSON slog.Logger at the given level ("debug"|"info"|"warn"|
// "error") writing to stderr, and installs it as the slog default.
func New(level string) *slog.Logger {
	h := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: parseLevel(level),
	})
	l := slog.New(h)
	slog.SetDefault(l)
	return l
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
