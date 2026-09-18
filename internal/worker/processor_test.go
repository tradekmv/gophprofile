package worker_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/tradekmv/gophprofile/internal/domain"
	"github.com/tradekmv/gophprofile/internal/repository"
	"github.com/tradekmv/gophprofile/internal/worker"
)

// inMemoryRepo is an in-memory AvatarRepository for unit tests.
type inMemoryRepo struct {
	mu    sync.Mutex
	items map[uuid.UUID]*domain.Avatar
}

func newInMemoryRepo() *inMemoryRepo {
	return &inMemoryRepo{items: map[uuid.UUID]*domain.Avatar{}}
}

func (r *inMemoryRepo) Create(_ context.Context, a *domain.Avatar) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *a
	r.items[a.ID] = &cp
	return nil
}
func (r *inMemoryRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Avatar, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.items[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	cp := *a
	return &cp, nil
}
func (r *inMemoryRepo) GetActiveByID(_ context.Context, id uuid.UUID) (*domain.Avatar, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.items[id]
	if !ok || a.DeletedAt != nil {
		return nil, repository.ErrNotFound
	}
	cp := *a
	return &cp, nil
}
func (r *inMemoryRepo) GetActiveByUserID(_ context.Context, uid string) (*domain.Avatar, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var newest *domain.Avatar
	for _, a := range r.items {
		if a.UserID != uid || a.DeletedAt != nil {
			continue
		}
		if newest == nil || a.CreatedAt.After(newest.CreatedAt) {
			newest = a
		}
	}
	if newest == nil {
		return nil, repository.ErrNotFound
	}
	cp := *newest
	return &cp, nil
}
func (r *inMemoryRepo) ListByUserID(_ context.Context, uid string, _ int) ([]*domain.Avatar, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.Avatar
	for _, a := range r.items {
		if a.UserID == uid && a.DeletedAt == nil {
			cp := *a
			out = append(out, &cp)
		}
	}
	return out, nil
}
func (r *inMemoryRepo) UpdateProcessingStatus(_ context.Context, id uuid.UUID, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a, ok := r.items[id]; ok {
		a.ProcessingStatus = status
	}
	return nil
}
func (r *inMemoryRepo) UpdateThumbnails(_ context.Context, id uuid.UUID, keys map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a, ok := r.items[id]; ok {
		a.ThumbnailS3Keys = keys
	}
	return nil
}
func (r *inMemoryRepo) MarkUploadStatus(_ context.Context, id uuid.UUID, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a, ok := r.items[id]; ok {
		a.UploadStatus = status
	}
	return nil
}
func (r *inMemoryRepo) SoftDelete(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a, ok := r.items[id]; ok {
		now := time.Now()
		a.DeletedAt = &now
	}
	return nil
}
func (r *inMemoryRepo) SoftDeleteAllByUserID(_ context.Context, uid string) ([]*domain.Avatar, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.Avatar
	now := time.Now()
	for _, a := range r.items {
		if a.UserID == uid && a.DeletedAt == nil {
			a.DeletedAt = &now
			cp := *a
			out = append(out, &cp)
		}
	}
	return out, nil
}

// testStorage stores uploads by key.
type testStorage struct {
	mu     sync.Mutex
	blobs  map[string][]byte
	failDL bool
}

func newTestStorage() *testStorage {
	return &testStorage{blobs: map[string][]byte{}}
}

func (s *testStorage) Upload(_ context.Context, key string, body io.Reader, _ int64, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, _ := io.ReadAll(body)
	s.blobs[key] = data
	return nil
}
func (s *testStorage) UploadBytes(ctx context.Context, key string, data []byte, _ string) error {
	return s.Upload(ctx, key, bytes.NewReader(data), int64(len(data)), "")
}
func (s *testStorage) Download(_ context.Context, key string) (io.ReadCloser, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failDL {
		return nil, "", errors.New("boom")
	}
	b, ok := s.blobs[key]
	if !ok {
		return nil, "", repository.ErrNotFoundInBucket
	}
	return io.NopCloser(bytes.NewReader(b)), "image/jpeg", nil
}
func (s *testStorage) Delete(_ context.Context, keys ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		delete(s.blobs, k)
	}
	return nil
}

// seedJPEG returns a small in-memory JPEG (white 16x16) for testing.
func seedJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	buf := new(bytes.Buffer)
	require.NoError(t, jpeg.Encode(buf, img, &jpeg.Options{Quality: 70}))
	return buf.Bytes()
}

// newProcessor wires a processor with in-memory repo + storage + store.
func newProcessor(t *testing.T) (*worker.Processor, *inMemoryRepo, *testStorage, *worker.InMemoryProcessedStore) {
	t.Helper()
	repo := newInMemoryRepo()
	st := newTestStorage()
	seen := worker.NewInMemoryProcessedStore(0)
	p := worker.NewProcessor(repo, st, seen)
	return p, repo, st, seen
}

func TestProcessUploadHappyPath(t *testing.T) {
	t.Parallel()
	p, repo, st, _ := newProcessor(t)

	id := uuid.New()
	require.NoError(t, repo.Create(context.Background(), &domain.Avatar{
		ID:               id,
		UserID:           "user-1",
		FileName:         "p.jpg",
		MimeType:         "image/jpeg",
		SizeBytes:        10,
		S3Key:            "avatars/user-1/" + id.String() + "/p.jpg",
		UploadStatus:     domain.UploadStatusUploaded,
		ProcessingStatus: domain.ProcessingStatusPending,
	}))
	require.NoError(t, st.UploadBytes(context.Background(), "avatars/user-1/"+id.String()+"/p.jpg", seedJPEG(t), "image/jpeg"))

	event := &domain.AvatarUploadEvent{
		AvatarID:     id.String(),
		UserID:       "user-1",
		S3Key:        "avatars/user-1/" + id.String() + "/p.jpg",
		OriginalSize: 10,
		MimeType:     "image/jpeg",
		Operations: []domain.ProcessingOp{
			{Type: "resize", Width: 100, Height: 100},
			{Type: "resize", Width: 300, Height: 300},
		},
	}
	body, err := jsonMarshal(event)
	require.NoError(t, err)

	require.NoError(t, p.Handle(context.Background(), []byte(domain.EventTypeUpload), []byte(id.String()), body))

	a, err := repo.GetByID(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, domain.ProcessingStatusCompleted, a.ProcessingStatus)
	require.Len(t, a.ThumbnailS3Keys, 2)
	require.Contains(t, a.ThumbnailS3Keys, "100x100")
	require.Contains(t, a.ThumbnailS3Keys, "300x300")
	require.NotZero(t, len(st.blobs[a.ThumbnailS3Keys["100x100"]]))

	// Idempotency: second call is a no-op (no error).
	require.NoError(t, p.Handle(context.Background(), []byte(domain.EventTypeUpload), []byte(id.String()), body))
	a2, _ := repo.GetByID(context.Background(), id)
	require.Equal(t, a.UpdatedAt, a2.UpdatedAt)
}

func TestProcessUploadAlreadyCompleted(t *testing.T) {
	t.Parallel()
	p, repo, st, _ := newProcessor(t)

	id := uuid.New()
	require.NoError(t, repo.Create(context.Background(), &domain.Avatar{
		ID:               id,
		UserID:           "u",
		FileName:         "p.jpg",
		MimeType:         "image/jpeg",
		SizeBytes:        10,
		S3Key:            "k",
		ProcessingStatus: domain.ProcessingStatusCompleted,
	}))
	require.NoError(t, st.UploadBytes(context.Background(), "k", seedJPEG(t), "image/jpeg"))

	event := &domain.AvatarUploadEvent{AvatarID: id.String(), UserID: "u", S3Key: "k", Operations: []domain.ProcessingOp{{Type: "resize", Width: 100, Height: 100}}}
	body, err := jsonMarshal(event)
	require.NoError(t, err)

	require.NoError(t, p.Handle(context.Background(), []byte(domain.EventTypeUpload), []byte(id.String()), body))
	a, _ := repo.GetByID(context.Background(), id)
	require.Equal(t, domain.ProcessingStatusCompleted, a.ProcessingStatus)
}

func TestProcessUploadAvatarNotFound(t *testing.T) {
	t.Parallel()
	p, _, _, _ := newProcessor(t)

	event := &domain.AvatarUploadEvent{AvatarID: uuid.New().String(), UserID: "u", S3Key: "k"}
	body, err := jsonMarshal(event)
	require.NoError(t, err)

	// Per Processor contract: not-found is treated as terminal (mark + nil).
	require.NoError(t, p.Handle(context.Background(), []byte(domain.EventTypeUpload), []byte("msg-1"), body))
}

func TestProcessUploadDownloadError(t *testing.T) {
	t.Parallel()
	p, repo, st, _ := newProcessor(t)
	st.failDL = true

	id := uuid.New()
	require.NoError(t, repo.Create(context.Background(), &domain.Avatar{
		ID:               id,
		UserID:           "u",
		FileName:         "p.jpg",
		MimeType:         "image/jpeg",
		SizeBytes:        1,
		S3Key:            "k",
		ProcessingStatus: domain.ProcessingStatusPending,
	}))

	event := &domain.AvatarUploadEvent{AvatarID: id.String(), UserID: "u", S3Key: "k"}
	body, err := jsonMarshal(event)
	require.NoError(t, err)

	err = p.Handle(context.Background(), []byte(domain.EventTypeUpload), []byte("m"), body)
	require.Error(t, err)
	a, _ := repo.GetByID(context.Background(), id)
	require.Equal(t, domain.ProcessingStatusFailed, a.ProcessingStatus)
}

func TestProcessDelete(t *testing.T) {
	t.Parallel()
	p, _, st, _ := newProcessor(t)
	require.NoError(t, st.UploadBytes(context.Background(), "a", []byte("x"), "image/jpeg"))
	require.NoError(t, st.UploadBytes(context.Background(), "b", []byte("y"), "image/jpeg"))

	event := &domain.AvatarDeleteEvent{AvatarID: uuid.New().String(), UserID: "u", S3Keys: []string{"a", "b", "missing"}}
	body, err := jsonMarshal(event)
	require.NoError(t, err)

	require.NoError(t, p.Handle(context.Background(), []byte(domain.EventTypeDelete), []byte("m1"), body))
	_, ok := st.blobs["a"]
	require.False(t, ok)
	_, ok = st.blobs["b"]
	require.False(t, ok)
}

func TestProcessUnknownEventType(t *testing.T) {
	t.Parallel()
	p, _, _, _ := newProcessor(t)
	require.NoError(t, p.Handle(context.Background(), []byte("unknown.type"), []byte("m"), []byte("{}")))
}

func TestIdempotencyTTL(t *testing.T) {
	t.Parallel()
	store := worker.NewInMemoryProcessedStore(50 * time.Millisecond)
	require.NoError(t, store.Mark("x"))
	seen, _ := store.Seen("x")
	require.True(t, seen)

	await := make(chan struct{})
	go func() {
		time.Sleep(80 * time.Millisecond)
		close(await)
	}()
	<-await
	seen, _ = store.Seen("x")
	require.False(t, seen)
}

// jsonMarshal is provided by testhelpers_test.go.
