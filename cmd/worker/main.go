// Package main — запуск Kafka-воркера GophProfile.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tradekmv/gophprofile/internal/broker"
	"github.com/tradekmv/gophprofile/internal/config"
	"github.com/tradekmv/gophprofile/internal/repository"
	"github.com/tradekmv/gophprofile/internal/worker"
	"github.com/tradekmv/gophprofile/pkg/logger"
	"github.com/tradekmv/gophprofile/pkg/observability"
)

func main() {
	if err := run(); err != nil {
		logger.L().Fatal().Err(err).Msg("worker exited with error")
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger.Setup(cfg.LogLevel)
	logger.L().Info().
		Str("topic", cfg.KafkaTopic).
		Str("group", cfg.KafkaConsumerGroup).
		Strs("brokers", cfg.KafkaBrokers).
		Strs("thumbnail_sizes", cfg.ThumbnailSizes).
		Str("otel_endpoint", cfg.OTELExporterEndpoint).
		Msg("gophprofile worker starting")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// OpenTelemetry SDK.
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

	pool, err := repository.NewPool(ctx, cfg.PgDSN())
	if err != nil {
		return err
	}
	defer pool.Close()
	repo := repository.NewPostgresAvatarRepo(pool)

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

	metrics := worker.NewWorkerMetrics()
	proc := worker.NewProcessor(repo, storage, worker.NewInMemoryProcessedStore(24*time.Hour), metrics)
	cons, err := broker.NewConsumer(broker.ConsumerConfig{
		Brokers:  cfg.KafkaBrokers,
		Topic:    cfg.KafkaTopic,
		GroupID:  cfg.KafkaConsumerGroup,
		ClientID: "gophprofile-worker",
	}, proc)
	if err != nil {
		return err
	}
	defer func() { _ = cons.Close() }()

	// Прямой /metrics endpoint (Prometheus pull, fallback).
	if cfg.MetricsHTTPAddr != "" {
		go func() {
			mux := http.NewServeMux()
			mux.Handle("/metrics", metrics.Handler())
			mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
			srv := &http.Server{Addr: cfg.MetricsHTTPAddr, Handler: mux}
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.L().Error().Err(err).Msg("worker metrics HTTP server error")
			}
		}()
	}

	logger.L().Info().Msg("worker consuming")
	if err := cons.Run(ctx); err != nil {
		return err
	}
	logger.L().Info().Msg("worker stopped")
	return nil
}
