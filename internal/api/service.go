// Package api — HTTP-хендлеры, middleware и DTO.
package api

import (
	"context"
	"io"

	"github.com/google/uuid"

	"github.com/tradekmv/gophprofile/internal/domain"
	"github.com/tradekmv/gophprofile/internal/services"
)

// AvatarService — интерфейс, который HTTP-слой использует от бизнес-логики.
// Реализация — в internal/services/avatar.go. UploadResult переэкспортирован
// из services, чтобы handlers не зависели от internal/services.
type AvatarService interface {
	Upload(ctx context.Context, userID string, file io.Reader, fileName string, size int64) (*UploadResult, error)
	GetMetadata(ctx context.Context, id uuid.UUID) (*domain.Avatar, error)
	GetBinary(ctx context.Context, id uuid.UUID, size string) (io.ReadCloser, string, error)
	GetByUserID(ctx context.Context, userID string) (*domain.Avatar, error)
	ListByUserID(ctx context.Context, userID string, limit int) ([]*domain.Avatar, error)
	Delete(ctx context.Context, id uuid.UUID, requesterUserID string) error
	DeleteAllByUserID(ctx context.Context, userID string) (int, error)
}

// UploadResult — алиас services.UploadResult.
type UploadResult = services.UploadResult

// HealthChecker пингует зависимость для /health.
type HealthChecker interface {
	Name() string
	Check(ctx context.Context) error
}
