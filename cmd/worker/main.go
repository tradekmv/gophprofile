// Package main — запуск Kafka-воркера GophProfile.
package main

import (
	"context"
	"os/signal"
	"syscall"
	"time"

	"github.com/tradekmv/gophprofile/internal/broker"
	"github.com/tradekmv/gophprofile/internal/config"
	"github.com/tradekmv/gophprofile/internal/repository"
	"github.com/tradekmv/gophprofile/internal/worker"
	"github.com/tradekmv/gophprofile/pkg/logger"
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
		Msg("gophprofile worker starting")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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

	proc := worker.NewProcessor(repo, storage, worker.NewInMemoryProcessedStore(24*time.Hour))
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

	logger.L().Info().Msg("worker consuming")
	if err := cons.Run(ctx); err != nil {
		return err
	}
	logger.L().Info().Msg("worker stopped")
	return nil
}
