package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"DEBUG":   slog.LevelDebug,
		"info":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"":        slog.LevelWarn, // fallback
		"bogus":   slog.LevelWarn, // fallback
		"  info ": slog.LevelInfo, // trimmed
	}
	for in, want := range cases {
		if got := ParseLevel(in, slog.LevelWarn); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNew_WritesToWriterAtLevel(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelInfo)

	log.Debug("debug msg") // below level → suppressed
	log.Info("info msg", "k", "v")

	out := buf.String()
	if strings.Contains(out, "debug msg") {
		t.Errorf("debug line emitted at info level:\n%s", out)
	}
	if !strings.Contains(out, "info msg") || !strings.Contains(out, "k=v") {
		t.Errorf("info line missing or malformed:\n%s", out)
	}
}

func TestNew_NilWriterDefaultsStderr(t *testing.T) {
	// Just confirm it doesn't panic and returns a usable logger.
	log := New(nil, slog.LevelError)
	if log == nil {
		t.Fatal("New(nil, ...) returned nil")
	}
	log.Error("to stderr") // no assertion on stderr; smoke only
}

func TestNop_Discards(t *testing.T) {
	log := Nop()
	// Should not panic and should produce no observable output.
	log.Error("this goes nowhere", "secret", "should-not-appear")
}

func TestFromEnv_DefaultWarn(t *testing.T) {
	t.Setenv(EnvLevel, "")
	var buf bytes.Buffer
	log := New(&buf, LevelFromEnv(slog.LevelWarn))
	log.Info("info suppressed at warn default")
	log.Warn("warn shown")
	out := buf.String()
	if strings.Contains(out, "info suppressed") {
		t.Errorf("info leaked at warn default:\n%s", out)
	}
	if !strings.Contains(out, "warn shown") {
		t.Errorf("warn missing:\n%s", out)
	}
}

func TestLevelFromEnv_Override(t *testing.T) {
	t.Setenv(EnvLevel, "debug")
	if got := LevelFromEnv(slog.LevelWarn); got != slog.LevelDebug {
		t.Errorf("LevelFromEnv = %v, want debug from env", got)
	}
}
