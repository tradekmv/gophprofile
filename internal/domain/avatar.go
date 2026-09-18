// Package domain — общие сущности и value-типы для всех слоёв.
package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Sentinel-ошибки, общие для всех слоёв.
var (
	ErrAvatarNotFound    = errors.New("avatar not found")
	ErrThumbnailNotReady = errors.New("thumbnail not ready")
	ErrForbidden         = errors.New("forbidden")
	ErrInvalidImage      = errors.New("invalid image")
)

// Статусы аватарки (совпадают с колонками в БД).
const (
	UploadStatusUploading = "uploading"
	UploadStatusUploaded  = "uploaded"

	ProcessingStatusPending    = "pending"
	ProcessingStatusProcessing = "processing"
	ProcessingStatusCompleted  = "completed"
	ProcessingStatusFailed     = "failed"
)

// Константы размеров миниатюр.
const (
	SizeOriginal = "original"
	Size100x100  = "100x100"
	Size300x300  = "300x300"
)

// Avatar — запись метаданных, хранимая в PostgreSQL.
type Avatar struct {
	ID               uuid.UUID
	UserID           string
	FileName         string
	MimeType         string
	SizeBytes        int64
	S3Key            string
	ThumbnailS3Keys  map[string]string // размер -> ключ (сейчас только JPEG)
	UploadStatus     string
	ProcessingStatus string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	DeletedAt        *time.Time
}

// IsDeleted возвращает true, если аватарка soft-deleted.
func (a *Avatar) IsDeleted() bool { return a.DeletedAt != nil }

// ThumbnailKey возвращает S3-ключ для миниатюры заданного размера.
// Для SizeOriginal (или пустой строки) возвращает S3Key оригинала.
func (a *Avatar) ThumbnailKey(size string) string {
	if size == SizeOriginal || size == "" {
		return a.S3Key
	}
	if a.ThumbnailS3Keys == nil {
		return ""
	}
	return a.ThumbnailS3Keys[size]
}
