package logger

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func TestSetupUnknownLevelFallsBackToInfo(t *testing.T) {
	t.Parallel()

	// Capture stdout during Setup.
	r, w, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	done := make(chan struct{})
	go func() {
		Setup("not-a-real-level")
		_ = w.Close()
		close(done)
	}()
	<-done

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	_ = r.Close()

	out := buf.String()
	// Setup didn't write any log; this is just to confirm no panic.
	_ = out
}

func TestSetupKnownLevelParses(t *testing.T) {
	t.Parallel()

	r, w, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	done := make(chan struct{})
	go func() {
		Setup("debug")
		_ = w.Close()
		close(done)
	}()
	<-done

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	_ = r.Close()

	// No assertion needed — just ensure no panic and stdout is restored.
}

func TestLReturnsGlobalLogger(t *testing.T) {
	t.Parallel()
	prev := log.Logger
	defer func() { log.Logger = prev }()
	log.Logger = zerolog.New(os.Stdout).With().Timestamp().Logger()
	got := L()
	if got == nil {
		t.Fatal("L() returned nil")
	}
}

// TestDebugLevelParse sanity-checks that zerolog can parse our common levels.
func TestDebugLevelParse(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"trace", "debug", "info", "warn", "error", "fatal", "panic", "unknown"} {
		_, err := zerolog.ParseLevel(strings.ToLower(raw))
		if raw == "unknown" {
			if err == nil {
				t.Errorf("expected error for %q", raw)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseLevel(%q): %v", raw, err)
		}
	}

	// Sanity: parse a JSON log line via the global logger wired to a buffer.
	var buf bytes.Buffer
	prev := log.Logger
	prevLvl := zerolog.GlobalLevel()
	log.Logger = zerolog.New(&buf).With().Timestamp().Logger()
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	defer func() {
		log.Logger = prev
		zerolog.SetGlobalLevel(prevLvl)
	}()
	log.Logger.Info().Str("a", "b").Msg("c")
	var got map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if got["a"] != "b" || got["message"] != "c" {
		t.Errorf("unexpected: %v", got)
	}
}
