// Package main — запуск HTTP-сервера GophProfile.
package main

import (
	"context"
	"errors"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/tradekmv/gophprofile/internal/api"
	"github.com/tradekmv/gophprofile/internal/broker"
	"github.com/tradekmv/gophprofile/internal/config"
	"github.com/tradekmv/gophprofile/internal/repository"
	"github.com/tradekmv/gophprofile/internal/services"
	"github.com/tradekmv/gophprofile/pkg/logger"
)

func main() {
	if err := run(); err != nil {
		logger.L().Fatal().Err(err).Msg("server exited with error")
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger.Setup(cfg.LogLevel)
	logger.L().Info().
		Str("address", cfg.ServerAddress).
		Str("base_url", cfg.BaseURL).
		Strs("kafka_brokers", cfg.KafkaBrokers).
		Strs("thumbnail_sizes", cfg.ThumbnailSizes).
		Msg("gophprofile server starting")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Подключения к зависимостям.
	pool, err := repository.NewPool(ctx, cfg.PgDSN())
	if err != nil {
		return err
	}
	defer pool.Close()
	avatarRepo := repository.NewPostgresAvatarRepo(pool)

	storage, err := repository.NewS3Storage(ctx, repository.S3Config{
		Endpoint:  cfg.S3Endpoint,
		Region:    cfg.S3Region,
		Bucket:    cfg.S3Bucket,
		AccessKey: cfg.S3AccessKey,
		SecretKey: cfg.S3SecretKey,
		UseSSL:    cfg.S3UseSSL,
	})
	if err != nil {
		return err
	}

	// Publisher для Kafka.
	pub, err := broker.NewSaramaPublisher(broker.Config{
		Brokers:  cfg.KafkaBrokers,
		Topic:    cfg.KafkaTopic,
		ClientID: "gophprofile-server",
	})
	if err != nil {
		return err
	}
	defer func() { _ = pub.Close() }()

	sizes, err := services.ParseThumbnailSizes(cfg.ThumbnailSizes)
	if err != nil {
		return err
	}
	svc := services.NewAvatarService(avatarRepo, storage, pub, cfg.BaseURL, sizes, cfg.MaxUploadSize)

	hs := &api.Handlers{
		Service: svc,
		BaseURL: cfg.BaseURL,
		Healthers: []api.HealthChecker{
			dbHealth{ping: pool.Ping},
			storageHealth{ping: func(ctx context.Context) error {
				_, _, err := storage.Download(ctx, "health/probe")
				if err != nil && !errors.Is(err, repository.ErrNotFoundInBucket) {
					return err
				}
				return nil
			}},
		},
	}
	router := api.Router(hs, cfg.WebDir, cfg.MaxUploadSize)

	srv := &http.Server{
		Addr:              cfg.ServerAddress,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.L().Info().Str("address", cfg.ServerAddress).Msg("http server listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.L().Error().Err(err).Msg("http server error")
			stop()
		}
	}()

	<-ctx.Done()
	logger.L().Info().Msg("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.L().Error().Err(err).Msg("http server shutdown error")
	}
	logger.L().Info().Msg("server stopped")
	return nil
}

// dbHealth пингует Postgres pool.
type dbHealth struct {
	ping func(ctx context.Context) error
}

func (h dbHealth) Name() string                    { return "db" }
func (h dbHealth) Check(ctx context.Context) error { return h.ping(ctx) }

// storageHealth пробует S3 запросом несуществующего ключа.
type storageHealth struct {
	ping func(ctx context.Context) error
}

func (h storageHealth) Name() string                    { return "s3" }
func (h storageHealth) Check(ctx context.Context) error { return h.ping(ctx) }
