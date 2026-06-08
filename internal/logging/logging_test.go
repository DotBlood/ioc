package logging

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"DEBUG":   slog.LevelDebug,
		"info":    slog.LevelInfo,
		"":        slog.LevelInfo, // empty falls back to info
		"bogus":   slog.LevelInfo, // unknown falls back to info
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"err":     slog.LevelError,
	}
	for in, want := range cases {
		require.Equalf(t, want, parseLevel(in), "parseLevel(%q)", in)
	}
}

// withSink redirects the package logger to buf for the duration of the test and
// restores the default (info/text/stderr) afterwards.
func withSink(t *testing.T, buf *bytes.Buffer) {
	t.Helper()
	mu.Lock()
	orig := curSink
	curSink = buf
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		curSink, curLvl, curFmt = orig, "info", "text"
		apply()
		mu.Unlock()
	})
}

func TestInit_FiltersByLevel(t *testing.T) {
	var buf bytes.Buffer
	withSink(t, &buf)

	Init("warn", "text")
	slog.Info("below-threshold")
	require.Empty(t, buf.String(), "info must be filtered when level=warn")

	slog.Warn("at-threshold")
	require.Contains(t, buf.String(), "at-threshold")
}

func TestSetLevel_AdjustsThreshold(t *testing.T) {
	var buf bytes.Buffer
	withSink(t, &buf)

	Init("info", "text")
	SetLevel("debug")
	slog.Debug("now-visible")
	require.Contains(t, buf.String(), "now-visible", "SetLevel(debug) must let debug through")
}

func TestSetFormat_JSON(t *testing.T) {
	var buf bytes.Buffer
	withSink(t, &buf)

	Init("info", "text")
	SetFormat("json")
	slog.Info("hello", "k", "v")
	out := buf.String()
	require.Contains(t, out, `"msg":"hello"`)
	require.Contains(t, out, `"k":"v"`)
}
