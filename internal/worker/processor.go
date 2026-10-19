package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"time"

	"github.com/disintegration/imaging"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/tradekmv/gophprofile/internal/domain"
	"github.com/tradekmv/gophprofile/internal/repository"
	"github.com/tradekmv/gophprofile/internal/services"
	"github.com/tradekmv/gophprofile/pkg/logger"
)

// tracer — глобальный трейсер для операций воркера. Используется через otel.Tracer.
var tracer = otel.Tracer("gophprofile-worker")

// Processor обрабатывает события аватарок (загрузка/удаление) end-to-end.
type Processor struct {
	Repo             repository.AvatarRepository
	Storage          repository.ObjectStorage
	Processed        ProcessedStore
	ImageJPEGQuality int // 1..100, по умолчанию 85
	// Metrics опциональны — если nil, метрики не записываются.
	Metrics *WorkerMetrics
	// TracerName — имя трейсера для спанов обработки. По умолчанию "gophprofile-worker".
	TracerName string
}

// NewProcessor создаёт Processor.
func NewProcessor(repo repository.AvatarRepository, storage repository.ObjectStorage, seen ProcessedStore, metrics *WorkerMetrics) *Processor {
	return &Processor{
		Repo:             repo,
		Storage:          storage,
		Processed:        seen,
		ImageJPEGQuality: 85,
		Metrics:          metrics,
		TracerName:       "gophprofile-worker",
	}
}

// Handle маршрутизирует сообщение Kafka по event-type заголовку.
// Возвращает ошибку, если сообщение нужно обработать повторно (не коммитим).
// nil — сообщение обработано или пропущено (можно коммитить).
func (p *Processor) Handle(ctx context.Context, msgType, messageID, raw []byte) error {
	if len(messageID) == 0 {
		return errors.New("empty message-id")
	}

	ctx, span := tracer.Start(ctx, p.TracerName+".handle",
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.message_id", string(messageID)),
			attribute.String("messaging.operation", "process"),
		),
	)
	defer span.End()

	seen, err := p.Processed.Seen(string(messageID))
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("idempotency store: %w", err)
	}
	if seen {
		span.SetAttributes(attribute.Bool("duplicate", true))
		logger.L().Debug().Str("message_id", string(messageID)).Msg("duplicate event skipped")
		return nil
	}

	switch string(msgType) {
	case domain.EventTypeUpload:
		var e domain.AvatarUploadEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			span.RecordError(err)
			return fmt.Errorf("unmarshal upload: %w", err)
		}
		span.SetAttributes(
			attribute.String("avatar.id", e.AvatarID),
			attribute.String("avatar.user_id", e.UserID),
		)
		return p.processUpload(ctx, &e, string(messageID))
	case domain.EventTypeDelete:
		var e domain.AvatarDeleteEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			span.RecordError(err)
			return fmt.Errorf("unmarshal delete: %w", err)
		}
		span.SetAttributes(
			attribute.String("avatar.id", e.AvatarID),
			attribute.String("avatar.user_id", e.UserID),
		)
		return p.processDelete(ctx, &e, string(messageID))
	default:
		logger.L().Warn().Str("event_type", string(msgType)).Msg("unknown event type")
		span.SetAttributes(attribute.Bool("unknown_event_type", true))
		return nil
	}
}

// processUpload обрабатывает AvatarUploaded.
//
// Метрики: один defer в конце функции вызывает observeProcessing(status, start),
// где status выставляется в каждой ветке. Это исключает двойной учёт failed-операций
// как completed (баг, на который указал ревьюер Sprint 12).
func (p *Processor) processUpload(ctx context.Context, e *domain.AvatarUploadEvent, messageID string) (err error) {
	ctx, span := tracer.Start(ctx, p.TracerName+".processUpload",
		trace.WithAttributes(attribute.String("avatar.id", e.AvatarID)),
	)
	defer span.End()

	start := time.Now()
	status := "completed"
	defer func() {
		p.observeProcessing(status, start)
	}()

	id, parseErr := uuid.Parse(e.AvatarID)
	if parseErr != nil {
		span.RecordError(parseErr)
		span.SetStatus(codes.Error, "parse avatar id")
		status = "decode_error"
		return fmt.Errorf("parse avatar id: %w", parseErr)
	}

	avatar, getErr := p.Repo.GetByID(ctx, id)
	if getErr != nil {
		if errors.Is(getErr, repository.ErrNotFound) {
			// Аватарки нет — помечаем событие обработанным и выходим.
			// Бизнес-успех (no-op), но не completed-листка.
			logger.L().Warn().Str("avatar_id", e.AvatarID).Msg("avatar not found, skipping")
			status = "skipped"
			return p.Processed.Mark(messageID)
		}
		span.RecordError(getErr)
		status = "storage_error"
		return getErr
	}

	if avatar.ProcessingStatus == domain.ProcessingStatusCompleted {
		logger.L().Debug().Str("avatar_id", e.AvatarID).Msg("already processed, skipping")
		status = "skipped"
		return p.Processed.Mark(messageID)
	}

	if updateErr := p.Repo.UpdateProcessingStatus(ctx, id, domain.ProcessingStatusProcessing); updateErr != nil {
		span.RecordError(updateErr)
		status = "storage_error"
		return updateErr
	}

	// Скачиваем оригинал.
	body, _, downloadErr := p.Storage.Download(ctx, e.S3Key)
	if downloadErr != nil {
		_ = p.Repo.UpdateProcessingStatus(ctx, id, domain.ProcessingStatusFailed)
		span.RecordError(downloadErr)
		status = "storage_error"
		return fmt.Errorf("download original: %w", downloadErr)
	}
	defer func() { _ = body.Close() }()

	imgBytes, readErr := io.ReadAll(body)
	if readErr != nil {
		_ = p.Repo.UpdateProcessingStatus(ctx, id, domain.ProcessingStatusFailed)
		span.RecordError(readErr)
		status = "decode_error"
		return fmt.Errorf("read body: %w", readErr)
	}

	img, _, decodeErr := image.Decode(bytes.NewReader(imgBytes))
	if decodeErr != nil {
		_ = p.Repo.UpdateProcessingStatus(ctx, id, domain.ProcessingStatusFailed)
		span.RecordError(decodeErr)
		span.SetStatus(codes.Error, "decode image")
		status = "decode_error"
		return fmt.Errorf("decode image: %w", decodeErr)
	}

	thumbs := map[string]string{}
	for _, op := range e.Operations {
		if op.Type != "resize" || op.Width <= 0 || op.Height <= 0 {
			continue
		}
		key := services.ThumbnailKey(e.UserID, id, op.Width)
		resized := imaging.Resize(img, op.Width, op.Height, imaging.Lanczos)
		quality := p.ImageJPEGQuality
		if quality <= 0 {
			quality = 85
		}
		buf := new(bytes.Buffer)
		if encErr := imaging.Encode(buf, resized, imaging.JPEG, imaging.JPEGQuality(quality)); encErr != nil {
			_ = p.Repo.UpdateProcessingStatus(ctx, id, domain.ProcessingStatusFailed)
			span.RecordError(encErr)
			status = "decode_error"
			return fmt.Errorf("encode thumbnail %dx%d: %w", op.Width, op.Height, encErr)
		}
		if upErr := p.Storage.UploadBytes(ctx, key, buf.Bytes(), "image/jpeg"); upErr != nil {
			_ = p.Repo.UpdateProcessingStatus(ctx, id, domain.ProcessingStatusFailed)
			span.RecordError(upErr)
			status = "storage_error"
			return fmt.Errorf("upload thumbnail %s: %w", key, upErr)
		}
		thumbs[fmt.Sprintf("%dx%d", op.Width, op.Height)] = key
	}
	span.SetAttributes(attribute.Int("thumbnails.count", len(thumbs)))

	if upThumbsErr := p.Repo.UpdateThumbnails(ctx, id, thumbs); upThumbsErr != nil {
		_ = p.Repo.UpdateProcessingStatus(ctx, id, domain.ProcessingStatusFailed)
		span.RecordError(upThumbsErr)
		status = "storage_error"
		return fmt.Errorf("update thumbnails: %w", upThumbsErr)
	}
	if finalErr := p.Repo.UpdateProcessingStatus(ctx, id, domain.ProcessingStatusCompleted); finalErr != nil {
		status = "storage_error"
		return finalErr
	}

	if markErr := p.Processed.Mark(messageID); markErr != nil {
		logger.L().Warn().Err(markErr).Msg("idempotency mark failed")
	}
	logger.L().Info().
		Str("avatar_id", e.AvatarID).
		Int("thumbnails", len(thumbs)).
		Msg("avatar processed")
	return nil
}

// observeProcessing записывает метрики обработки.
func (p *Processor) observeProcessing(status string, start time.Time) {
	if p.Metrics == nil {
		return
	}
	p.Metrics.ProcessedTotal.WithLabelValues(status).Inc()
	p.Metrics.Duration.WithLabelValues(status).Observe(time.Since(start).Seconds())
}

// processDelete обрабатывает AvatarDeleted (best-effort удаление из S3).
//
// Как и processUpload: один defer смотрит на named return err для определения
// статуса — "completed" при Mark, "storage_error" при S3-fail.
func (p *Processor) processDelete(ctx context.Context, e *domain.AvatarDeleteEvent, messageID string) (err error) {
	ctx, span := tracer.Start(ctx, p.TracerName+".processDelete",
		trace.WithAttributes(
			attribute.String("avatar.id", e.AvatarID),
			attribute.Int("keys.count", len(e.S3Keys)),
		),
	)
	defer span.End()

	start := time.Now()
	status := "completed"
	defer func() {
		p.observeProcessing(status, start)
	}()

	if len(e.S3Keys) == 0 {
		return p.Processed.Mark(messageID)
	}
	if delErr := p.Storage.Delete(ctx, e.S3Keys...); delErr != nil {
		span.RecordError(delErr)
		status = "storage_error"
		// Логируем и всё равно помечаем — иначе зависнем на постоянной ошибке.
		logger.L().Warn().Err(delErr).Strs("keys", e.S3Keys).Msg("delete failed")
	}
	return p.Processed.Mark(messageID)
}
