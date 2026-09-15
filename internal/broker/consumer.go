package broker

import (
	"context"
	"fmt"
	"time"

	"github.com/IBM/sarama"

	"github.com/tradekmv/gophprofile/internal/domain"
	"github.com/tradekmv/gophprofile/pkg/logger"
)

// Handler реализуется воркером (worker.Processor).
type Handler interface {
	Handle(ctx context.Context, msgType, messageID, payload []byte) error
}

// ConsumerConfig — конфигурация consumer group на Sarama.
type ConsumerConfig struct {
	Brokers  []string
	Topic    string
	GroupID  string
	ClientID string
}

// Consumer — обёртка над Sarama consumer group.
type Consumer struct {
	cfg     ConsumerConfig
	handler Handler
	group   sarama.ConsumerGroup
}

// NewConsumer создаёт consumer group с ручным коммитом оффсетов.
func NewConsumer(cfg ConsumerConfig, h Handler) (*Consumer, error) {
	if len(cfg.Brokers) == 0 {
		return nil, fmt.Errorf("no kafka brokers configured")
	}
	if cfg.Topic == "" {
		return nil, fmt.Errorf("topic is required")
	}
	if cfg.GroupID == "" {
		return nil, fmt.Errorf("group id is required")
	}
	if cfg.ClientID == "" {
		cfg.ClientID = "gophprofile-worker"
	}

	sc := sarama.NewConfig()
	sc.Version = sarama.V3_6_0_0
	sc.ClientID = cfg.ClientID
	sc.Consumer.Offsets.Initial = sarama.OffsetOldest
	sc.Consumer.Offsets.AutoCommit.Enable = false // ручной коммит
	sc.Consumer.Group.Rebalance.Strategy = sarama.NewBalanceStrategyRoundRobin()
	sc.Consumer.Group.Session.Timeout = 30 * time.Second
	sc.Consumer.Group.Heartbeat.Interval = 3 * time.Second
	sc.Consumer.Return.Errors = true

	group, err := sarama.NewConsumerGroup(cfg.Brokers, cfg.GroupID, sc)
	if err != nil {
		return nil, fmt.Errorf("create consumer group: %w", err)
	}
	return &Consumer{cfg: cfg, handler: h, group: group}, nil
}

// Run запускает потребление до отмены ctx.
func (c *Consumer) Run(ctx context.Context) error {
	handler := &saramaHandler{ctx: ctx, handler: c.handler, ready: make(chan struct{})}

	// Логируем ошибки consumer group.
	go func() {
		for err := range c.group.Errors() {
			logger.L().Error().Err(err).Msg("kafka consumer error")
		}
	}()

	for {
		if err := c.group.Consume(ctx, []string{c.cfg.Topic}, handler); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			logger.L().Error().Err(err).Msg("consume failed; will retry")
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

// Close останавливает consumer group.
func (c *Consumer) Close() error { return c.group.Close() }

// saramaHandler реализует sarama.ConsumerGroupHandler.
type saramaHandler struct {
	ctx     context.Context
	handler Handler
	ready   chan struct{}
}

func (h *saramaHandler) Setup(_ sarama.ConsumerGroupSession) error {
	close(h.ready)
	return nil
}

func (h *saramaHandler) Cleanup(_ sarama.ConsumerGroupSession) error { return nil }

func (h *saramaHandler) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case msg, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			h.process(sess, msg)
		case <-sess.Context().Done():
			return nil
		case <-h.ctx.Done():
			return nil
		}
	}
}

// process вызывает обработчик и коммитит оффсет при успехе.
// При ошибке НЕ коммитит — сообщение будет доставлено заново после ребаланса/рестарта.
func (h *saramaHandler) process(sess sarama.ConsumerGroupSession, msg *sarama.ConsumerMessage) {
	msgType := headerValue(msg.Headers, domain.HeaderEventType)
	messageID := headerValue(msg.Headers, domain.HeaderMessageID)
	if messageID == "" {
		messageID = string(msg.Key)
	}

	if err := h.handler.Handle(sess.Context(), []byte(msgType), []byte(messageID), msg.Value); err != nil {
		logger.L().Error().Err(err).
			Str("topic", msg.Topic).
			Int32("partition", msg.Partition).
			Int64("offset", msg.Offset).
			Str("message_id", messageID).
			Msg("handler failed; message will be redelivered")
		return
	}
	sess.MarkMessage(msg, "")
}

// headerValue возвращает значение заголовка по ключу или пустую строку.
func headerValue(hs []*sarama.RecordHeader, key string) string {
	for _, h := range hs {
		if string(h.Key) == key {
			return string(h.Value)
		}
	}
	return ""
}
