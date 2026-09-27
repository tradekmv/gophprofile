// Package logger — настройка zerolog для всего проекта.
package logger

import (
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Setup настраивает глобальный логгер zerolog.
// level: trace, debug, info, warn, error, fatal, panic.
// Вывод — JSON в stdout с таймстампом.
func Setup(level string) {
	zerolog.TimeFieldFormat = time.RFC3339Nano

	lvl, err := zerolog.ParseLevel(strings.ToLower(level))
	if err != nil || lvl == zerolog.NoLevel {
		lvl = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(lvl)

	log.Logger = zerolog.New(os.Stdout).
		With().
		Timestamp().
		Logger()
}

// L возвращает указатель на глобальный логгер.
func L() *zerolog.Logger { return &log.Logger }
