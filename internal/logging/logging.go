// Package logging owns IOC's process-wide diagnostic logger. It configures the
// standard library's log/slog default logger (Go 1.21+) so every package can
// emit structured diagnostics with plain `slog.Info/Warn/Error` calls and no
// dependency on this package.
//
// HARD INVARIANT: logs go to STDERR only. STDOUT is reserved for machine-
// readable command results (cmd/ioc's printJSON, internal/eval reports); a
// logger must never write there or it corrupts that contract. Callers that
// emit results keep using os.Stdout directly.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

// state remembers the last-applied level/format/sink so the per-command flags
// (-log-level / -log-format) can re-initialise one axis without losing the
// others. Startup is single-threaded but the mutex keeps re-init race-free if a
// later caller logs concurrently.
var (
	mu      sync.Mutex
	curLvl            = "info"
	curFmt            = "text"
	curSink io.Writer = os.Stderr
)

// Init configures the default slog logger from a level (debug|info|warn|error)
// and format (text|json), writing to stderr. Unknown values fall back to
// info/text. Safe to call repeatedly.
func Init(level, format string) {
	mu.Lock()
	defer mu.Unlock()
	curLvl, curFmt = level, format
	apply()
}

// SetLevel re-initialises only the level, preserving the current format/sink.
// Used by the -log-level flag, which applies during flag.Parse.
func SetLevel(level string) {
	mu.Lock()
	defer mu.Unlock()
	curLvl = level
	apply()
}

// SetFormat re-initialises only the format, preserving the current level/sink.
func SetFormat(format string) {
	mu.Lock()
	defer mu.Unlock()
	curFmt = format
	apply()
}

// apply rebuilds the handler from the current state. Caller holds mu.
func apply() {
	opts := &slog.HandlerOptions{Level: parseLevel(curLvl)}
	var h slog.Handler
	if strings.EqualFold(strings.TrimSpace(curFmt), "json") {
		h = slog.NewJSONHandler(curSink, opts)
	} else {
		h = slog.NewTextHandler(curSink, opts)
	}
	slog.SetDefault(slog.New(h))
}

// parseLevel maps a human level string to a slog.Level, defaulting to Info.
func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error", "err":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
