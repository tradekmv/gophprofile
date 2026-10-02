// Package logger — настройка zerolog для всего проекта.
package logger

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/trace"
)

var (
	baseLogger     zerolog.Logger
	baseLoggerOnce sync.RWMutex
)

// Setup настраивает глобальный логгер zerolog.
// level: trace, debug, info, warn, error, fatal, panic.
// Вывод — JSON в stdout с таймстампом.
//
// Сохраняет base logger в package-level переменную. Глобальный log.Logger
// НЕ трогаем — это устраняет race-condition с тестами, которые читают
// log.Logger параллельно с Setup/LContext.
func Setup(level string) {
	zerolog.TimeFieldFormat = time.RFC3339Nano

	lvl, err := zerolog.ParseLevel(strings.ToLower(level))
	if err != nil || lvl == zerolog.NoLevel {
		lvl = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(lvl)

	baseLoggerOnce.Lock()
	baseLogger = zerolog.New(os.Stdout).
		With().
		Timestamp().
		Logger()
	baseLoggerOnce.Unlock()
}

// L возвращает указатель на base-логгер (настроенный через Setup).
// Алиас для обратной совместимости со старым кодом, который использовал L().
func L() *zerolog.Logger {
	baseLoggerOnce.RLock()
	defer baseLoggerOnce.RUnlock()
	l := baseLogger
	return &l
}

// LContext возвращает zerolog.Logger, обогащённый trace_id/span_id из ctx,
// если они там есть. Используется в обработчиках с request-context.
//
// Не трогает глобальный log.Logger — все derived-логгеры создаются через
// baseLogger.With(), что устраняет race-condition с другими частями кода,
// читающими/пишущими в log.Logger (например, в тестах).
func LContext(ctx context.Context) *zerolog.Logger {
	sc := trace.SpanContextFromContext(ctx)
	baseLoggerOnce.RLock()
	base := baseLogger
	baseLoggerOnce.RUnlock()
	if !sc.IsValid() {
		// Возвращаем копию, чтобы вызывающий не мутировал наш singleton.
		l := base
		return &l
	}
	l := base.With().
		Str("trace_id", sc.TraceID().String()).
		Str("span_id", sc.SpanID().String()).
		Logger()
	return &l
}

// SetBaseOutput позволяет тестам перенаправить вывод логгера (например,
// в bytes.Buffer) без обращения к глобальному log.Logger.
func SetBaseOutput(w io.Writer) {
	baseLoggerOnce.Lock()
	baseLogger = zerolog.New(w).
		With().
		Timestamp().
		Logger()
	baseLoggerOnce.Unlock()
}
