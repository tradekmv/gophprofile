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

	"github.com/disintegration/imaging"
	"github.com/google/uuid"

	"github.com/tradekmv/gophprofile/internal/domain"
	"github.com/tradekmv/gophprofile/internal/repository"
	"github.com/tradekmv/gophprofile/internal/services"
	"github.com/tradekmv/gophprofile/pkg/logger"
)

// Processor обрабатывает события аватарок (загрузка/удаление) end-to-end.
type Processor struct {
	Repo             repository.AvatarRepository
	Storage          repository.ObjectStorage
	Processed        ProcessedStore
	ImageJPEGQuality int // 1..100, по умолчанию 85
}

// NewProcessor создаёт Processor.
func NewProcessor(repo repository.AvatarRepository, storage repository.ObjectStorage, seen ProcessedStore) *Processor {
	return &Processor{
		Repo:             repo,
		Storage:          storage,
		Processed:        seen,
		ImageJPEGQuality: 85,
	}
}

// Handle маршрутизирует сообщение Kafka по event-type заголовку.
// Возвращает ошибку, если сообщение нужно обработать повторно (не коммитим).
// nil — сообщение обработано или пропущено (можно коммитить).
func (p *Processor) Handle(ctx context.Context, msgType, messageID, raw []byte) error {
	if len(messageID) == 0 {
		return errors.New("empty message-id")
	}

	seen, err := p.Processed.Seen(string(messageID))
	if err != nil {
		return fmt.Errorf("idempotency store: %w", err)
	}
	if seen {
		logger.L().Debug().Str("message_id", string(messageID)).Msg("duplicate event skipped")
		return nil
	}

	switch string(msgType) {
	case domain.EventTypeUpload:
		var e domain.AvatarUploadEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			return fmt.Errorf("unmarshal upload: %w", err)
		}
		return p.processUpload(ctx, &e, string(messageID))
	case domain.EventTypeDelete:
		var e domain.AvatarDeleteEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			return fmt.Errorf("unmarshal delete: %w", err)
		}
		return p.processDelete(ctx, &e, string(messageID))
	default:
		logger.L().Warn().Str("event_type", string(msgType)).Msg("unknown event type")
		return nil
	}
}

// processUpload обрабатывает AvatarUploaded.
func (p *Processor) processUpload(ctx context.Context, e *domain.AvatarUploadEvent, messageID string) error {
	id, err := uuid.Parse(e.AvatarID)
	if err != nil {
		return fmt.Errorf("parse avatar id: %w", err)
	}

	avatar, err := p.Repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// Аватарки нет — помечаем событие обработанным и выходим.
			logger.L().Warn().Str("avatar_id", e.AvatarID).Msg("avatar not found, skipping")
			return p.Processed.Mark(messageID)
		}
		return err
	}

	if avatar.ProcessingStatus == domain.ProcessingStatusCompleted {
		logger.L().Debug().Str("avatar_id", e.AvatarID).Msg("already processed, skipping")
		return p.Processed.Mark(messageID)
	}

	if err := p.Repo.UpdateProcessingStatus(ctx, id, domain.ProcessingStatusProcessing); err != nil {
		return err
	}

	// Скачиваем оригинал.
	body, _, err := p.Storage.Download(ctx, e.S3Key)
	if err != nil {
		_ = p.Repo.UpdateProcessingStatus(ctx, id, domain.ProcessingStatusFailed)
		return fmt.Errorf("download original: %w", err)
	}
	defer func() { _ = body.Close() }()

	imgBytes, err := io.ReadAll(body)
	if err != nil {
		_ = p.Repo.UpdateProcessingStatus(ctx, id, domain.ProcessingStatusFailed)
		return fmt.Errorf("read body: %w", err)
	}

	img, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		_ = p.Repo.UpdateProcessingStatus(ctx, id, domain.ProcessingStatusFailed)
		return fmt.Errorf("decode image: %w", err)
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
		if err := imaging.Encode(buf, resized, imaging.JPEG, imaging.JPEGQuality(quality)); err != nil {
			_ = p.Repo.UpdateProcessingStatus(ctx, id, domain.ProcessingStatusFailed)
			return fmt.Errorf("encode thumbnail %dx%d: %w", op.Width, op.Height, err)
		}
		if err := p.Storage.UploadBytes(ctx, key, buf.Bytes(), "image/jpeg"); err != nil {
			_ = p.Repo.UpdateProcessingStatus(ctx, id, domain.ProcessingStatusFailed)
			return fmt.Errorf("upload thumbnail %s: %w", key, err)
		}
		thumbs[fmt.Sprintf("%dx%d", op.Width, op.Height)] = key
	}

	if err := p.Repo.UpdateThumbnails(ctx, id, thumbs); err != nil {
		_ = p.Repo.UpdateProcessingStatus(ctx, id, domain.ProcessingStatusFailed)
		return fmt.Errorf("update thumbnails: %w", err)
	}
	if err := p.Repo.UpdateProcessingStatus(ctx, id, domain.ProcessingStatusCompleted); err != nil {
		return err
	}

	if err := p.Processed.Mark(messageID); err != nil {
		logger.L().Warn().Err(err).Msg("idempotency mark failed")
	}
	logger.L().Info().
		Str("avatar_id", e.AvatarID).
		Int("thumbnails", len(thumbs)).
		Msg("avatar processed")
	return nil
}

// processDelete обрабатывает AvatarDeleted (best-effort удаление из S3).
func (p *Processor) processDelete(ctx context.Context, e *domain.AvatarDeleteEvent, messageID string) error {
	if len(e.S3Keys) == 0 {
		return p.Processed.Mark(messageID)
	}
	if err := p.Storage.Delete(ctx, e.S3Keys...); err != nil {
		// Логируем и всё равно помечаем — иначе зависнем на постоянной ошибке.
		logger.L().Warn().Err(err).Strs("keys", e.S3Keys).Msg("delete failed")
	}
	return p.Processed.Mark(messageID)
}
