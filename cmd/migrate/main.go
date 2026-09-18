// Package main — запуск миграций БД (up/down).
package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/tradekmv/gophprofile/internal/config"
	"github.com/tradekmv/gophprofile/pkg/logger"
)

// migrationsURL — путь к файлам миграций относительно рабочей директории.
const migrationsURL = "file://migrations"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: migrate <up|down|version|force VERSION> [steps]")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "migrate error:", err)
		os.Exit(1)
	}
}

func run(cmd string, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger.Setup(cfg.LogLevel)

	m, err := migrate.New(migrationsURL, cfg.PgDSN())
	if err != nil {
		return fmt.Errorf("init migrator: %w", err)
	}
	defer func() { _, _ = m.Close() }()

	switch cmd {
	case "up":
		logger.L().Info().Msg("applying up migrations")
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return fmt.Errorf("up: %w", err)
		}
	case "down":
		steps := 1
		if len(args) > 0 {
			n, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid steps: %w", err)
			}
			steps = n
		}
		logger.L().Info().Int("steps", steps).Msg("rolling back migrations")
		if err := m.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return fmt.Errorf("down: %w", err)
		}
	case "version":
		v, dirty, err := m.Version()
		if err != nil {
			return fmt.Errorf("version: %w", err)
		}
		fmt.Printf("version=%d dirty=%v\n", v, dirty)
		return nil
	case "force":
		if len(args) == 0 {
			return errors.New("force requires VERSION arg")
		}
		v, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid version: %w", err)
		}
		if err := m.Force(v); err != nil {
			return fmt.Errorf("force: %w", err)
		}
		return nil
	default:
		return errors.New("unknown command: " + cmd)
	}

	v, dirty, _ := m.Version()
	logger.L().Info().Uint("version", v).Bool("dirty", dirty).Msg("migrations done")
	return nil
}
