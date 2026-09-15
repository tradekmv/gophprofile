package domain

import "time"

// Тип события в заголовке Kafka.
const (
	EventTypeUpload = "avatar.uploaded"
	EventTypeDelete = "avatar.deleted"
	HeaderEventType = "event-type"
	HeaderMessageID = "message-id"
)

// ProcessingOp — одна операция обработки изображения.
type ProcessingOp struct {
	Type   string `json:"type"` // "resize"
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// AvatarUploadEvent публикуется после загрузки аватарки в S3 — сигнал
// воркеру, что нужно сделать миниатюры.
type AvatarUploadEvent struct {
	AvatarID     string         `json:"avatar_id"`
	UserID       string         `json:"user_id"`
	S3Key        string         `json:"s3_key"`
	OriginalSize int64          `json:"original_size"`
	MimeType     string         `json:"mime_type"`
	Operations   []ProcessingOp `json:"operations,omitempty"`
	UploadedAt   time.Time      `json:"uploaded_at"`
}

// AvatarDeleteEvent публикуется при soft-delete аватарки — воркер по нему
// удаляет оригинал и миниатюры из S3.
type AvatarDeleteEvent struct {
	AvatarID  string    `json:"avatar_id"`
	UserID    string    `json:"user_id"`
	S3Keys    []string  `json:"s3_keys"`
	DeletedAt time.Time `json:"deleted_at"`
}
