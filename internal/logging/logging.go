// Package logging centralises slog logger construction for the
// agent-memory binary. Both the CLI and the MCP server build a logger
// here and thread it through the memory/fetch/staging deps.
//
// Two hard rules this package exists to enforce by construction:
//
//  1. Logs go to STDERR, never stdout. The `agent-memory mcp` server
//     speaks JSON-RPC over stdout; a stray log line there corrupts the
//     protocol. CLI commands likewise reserve stdout for their own
//     output (Markdown packs, --json payloads). New(...) takes an
//     explicit io.Writer so the caller picks stderr deliberately.
//
//  2. The level is quiet by default. Without configuration the logger
//     emits at WARN — so a normal run is silent unless something is
//     worth a human's attention. Opt into detail via AGENT_MEMORY_LOG.
//
// Secret-safety (never logging matched credential bytes) is enforced at
// the call sites, not here: callers log Finding.Type / .Line only, never
// re-slice content by offset. See docs/patterns/security-layer.md.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// EnvLevel is the environment variable that overrides the default log
// level: one of debug | info | warn | error (case-insensitive).
const EnvLevel = "AGENT_MEMORY_LOG"

// New builds a text-format slog.Logger writing to w at the given level.
// w is almost always os.Stderr (see the package comment). A nil w
// defaults to os.Stderr defensively.
func New(w io.Writer, level slog.Level) *slog.Logger {
	if w == nil {
		w = os.Stderr
	}
	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(h)
}

// Nop returns a logger that discards everything. Used as the fallback
// when no logger is wired into a deps struct, so call sites never have
// to nil-check.
func Nop() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// LevelFromEnv reads AGENT_MEMORY_LOG and maps it to an slog.Level.
// Unset or unrecognised → the supplied fallback. Accepts the four
// canonical names case-insensitively.
func LevelFromEnv(fallback slog.Level) slog.Level {
	return ParseLevel(os.Getenv(EnvLevel), fallback)
}

// ParseLevel maps a level name to slog.Level. Empty/unknown → fallback.
func ParseLevel(name string, fallback slog.Level) slog.Level {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return fallback
	}
}

// FromEnv is the common one-liner the CLI and MCP server use: a logger
// to w (stderr) whose level is AGENT_MEMORY_LOG or WARN by default.
func FromEnv(w io.Writer) *slog.Logger {
	return New(w, LevelFromEnv(slog.LevelWarn))
}
