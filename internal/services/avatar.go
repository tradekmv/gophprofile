// Package services — бизнес-логика, оркестрирующая репозитории и брокер.
package services

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tradekmv/gophprofile/internal/broker"
	"github.com/tradekmv/gophprofile/internal/domain"
	"github.com/tradekmv/gophprofile/internal/repository"
	"github.com/tradekmv/gophprofile/pkg/imgutil"
	"github.com/tradekmv/gophprofile/pkg/logger"
)

// ThumbnailSize — один вариант миниатюры для воркера.
type ThumbnailSize struct {
	Width  int
	Height int
}

// ParseThumbnailSizes парсит "100x100,300x300" в []ThumbnailSize.
// Размеры должны быть квадратными.
func ParseThumbnailSizes(raw []string) ([]ThumbnailSize, error) {
	out := make([]ThumbnailSize, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		w, h, err := imgutil.ParseSize(s)
		if err != nil {
			return nil, fmt.Errorf("invalid thumbnail size %q: %w", s, err)
		}
		if w != h {
			return nil, fmt.Errorf("thumbnail size must be square, got %q", s)
		}
		out = append(out, ThumbnailSize{Width: w, Height: h})
	}
	if len(out) == 0 {
		return nil, errors.New("no thumbnail sizes configured")
	}
	return out, nil
}

// Псевдонимы интерфейсов для короткого использования внутри пакета.
type (
	AvatarRepo = repository.AvatarRepository
	Storage    = repository.ObjectStorage
)

// AvatarService оркестрирует операции с аватарками.
// Поля приватные — состояние объекта задаётся только через NewAvatarService.
type AvatarService struct {
	repo           AvatarRepo
	storage        Storage
	publisher      broker.Publisher
	baseURL        string
	thumbnailSizes []ThumbnailSize
	maxUploadSize  int64
}

// NewAvatarService создаёт AvatarService.
func NewAvatarService(repo AvatarRepo, storage Storage, pub broker.Publisher, baseURL string, sizes []ThumbnailSize, maxUpload int64) *AvatarService {
	return &AvatarService{
		repo:           repo,
		storage:        storage,
		publisher:      pub,
		baseURL:        baseURL,
		thumbnailSizes: sizes,
		maxUploadSize:  maxUpload,
	}
}

// Upload проверяет файл, сохраняет в S3, создаёт запись в БД и публикует событие.
func (s *AvatarService) Upload(ctx context.Context, userID string, file io.Reader, fileName string, size int64) (*UploadResult, error) {
	if userID == "" {
		return nil, domain.ErrForbidden // нет пользователя — нет действия
	}
	if s.maxUploadSize > 0 && size > s.maxUploadSize {
		return nil, errors.New("payload too large")
	}
	br := bufio.NewReader(file)
	mime, err := imgutil.DetectMimeType(br)
	if err != nil {
		return nil, domain.ErrInvalidImage
	}

	avatarID := uuid.New()
	s3Key := s.originalKey(userID, avatarID, fileName)

	// Читаем тело целиком, чтобы знать точный размер после multipart.
	bodyBytes, err := io.ReadAll(br)
	if err != nil {
		return nil, fmt.Errorf("read upload body: %w", err)
	}

	if err := s.storage.UploadBytes(ctx, s3Key, bodyBytes, mime); err != nil {
		return nil, fmt.Errorf("upload to storage: %w", err)
	}

	avatar := &domain.Avatar{
		ID:               avatarID,
		UserID:           userID,
		FileName:         fileName,
		MimeType:         mime,
		SizeBytes:        int64(len(bodyBytes)),
		S3Key:            s3Key,
		UploadStatus:     domain.UploadStatusUploaded,
		ProcessingStatus: domain.ProcessingStatusPending,
	}
	if err := s.repo.Create(ctx, avatar); err != nil {
		// Если БД не сохранила — убираем файл из S3 (best-effort).
		_ = s.storage.Delete(ctx, s3Key)
		return nil, fmt.Errorf("persist avatar: %w", err)
	}

	// Список операций для воркера.
	ops := make([]domain.ProcessingOp, 0, len(s.thumbnailSizes))
	for _, sz := range s.thumbnailSizes {
		ops = append(ops, domain.ProcessingOp{Type: "resize", Width: sz.Width, Height: sz.Height})
	}

	if err := s.publisher.PublishUpload(ctx, domain.AvatarUploadEvent{
		AvatarID:     avatar.ID.String(),
		UserID:       avatar.UserID,
		S3Key:        avatar.S3Key,
		OriginalSize: avatar.SizeBytes,
		MimeType:     avatar.MimeType,
		Operations:   ops,
		UploadedAt:   time.Now().UTC(),
	}); err != nil {
		// Событие не доставлено — запись в pending остаётся. Re-enqueue job (вне MVP).
		logger.L().Error().Err(err).Str("avatar_id", avatar.ID.String()).Msg("publish failed; row left pending")
	}

	return &UploadResult{
		Avatar:  avatar,
		URL:     s.baseURL + "/api/v1/avatars/" + avatar.ID.String(),
		Message: "avatar accepted; processing asynchronously",
	}, nil
}

// GetMetadata возвращает метаданные не удалённой аватарки.
func (s *AvatarService) GetMetadata(ctx context.Context, id uuid.UUID) (*domain.Avatar, error) {
	a, err := s.repo.GetActiveByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, domain.ErrAvatarNotFound
	}
	return a, err
}

// GetBinary возвращает тело аватарки (или миниатюры). Только для активных аватарок.
func (s *AvatarService) GetBinary(ctx context.Context, id uuid.UUID, size string) (io.ReadCloser, string, error) {
	a, err := s.repo.GetActiveByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, "", domain.ErrAvatarNotFound
	}
	if err != nil {
		return nil, "", err
	}

	key := a.ThumbnailKey(size)
	if key == "" {
		return nil, "", domain.ErrThumbnailNotReady
	}
	body, ct, err := s.storage.Download(ctx, key)
	if errors.Is(err, repository.ErrNotFoundInBucket) {
		return nil, "", domain.ErrThumbnailNotReady
	}
	if err != nil {
		return nil, "", err
	}
	if ct == "" {
		ct = a.MimeType
	}
	return body, ct, nil
}

// GetByUserID возвращает активную аватарку пользователя.
func (s *AvatarService) GetByUserID(ctx context.Context, userID string) (*domain.Avatar, error) {
	a, err := s.repo.GetActiveByUserID(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, domain.ErrAvatarNotFound
	}
	return a, err
}

// ListByUserID возвращает до limit активных аватарок пользователя.
func (s *AvatarService) ListByUserID(ctx context.Context, userID string, limit int) ([]*domain.Avatar, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.repo.ListByUserID(ctx, userID, limit)
}

// Delete помечает аватарку как удалённую и публикует событие на очистку S3.
func (s *AvatarService) Delete(ctx context.Context, id uuid.UUID, requesterUserID string) error {
	a, err := s.repo.GetByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.ErrAvatarNotFound
	}
	if err != nil {
		return err
	}
	if a.UserID != requesterUserID {
		return domain.ErrForbidden
	}
	if err := s.repo.SoftDelete(ctx, id); err != nil {
		return err
	}

	keys := []string{a.S3Key}
	for _, k := range a.ThumbnailS3Keys {
		keys = append(keys, k)
	}
	if err := s.publisher.PublishDelete(ctx, domain.AvatarDeleteEvent{
		AvatarID:  id.String(),
		UserID:    requesterUserID,
		S3Keys:    keys,
		DeletedAt: time.Now().UTC(),
	}); err != nil {
		// Лог, но не валим удаление — БД уже soft-deleted.
		logger.L().Error().Err(err).Str("avatar_id", id.String()).Msg("publish delete event failed")
	}
	return nil
}

// DeleteAllByUserID помечает все аватарки пользователя как удалённые и
// публикует delete-события для каждой. Возвращает количество удалённых.
func (s *AvatarService) DeleteAllByUserID(ctx context.Context, userID string) (int, error) {
	avatars, err := s.repo.SoftDeleteAllByUserID(ctx, userID)
	if err != nil {
		return 0, err
	}
	for _, a := range avatars {
		keys := []string{a.S3Key}
		for _, k := range a.ThumbnailS3Keys {
			keys = append(keys, k)
		}
		if err := s.publisher.PublishDelete(ctx, domain.AvatarDeleteEvent{
			AvatarID:  a.ID.String(),
			UserID:    userID,
			S3Keys:    keys,
			DeletedAt: time.Now().UTC(),
		}); err != nil {
			logger.L().Error().Err(err).Str("avatar_id", a.ID.String()).Msg("publish delete event failed")
		}
	}
	return len(avatars), nil
}

// originalKey строит S3-ключ для оригинального файла аватарки.
func (s *AvatarService) originalKey(userID string, id uuid.UUID, fileName string) string {
	return fmt.Sprintf("avatars/%s/%s/%s", userID, id.String(), sanitizeFilename(fileName))
}

// ThumbnailKey строит S3-ключ для миниатюры.
func ThumbnailKey(userID string, id uuid.UUID, size int) string {
	return fmt.Sprintf("thumbnails/%s/%s/%dx%d.jpg", userID, id.String(), size, size)
}

// sanitizeFilename убирает path separators из имени файла.
func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "..", "")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	if name == "" {
		name = "avatar"
	}
	if len(name) > 200 {
		name = name[len(name)-200:]
	}
	return name
}
