// Package main — запуск HTTP-сервера GophProfile.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tradekmv/gophprofile/internal/api"
	"github.com/tradekmv/gophprofile/internal/broker"
	"github.com/tradekmv/gophprofile/internal/config"
	"github.com/tradekmv/gophprofile/internal/repository"
	"github.com/tradekmv/gophprofile/internal/services"
	"github.com/tradekmv/gophprofile/pkg/logger"
	"github.com/tradekmv/gophprofile/pkg/observability"
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
		Str("otel_endpoint", cfg.OTELExporterEndpoint).
		Msg("gophprofile server starting")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// OpenTelemetry: tracer + meter + logger + slog.
	obsCfg := observability.Config{
		ServiceName:         cfg.OTELServiceName,
		ServiceVersion:      "0.2.0",
		DeploymentEnv:       "development",
		OTLPEndpoint:        cfg.OTELExporterEndpoint,
		OTLPTracesEndpoint:  os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"),
		OTLPMetricsEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"),
		OTLPLogsEndpoint:    os.Getenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"),
		Disabled:            cfg.OTELSDKDisabled,
		MetricsHTTPAddr:     cfg.MetricsHTTPAddr,
	}
	sdkShutdown, err := observability.Init(ctx, obsCfg)
	if err != nil {
		logger.L().Error().Err(err).Msg("OTel SDK init failed; continuing without telemetry")
	}
	if sdkShutdown != nil {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = sdkShutdown(ctx)
		}()
	}

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

	metrics := api.NewBusinessMetrics()

	hs := &api.Handlers{
		Service: svc,
		BaseURL: cfg.BaseURL,
		Metrics: metrics,
		Healthers: []api.HealthChecker{
			dbHealth{ping: pool.Ping},
			storageHealth{ping: func(ctx context.Context) error {
				_, _, err := storage.Download(ctx, "health/probe")
				if err != nil && !errors.Is(err, repository.ErrNotFoundInBucket) {
					return err
				}
				return nil
			}},
			kafkaHealth{ping: func(ctx context.Context) error {
				return pingKafka(ctx, cfg.KafkaBrokers)
			}},
		},
	}
	router := api.Router(hs, api.RouterOptions{
		WebDir:          cfg.WebDir,
		MaxBody:         cfg.MaxUploadSize,
		ServiceName:     cfg.OTELServiceName,
		BusinessMetrics: metrics,
	})

	srv := &http.Server{
		Addr:              cfg.ServerAddress,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Прямой /metrics endpoint (Prometheus pull, fallback).
	// Прямой /metrics endpoint (Prometheus pull, fallback).
	if metricsShutdown := observability.StartPrometheusHTTP(cfg.MetricsHTTPAddr, metrics.Handler()); metricsShutdown != nil {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = metricsShutdown(ctx)
		}()
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
