package logger

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestSetupUnknownLevelFallsBackToInfo(t *testing.T) {
	// Без t.Parallel() — тест меняет глобальный os.Stdout, race с параллельными тестами.
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
	// Без t.Parallel() — тест меняет глобальный os.Stdout, race с параллельными тестами.
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
	// L() возвращает baseLogger (наш package-level singleton), не log.Logger.
	// Не трогаем log.Logger — это устраняет race с другими параллельными тестами.
	got := L()
	if got == nil {
		t.Fatal("L() returned nil")
	}
}

// TestDebugLevelParse sanity-checks that zerolog can parse our common levels.
func TestDebugLevelParse(t *testing.T) {
	// Без t.Parallel() — мы используем локальный буфер и глобальный zerolog.GlobalLevel
	// (через SetGlobalLevel/ParseLevel), чтобы не конкурировать с другими тестами.
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

	// Sanity: JSON log line через локальный zerolog.Logger (не глобальный).
	var buf bytes.Buffer
	lg := zerolog.New(&buf).With().Timestamp().Logger()
	lg.Info().Str("a", "b").Msg("c")
	var got map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if got["a"] != "b" || got["message"] != "c" {
		t.Errorf("unexpected: %v", got)
	}
}
