package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/IBM/sarama"
)

// kafkaHealth проверяет доступность Kafka через AdminClient.
type kafkaHealth struct {
	ping func(ctx context.Context) error
}

func (h kafkaHealth) Name() string                    { return "kafka" }
func (h kafkaHealth) Check(ctx context.Context) error { return h.ping(ctx) }

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

// pingKafka создаёт короткоживущий AdminClient и запрашивает у брокера
// версию API. Это дешёвая операция (~миллисекунды) и не требует запущенного
// publisher'а.
func pingKafka(ctx context.Context, brokers []string) error {
	if len(brokers) == 0 {
		return errors.New("no kafka brokers configured")
	}
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V3_6_0_0
	cfg.ClientID = "gophprofile-healthcheck"
	cfg.Net.DialTimeout = 3 * time.Second
	cfg.Net.ReadTimeout = 3 * time.Second
	cfg.Net.WriteTimeout = 3 * time.Second
	cfg.Admin.Timeout = 5 * time.Second

	client, err := sarama.NewClient(brokers, cfg)
	if err != nil {
		return fmt.Errorf("kafka dial: %w", err)
	}
	defer func() { _ = client.Close() }()

	// Проверяем хотя бы один брокер.
	if len(client.Brokers()) == 0 {
		return errors.New("no brokers available")
	}
	broker := client.Brokers()[0]
	ok, err := broker.Connected()
	if err != nil {
		return fmt.Errorf("broker connected check: %w", err)
	}
	if !ok {
		return errors.New("broker not connected")
	}
	return nil
}
