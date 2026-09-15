package repository_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/minio"

	"github.com/tradekmv/gophprofile/internal/repository"
)

const (
	minioUser = "gophprof"
	minioPass = "gophprof-test-pass"
)

func startMinioStorage(t *testing.T) (*repository.S3Storage, func()) {
	t.Helper()
	ctx := context.Background()

	ctr, err := minio.Run(ctx, "quay.io/minio/minio:latest",
		minio.WithUsername(minioUser),
		minio.WithPassword(minioPass),
	)
	require.NoError(t, err, "start minio")

	endpoint, err := ctr.ConnectionString(ctx)
	require.NoError(t, err, "minio endpoint")
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "http://" + endpoint
	}

	storage, err := repository.NewS3Storage(ctx, repository.S3Config{
		Endpoint:  endpoint,
		Region:    "us-east-1",
		Bucket:    "test-bucket",
		AccessKey: minioUser,
		SecretKey: minioPass,
		UseSSL:    false,
	})
	require.NoError(t, err, "create storage")

	require.NoError(t, storage.EnsureBucket(ctx), "ensure bucket")

	cleanup := func() {
		_ = storage
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = ctr.Terminate(stopCtx)
	}
	return storage, cleanup
}

func TestS3UploadDownloadRoundTrip(t *testing.T) {
	storage, cleanup := startMinioStorage(t)
	defer cleanup()
	ctx := context.Background()

	key := "avatars/user-1/abc/test.jpg"
	payload := []byte("hello-minio-bytes")

	require.NoError(t, storage.Upload(ctx, key, bytes.NewReader(payload), int64(len(payload)), "image/jpeg"))

	body, contentType, err := storage.Download(ctx, key)
	require.NoError(t, err)
	defer body.Close()

	got, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, payload, got)
	require.Equal(t, "image/jpeg", contentType)
}

func TestS3DownloadMissing(t *testing.T) {
	storage, cleanup := startMinioStorage(t)
	defer cleanup()
	ctx := context.Background()

	_, _, err := storage.Download(ctx, "avatars/missing.jpg")
	require.ErrorIs(t, err, repository.ErrNotFoundInBucket)
}

func TestS3UploadBytes(t *testing.T) {
	storage, cleanup := startMinioStorage(t)
	defer cleanup()
	ctx := context.Background()

	key := "thumbnails/u/x/100x100.jpg"
	payload := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}
	require.NoError(t, storage.UploadBytes(ctx, key, payload, "image/jpeg"))

	body, _, err := storage.Download(ctx, key)
	require.NoError(t, err)
	defer body.Close()

	got, err := io.ReadAll(body)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(got, []byte{0xFF, 0xD8}))
}

func TestS3DeleteMultiple(t *testing.T) {
	storage, cleanup := startMinioStorage(t)
	defer cleanup()
	ctx := context.Background()

	keys := []string{
		"avatars/u/x/test.jpg",
		"thumbnails/u/x/100x100.jpg",
		"thumbnails/u/x/300x300.jpg",
	}
	for _, k := range keys {
		require.NoError(t, storage.UploadBytes(ctx, k, []byte("data"), "image/jpeg"))
	}

	require.NoError(t, storage.Delete(ctx, keys...))

	for _, k := range keys {
		_, _, err := storage.Download(ctx, k)
		require.ErrorIs(t, err, repository.ErrNotFoundInBucket, "key %s should be gone", k)
	}
}

func TestS3DeleteIgnoresMissing(t *testing.T) {
	storage, cleanup := startMinioStorage(t)
	defer cleanup()
	ctx := context.Background()

	require.NoError(t, storage.Delete(ctx, "missing/key1", "missing/key2"))
	require.NoError(t, storage.Delete(ctx)) // no keys
}

func TestS3EnsureBucketIdempotent(t *testing.T) {
	storage, cleanup := startMinioStorage(t)
	defer cleanup()
	ctx := context.Background()

	// Already ensured during start; calling again must succeed.
	require.NoError(t, storage.EnsureBucket(ctx))
}

func TestS3KeyValidation(t *testing.T) {
	storage, cleanup := startMinioStorage(t)
	defer cleanup()
	ctx := context.Background()

	err := storage.Upload(ctx, "", bytes.NewReader([]byte("x")), 1, "text/plain")
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "empty key")
}

func TestNewS3StorageValidations(t *testing.T) {
	t.Parallel()
	_, err := repository.NewS3Storage(context.Background(), repository.S3Config{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "bucket")
}
