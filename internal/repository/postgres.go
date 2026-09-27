// Package repository — реализации персистентности.
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tradekmv/gophprofile/internal/domain"
)

// Sentinel-ошибки репозитория.
var (
	ErrNotFound = errors.New("avatar not found")
)

// AvatarRepository — интерфейс персистентности для аватарок.
type AvatarRepository interface {
	Create(ctx context.Context, a *domain.Avatar) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Avatar, error)
	GetActiveByID(ctx context.Context, id uuid.UUID) (*domain.Avatar, error)
	GetActiveByUserID(ctx context.Context, userID string) (*domain.Avatar, error)
	ListByUserID(ctx context.Context, userID string, limit int) ([]*domain.Avatar, error)
	UpdateProcessingStatus(ctx context.Context, id uuid.UUID, status string) error
	UpdateThumbnails(ctx context.Context, id uuid.UUID, keys map[string]string) error
	MarkUploadStatus(ctx context.Context, id uuid.UUID, status string) error
	SoftDelete(ctx context.Context, id uuid.UUID) error
	SoftDeleteAllByUserID(ctx context.Context, userID string) ([]*domain.Avatar, error)
}

// PostgresAvatarRepo — реализация на pgx.
type PostgresAvatarRepo struct {
	pool *pgxpool.Pool
}

// NewPostgresAvatarRepo создаёт репозиторий на указанном пуле.
func NewPostgresAvatarRepo(pool *pgxpool.Pool) *PostgresAvatarRepo {
	return &PostgresAvatarRepo{pool: pool}
}

// NewPool создаёт пул соединений pgx по DSN.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = 30 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return pool, nil
}

// Create вставляет новую запись об аватарке.
func (r *PostgresAvatarRepo) Create(ctx context.Context, a *domain.Avatar) error {
	const q = `
		INSERT INTO avatars
			(id, user_id, file_name, mime_type, size_bytes, s3_key,
			 upload_status, processing_status, created_at, updated_at)
		VALUES
			($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
	`
	_, err := r.pool.Exec(ctx, q,
		a.ID, a.UserID, a.FileName, a.MimeType, a.SizeBytes, a.S3Key,
		a.UploadStatus, a.ProcessingStatus,
	)
	if err != nil {
		return fmt.Errorf("insert avatar: %w", err)
	}
	return nil
}

// GetByID получает аватарку по id (включая soft-deleted).
func (r *PostgresAvatarRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Avatar, error) {
	const q = `
		SELECT id, user_id, file_name, mime_type, size_bytes, s3_key,
		       thumbnail_s3_keys, upload_status, processing_status,
		       created_at, updated_at, deleted_at
		FROM avatars WHERE id = $1
	`
	row := r.pool.QueryRow(ctx, q, id)
	a, err := scanAvatar(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return a, nil
}

// GetActiveByID возвращает не удалённую аватарку по id.
func (r *PostgresAvatarRepo) GetActiveByID(ctx context.Context, id uuid.UUID) (*domain.Avatar, error) {
	const q = `
		SELECT id, user_id, file_name, mime_type, size_bytes, s3_key,
		       thumbnail_s3_keys, upload_status, processing_status,
		       created_at, updated_at, deleted_at
		FROM avatars WHERE id = $1 AND deleted_at IS NULL
	`
	row := r.pool.QueryRow(ctx, q, id)
	a, err := scanAvatar(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return a, nil
}

// GetActiveByUserID возвращает последнюю не удалённую аватарку пользователя.
func (r *PostgresAvatarRepo) GetActiveByUserID(ctx context.Context, userID string) (*domain.Avatar, error) {
	const q = `
		SELECT id, user_id, file_name, mime_type, size_bytes, s3_key,
		       thumbnail_s3_keys, upload_status, processing_status,
		       created_at, updated_at, deleted_at
		FROM avatars
		WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT 1
	`
	row := r.pool.QueryRow(ctx, q, userID)
	a, err := scanAvatar(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return a, nil
}

// ListByUserID возвращает до limit не удалённых аватарок пользователя (новые первые).
func (r *PostgresAvatarRepo) ListByUserID(ctx context.Context, userID string, limit int) ([]*domain.Avatar, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const q = `
		SELECT id, user_id, file_name, mime_type, size_bytes, s3_key,
		       thumbnail_s3_keys, upload_status, processing_status,
		       created_at, updated_at, deleted_at
		FROM avatars
		WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT $2
	`
	rows, err := r.pool.Query(ctx, q, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("query list: %w", err)
	}
	defer rows.Close()

	var out []*domain.Avatar
	for rows.Next() {
		a, err := scanAvatar(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateProcessingStatus обновляет processing_status и обновляет updated_at.
func (r *PostgresAvatarRepo) UpdateProcessingStatus(ctx context.Context, id uuid.UUID, status string) error {
	const q = `UPDATE avatars SET processing_status=$1, updated_at=NOW() WHERE id=$2`
	_, err := r.pool.Exec(ctx, q, status, id)
	return err
}

// UpdateThumbnails сохраняет карту миниатюр (размер -> S3-ключ).
func (r *PostgresAvatarRepo) UpdateThumbnails(ctx context.Context, id uuid.UUID, keys map[string]string) error {
	b, err := json.Marshal(keys)
	if err != nil {
		return fmt.Errorf("marshal thumbnails: %w", err)
	}
	const q = `UPDATE avatars SET thumbnail_s3_keys=$1, updated_at=NOW() WHERE id=$2`
	_, err = r.pool.Exec(ctx, q, b, id)
	return err
}

// MarkUploadStatus обновляет upload_status.
func (r *PostgresAvatarRepo) MarkUploadStatus(ctx context.Context, id uuid.UUID, status string) error {
	const q = `UPDATE avatars SET upload_status=$1, updated_at=NOW() WHERE id=$2`
	_, err := r.pool.Exec(ctx, q, status, id)
	return err
}

// SoftDelete помечает аватарку как удалённую.
func (r *PostgresAvatarRepo) SoftDelete(ctx context.Context, id uuid.UUID) error {
	const q = `UPDATE avatars SET deleted_at=NOW() WHERE id=$1 AND deleted_at IS NULL`
	tag, err := r.pool.Exec(ctx, q, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// rowScanner абстрагирует pgx.Row и pgx.Rows для общего сканирования.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanAvatar(s rowScanner) (*domain.Avatar, error) {
	var (
		a       domain.Avatar
		thumbs  []byte
		deleted *time.Time
	)
	err := s.Scan(
		&a.ID, &a.UserID, &a.FileName, &a.MimeType, &a.SizeBytes, &a.S3Key,
		&thumbs, &a.UploadStatus, &a.ProcessingStatus,
		&a.CreatedAt, &a.UpdatedAt, &deleted,
	)
	if err != nil {
		return nil, err
	}
	if len(thumbs) > 0 {
		if err := json.Unmarshal(thumbs, &a.ThumbnailS3Keys); err != nil {
			return nil, fmt.Errorf("unmarshal thumbnails: %w", err)
		}
	}
	a.DeletedAt = deleted
	return &a, nil
}

// SoftDeleteAllByUserID помечает deleted_at на всех не удалённых аватарках
// пользователя. Возвращает список затронутых аватарок (для publish delete events).
func (r *PostgresAvatarRepo) SoftDeleteAllByUserID(ctx context.Context, userID string) ([]*domain.Avatar, error) {
	const q = `
		UPDATE avatars SET deleted_at=NOW()
		WHERE user_id = $1 AND deleted_at IS NULL
		RETURNING id, user_id, file_name, mime_type, size_bytes, s3_key,
		          thumbnail_s3_keys, upload_status, processing_status,
		          created_at, updated_at, deleted_at
	`
	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("query soft delete: %w", err)
	}
	defer rows.Close()

	var out []*domain.Avatar
	for rows.Next() {
		a, err := scanAvatar(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
