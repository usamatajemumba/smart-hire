// Package logger builds the structured logger used by every service.
//
// We use the standard library's log/slog (Go 1.21+): JSON output is easy to
// ship to Loki/ELK, and in Week 3 we attach OpenTelemetry trace IDs to it.
package logger

import (
	"log/slog"
	"os"
	"strings"
)

// New returns a JSON slog.Logger tagged with the service name.
// level: debug|info|warn|error (default info).
func New(service, level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(h).With("service", service)
}
