// Package broker — Kafka publisher и consumer.
package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/IBM/sarama"

	"github.com/tradekmv/gophprofile/internal/domain"
	"github.com/tradekmv/gophprofile/pkg/logger"
)

// Publisher — интерфейс публикации событий (для тестов).
type Publisher interface {
	PublishUpload(ctx context.Context, event domain.AvatarUploadEvent) error
	PublishDelete(ctx context.Context, event domain.AvatarDeleteEvent) error
	Close() error
}

// Config — конфигурация publisher'а на Sarama.
type Config struct {
	Brokers    []string
	Topic      string
	ClientID   string
	Idempotent bool // false по умолчанию (для совместимости с single-broker KRaft)
}

// SaramaPublisher — sync producer.
type SaramaPublisher struct {
	cfg    Config
	client sarama.SyncProducer
}

// NewSaramaPublisher создаёт SyncProducer с retry/backoff.
//
// Idempotent=true требует доп. настройки Kafka (transaction.state.log
// replication, init_producer_id), которая не всегда работает с single-broker
// KRaft в тестах. Дедупликация в воркере делается через message-id в headers.
func NewSaramaPublisher(cfg Config) (*SaramaPublisher, error) {
	if len(cfg.Brokers) == 0 {
		return nil, fmt.Errorf("no kafka brokers configured")
	}
	if cfg.Topic == "" {
		cfg.Topic = "avatar-events"
	}
	if cfg.ClientID == "" {
		cfg.ClientID = "gophprofile-server"
	}
	_ = cfg.Idempotent // обрабатывается ниже через sc.Producer.Idempotent

	sc := sarama.NewConfig()
	sc.Version = sarama.V3_6_0_0
	sc.ClientID = cfg.ClientID
	sc.Producer.RequiredAcks = sarama.WaitForAll
	sc.Producer.Idempotent = cfg.Idempotent
	sc.Producer.Retry.Max = 5
	sc.Producer.Retry.Backoff = 200 * time.Millisecond
	sc.Producer.Return.Successes = true
	sc.Producer.Return.Errors = true
	sc.Producer.Compression = sarama.CompressionLZ4
	sc.Producer.Partitioner = sarama.NewHashPartitioner
	if cfg.Idempotent {
		sc.Net.MaxOpenRequests = 1 // обязательно для idempotent producer
	}
	sc.Producer.Timeout = 10 * time.Second

	prod, err := sarama.NewSyncProducer(cfg.Brokers, sc)
	if err != nil {
		return nil, fmt.Errorf("create sarama producer: %w", err)
	}
	return &SaramaPublisher{cfg: cfg, client: prod}, nil
}

// publish сериализует payload в JSON и отправляет с нужными headers.
func (p *SaramaPublisher) publish(ctx context.Context, eventType string, messageID string, key string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	msg := &sarama.ProducerMessage{
		Topic: p.cfg.Topic,
		Key:   sarama.StringEncoder(key),
		Value: sarama.ByteEncoder(body),
		Headers: []sarama.RecordHeader{
			{Key: []byte(domain.HeaderEventType), Value: []byte(eventType)},
			{Key: []byte(domain.HeaderMessageID), Value: []byte(messageID)},
		},
		Timestamp: time.Now(),
	}

	done := make(chan error, 1)
	go func() {
		_, _, sendErr := p.client.SendMessage(msg)
		done <- sendErr
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("send message: %w", err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// PublishUpload отправляет AvatarUploaded с ключом = avatar_id.
func (p *SaramaPublisher) PublishUpload(ctx context.Context, event domain.AvatarUploadEvent) error {
	if err := p.publish(ctx, domain.EventTypeUpload, event.AvatarID, event.AvatarID, event); err != nil {
		logger.L().Error().Err(err).Str("avatar_id", event.AvatarID).Msg("publish upload event failed")
		return err
	}
	logger.L().Debug().Str("avatar_id", event.AvatarID).Msg("upload event published")
	return nil
}

// PublishDelete отправляет AvatarDeleted.
func (p *SaramaPublisher) PublishDelete(ctx context.Context, event domain.AvatarDeleteEvent) error {
	if err := p.publish(ctx, domain.EventTypeDelete, event.AvatarID, event.AvatarID, event); err != nil {
		logger.L().Error().Err(err).Str("avatar_id", event.AvatarID).Msg("publish delete event failed")
		return err
	}
	logger.L().Debug().Str("avatar_id", event.AvatarID).Msg("delete event published")
	return nil
}

// Close флашит сообщения и закрывает producer.
func (p *SaramaPublisher) Close() error { return p.client.Close() }

// nopPublisher — пустая реализация для тестов и случаев без Kafka.
type nopPublisher struct{}

// NopPublisher возвращает Publisher, который ничего не делает.
func NopPublisher() Publisher { return nopPublisher{} }

func (nopPublisher) PublishUpload(context.Context, domain.AvatarUploadEvent) error { return nil }
func (nopPublisher) PublishDelete(context.Context, domain.AvatarDeleteEvent) error { return nil }
func (nopPublisher) Close() error                                                  { return nil }
