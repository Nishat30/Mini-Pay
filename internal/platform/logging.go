// logging.go configures structured JSON logging via log/slog.
// It owns logger creation and log level parsing.
// It must never log secrets (API keys, webhook secrets, passwords).
// It relies on slog from the standard library only.
package platform

import (
	"log/slog"
	"os"
	"strings"
)

// NewLogger creates a structured JSON logger at the given level.
// Valid levels: debug, info, warn, error. Defaults to info.
func NewLogger(level string) *slog.Logger {
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

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: lvl,
	})

	return slog.New(handler)
}
