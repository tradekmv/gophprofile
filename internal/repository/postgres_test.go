package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/tradekmv/gophprofile/internal/domain"
	"github.com/tradekmv/gophprofile/internal/repository"
)

// pgContainer spins up a Postgres container with pgcrypto enabled and a
// fresh schema matching the 001_init migration.
type pgContainer struct {
	ctr *postgres.PostgresContainer
	pool *pgxpool.Pool
	dsn  string
}

func startPostgres(t *testing.T) *pgContainer {
	t.Helper()
	ctx := context.Background()

	ctr, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("gophprofile"),
		postgres.WithUsername("app"),
		postgres.WithPassword("app"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err, "start postgres container")

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "get dsn")

	pool, err := repository.NewPool(ctx, dsn)
	require.NoError(t, err, "create pool")

	// Inline schema (mirrors 001_init.up.sql — kept in sync intentionally so
	// tests don't need a filesystem path).
	schemaSQL := `
		CREATE EXTENSION IF NOT EXISTS pgcrypto;
		CREATE TABLE IF NOT EXISTS avatars (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id VARCHAR(255) NOT NULL,
			file_name VARCHAR(255) NOT NULL,
			mime_type VARCHAR(100) NOT NULL,
			size_bytes BIGINT NOT NULL,
			s3_key VARCHAR(500) NOT NULL,
			thumbnail_s3_keys JSONB,
			upload_status VARCHAR(50) NOT NULL DEFAULT 'uploaded',
			processing_status VARCHAR(50) NOT NULL DEFAULT 'pending',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMPTZ
		);
		CREATE INDEX IF NOT EXISTS idx_avatars_user_id ON avatars(user_id) WHERE deleted_at IS NULL;
		CREATE INDEX IF NOT EXISTS idx_avatars_status ON avatars(upload_status, processing_status);
	`
	_, err = pool.Exec(ctx, schemaSQL)
	require.NoError(t, err, "apply schema")

	return &pgContainer{ctr: ctr, pool: pool, dsn: dsn}
}

func (p *pgContainer) cleanup(t *testing.T) {
	t.Helper()
	p.pool.Close()
	require.NoError(t, p.ctr.Terminate(context.Background()))
}

func newAvatar(userID, s3Key string) *domain.Avatar {
	return &domain.Avatar{
		ID:               uuid.New(),
		UserID:           userID,
		FileName:         "test.jpg",
		MimeType:         "image/jpeg",
		SizeBytes:        1024,
		S3Key:            s3Key,
		UploadStatus:     domain.UploadStatusUploaded,
		ProcessingStatus: domain.ProcessingStatusPending,
	}
}

func TestCreateAndGetByID(t *testing.T) {
	pg := startPostgres(t)
	defer pg.cleanup(t)
	repo := repository.NewPostgresAvatarRepo(pg.pool)
	ctx := context.Background()

	a := newAvatar("user-1", "avatars/user-1/x/test.jpg")
	require.NoError(t, repo.Create(ctx, a))

	got, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, a.ID, got.ID)
	require.Equal(t, a.UserID, got.UserID)
	require.Equal(t, a.MimeType, got.MimeType)
	require.Equal(t, a.S3Key, got.S3Key)
	require.Equal(t, domain.ProcessingStatusPending, got.ProcessingStatus)
	require.Nil(t, got.DeletedAt)
}

func TestGetByIDNotFound(t *testing.T) {
	pg := startPostgres(t)
	defer pg.cleanup(t)
	repo := repository.NewPostgresAvatarRepo(pg.pool)
	ctx := context.Background()

	_, err := repo.GetByID(ctx, uuid.New())
	require.ErrorIs(t, err, repository.ErrNotFound)
}

func TestGetActiveByUserID_OnlyLatest(t *testing.T) {
	pg := startPostgres(t)
	defer pg.cleanup(t)
	repo := repository.NewPostgresAvatarRepo(pg.pool)
	ctx := context.Background()

	older := newAvatar("user-1", "avatars/user-1/older/test.jpg")
	older.CreatedAt = time.Now().Add(-time.Hour)
	require.NoError(t, repo.Create(ctx, older))

	newer := newAvatar("user-1", "avatars/user-1/newer/test.jpg")
	require.NoError(t, repo.Create(ctx, newer))

	got, err := repo.GetActiveByUserID(ctx, "user-1")
	require.NoError(t, err)
	require.Equal(t, newer.ID, got.ID, "should return the newest avatar")
}

func TestSoftDeleteHidesFromActive(t *testing.T) {
	pg := startPostgres(t)
	defer pg.cleanup(t)
	repo := repository.NewPostgresAvatarRepo(pg.pool)
	ctx := context.Background()

	a := newAvatar("user-1", "avatars/user-1/x/test.jpg")
	require.NoError(t, repo.Create(ctx, a))
	require.NoError(t, repo.SoftDelete(ctx, a.ID))

	_, err := repo.GetActiveByUserID(ctx, "user-1")
	require.ErrorIs(t, err, repository.ErrNotFound)

	// GetByID still returns the row with deleted_at set.
	got, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.NotNil(t, got.DeletedAt)
}

func TestUpdateProcessingStatusAndThumbnails(t *testing.T) {
	pg := startPostgres(t)
	defer pg.cleanup(t)
	repo := repository.NewPostgresAvatarRepo(pg.pool)
	ctx := context.Background()

	a := newAvatar("user-1", "avatars/user-1/x/test.jpg")
	require.NoError(t, repo.Create(ctx, a))

	require.NoError(t, repo.UpdateProcessingStatus(ctx, a.ID, domain.ProcessingStatusProcessing))
	got, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, domain.ProcessingStatusProcessing, got.ProcessingStatus)

	thumbs := map[string]string{
		"100x100": "thumbnails/user-1/x/100x100.jpg",
		"300x300": "thumbnails/user-1/x/300x300.jpg",
	}
	require.NoError(t, repo.UpdateThumbnails(ctx, a.ID, thumbs))
	got, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, thumbs, got.ThumbnailS3Keys)
	require.Equal(t, thumbs["100x100"], got.ThumbnailKey("100x100"))
	require.Equal(t, a.S3Key, got.ThumbnailKey(domain.SizeOriginal))
	require.Equal(t, "", got.ThumbnailKey("999x999"))
}

func TestListByUserID(t *testing.T) {
	pg := startPostgres(t)
	defer pg.cleanup(t)
	repo := repository.NewPostgresAvatarRepo(pg.pool)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		a := newAvatar("user-1", "avatars/u/x"+string(rune('a'+i)))
		require.NoError(t, repo.Create(ctx, a))
	}
	// other user
	other := newAvatar("user-2", "avatars/u/y")
	require.NoError(t, repo.Create(ctx, other))

	got, err := repo.ListByUserID(ctx, "user-1", 10)
	require.NoError(t, err)
	require.Len(t, got, 3)

	got, err = repo.ListByUserID(ctx, "user-2", 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestSoftDeleteNotFound(t *testing.T) {
	pg := startPostgres(t)
	defer pg.cleanup(t)
	repo := repository.NewPostgresAvatarRepo(pg.pool)
	ctx := context.Background()

	err := repo.SoftDelete(ctx, uuid.New())
	require.ErrorIs(t, err, repository.ErrNotFound)
}
