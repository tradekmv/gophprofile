package services_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/tradekmv/gophprofile/internal/broker"
	"github.com/tradekmv/gophprofile/internal/domain"
	"github.com/tradekmv/gophprofile/internal/repository"
	"github.com/tradekmv/gophprofile/internal/services"
)

// fakeRepo is an in-memory implementation of AvatarRepository.
type fakeRepo struct {
	items  map[uuid.UUID]*domain.Avatar
	create func(ctx context.Context, a *domain.Avatar) error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{items: map[uuid.UUID]*domain.Avatar{}}
}

func (f *fakeRepo) Create(ctx context.Context, a *domain.Avatar) error {
	if f.create != nil {
		return f.create(ctx, a)
	}
	f.items[a.ID] = a
	return nil
}
func (f *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Avatar, error) {
	a, ok := f.items[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	cp := *a
	return &cp, nil
}
func (f *fakeRepo) GetActiveByID(_ context.Context, id uuid.UUID) (*domain.Avatar, error) {
	a, ok := f.items[id]
	if !ok || a.DeletedAt != nil {
		return nil, repository.ErrNotFound
	}
	cp := *a
	return &cp, nil
}
func (f *fakeRepo) GetActiveByUserID(_ context.Context, uid string) (*domain.Avatar, error) {
	var newest *domain.Avatar
	for _, a := range f.items {
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
func (f *fakeRepo) ListByUserID(_ context.Context, uid string, limit int) ([]*domain.Avatar, error) {
	var out []*domain.Avatar
	for _, a := range f.items {
		if a.UserID == uid && a.DeletedAt == nil {
			cp := *a
			out = append(out, &cp)
		}
	}
	return out, nil
}
func (f *fakeRepo) UpdateProcessingStatus(_ context.Context, id uuid.UUID, status string) error {
	if a, ok := f.items[id]; ok {
		a.ProcessingStatus = status
	}
	return nil
}
func (f *fakeRepo) UpdateThumbnails(_ context.Context, id uuid.UUID, keys map[string]string) error {
	if a, ok := f.items[id]; ok {
		a.ThumbnailS3Keys = keys
	}
	return nil
}
func (f *fakeRepo) MarkUploadStatus(_ context.Context, id uuid.UUID, status string) error {
	if a, ok := f.items[id]; ok {
		a.UploadStatus = status
	}
	return nil
}
func (f *fakeRepo) SoftDelete(_ context.Context, id uuid.UUID) error {
	if a, ok := f.items[id]; ok {
		now := time.Now()
		a.DeletedAt = &now
		return nil
	}
	return repository.ErrNotFound
}
func (f *fakeRepo) SoftDeleteAllByUserID(_ context.Context, uid string) ([]*domain.Avatar, error) {
	var out []*domain.Avatar
	now := time.Now()
	for _, a := range f.items {
		if a.UserID == uid && a.DeletedAt == nil {
			a.DeletedAt = &now
			cp := *a
			out = append(out, &cp)
		}
	}
	return out, nil
}

// fakeStorage records uploads/downloads/deletes in memory.
type fakeStorage struct {
	uploads  []uploadCall
	download func(ctx context.Context, key string) (io.ReadCloser, string, error)
	deletes  [][]string
}

type uploadCall struct {
	key         string
	contentType string
	data        []byte
}

func (f *fakeStorage) Upload(_ context.Context, key string, body io.Reader, size int64, ct string) error {
	data, _ := io.ReadAll(body)
	f.uploads = append(f.uploads, uploadCall{key: key, contentType: ct, data: data})
	return nil
}
func (f *fakeStorage) UploadBytes(ctx context.Context, key string, data []byte, ct string) error {
	return f.Upload(ctx, key, bytes.NewReader(data), int64(len(data)), ct)
}
func (f *fakeStorage) Download(ctx context.Context, key string) (io.ReadCloser, string, error) {
	if f.download != nil {
		return f.download(ctx, key)
	}
	return io.NopCloser(bytes.NewReader([]byte("data"))), "image/jpeg", nil
}
func (f *fakeStorage) Delete(_ context.Context, keys ...string) error {
	f.deletes = append(f.deletes, keys)
	return nil
}

// recordingPublisher records published events for assertions.
type recordingPublisher struct {
	uploads []domain.AvatarUploadEvent
	deletes []domain.AvatarDeleteEvent
	failU   bool
}

func (r *recordingPublisher) PublishUpload(_ context.Context, e domain.AvatarUploadEvent) error {
	if r.failU {
		return errors.New("publish failed")
	}
	r.uploads = append(r.uploads, e)
	return nil
}
func (r *recordingPublisher) PublishDelete(_ context.Context, e domain.AvatarDeleteEvent) error {
	r.deletes = append(r.deletes, e)
	return nil
}
func (r *recordingPublisher) Close() error { return nil }

var _ broker.Publisher = (*recordingPublisher)(nil)

func validJPEG() []byte {
	return []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01, 0x02, 0x03, 0x04, 0x05}
}

func newSvc(t *testing.T) (*services.AvatarService, *fakeRepo, *fakeStorage, *recordingPublisher) {
	t.Helper()
	repo := newFakeRepo()
	st := &fakeStorage{}
	pub := &recordingPublisher{}
	sizes, err := services.ParseThumbnailSizes([]string{"100x100", "300x300"})
	require.NoError(t, err)
	svc := services.NewAvatarService(repo, st, pub, "http://example.com", sizes, 10<<20)
	return svc, repo, st, pub
}

func TestUploadHappyPath(t *testing.T) {
	t.Parallel()
	svc, repo, st, pub := newSvc(t)

	res, err := svc.Upload(context.Background(), "user-1", bytes.NewReader(validJPEG()), "pic.jpg", int64(len(validJPEG())))
	require.NoError(t, err)
	require.NotNil(t, res.Avatar)
	require.Equal(t, "user-1", res.Avatar.UserID)
	require.Equal(t, "image/jpeg", res.Avatar.MimeType)
	require.Equal(t, domain.ProcessingStatusPending, res.Avatar.ProcessingStatus)
	require.Contains(t, res.URL, res.Avatar.ID.String())

	require.Len(t, st.uploads, 1)
	require.Equal(t, res.Avatar.S3Key, st.uploads[0].key)
	require.Equal(t, "image/jpeg", st.uploads[0].contentType)

	require.Len(t, pub.uploads, 1)
	require.Equal(t, res.Avatar.ID.String(), pub.uploads[0].AvatarID)
	require.Equal(t, res.Avatar.S3Key, pub.uploads[0].S3Key)
	require.Len(t, pub.uploads[0].Operations, 2)

	// Row is in repo
	got, err := repo.GetByID(context.Background(), res.Avatar.ID)
	require.NoError(t, err)
	require.Equal(t, res.Avatar.S3Key, got.S3Key)
}

func TestUploadInvalidImage(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSvc(t)

	_, err := svc.Upload(context.Background(), "user-1", bytes.NewReader([]byte("not an image")), "x.txt", 12)
	require.ErrorIs(t, err, domain.ErrInvalidImage)
}

func TestUploadPublishFailureKeepsRow(t *testing.T) {
	t.Parallel()
	svc, repo, st, pub := newSvc(t)
	pub.failU = true

	res, err := svc.Upload(context.Background(), "user-1", bytes.NewReader(validJPEG()), "pic.jpg", int64(len(validJPEG())))
	require.NoError(t, err) // publish failure is non-fatal
	require.NotNil(t, res.Avatar)

	// Row still exists; can be picked up by a re-enqueue job later.
	_, err = repo.GetByID(context.Background(), res.Avatar.ID)
	require.NoError(t, err)
	// S3 object was uploaded.
	require.Len(t, st.uploads, 1)
}

func TestGetBinaryOriginal(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSvc(t)

	res, err := svc.Upload(context.Background(), "user-1", bytes.NewReader(validJPEG()), "p.jpg", int64(len(validJPEG())))
	require.NoError(t, err)

	body, ct, err := svc.GetBinary(context.Background(), res.Avatar.ID, domain.SizeOriginal)
	require.NoError(t, err)
	defer body.Close()
	require.Equal(t, "image/jpeg", ct)
}

func TestGetBinaryThumbnailNotReady(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSvc(t)

	res, err := svc.Upload(context.Background(), "user-1", bytes.NewReader(validJPEG()), "p.jpg", int64(len(validJPEG())))
	require.NoError(t, err)

	_, _, err = svc.GetBinary(context.Background(), res.Avatar.ID, "100x100")
	require.ErrorIs(t, err, domain.ErrThumbnailNotReady)
}

func TestGetBinaryAvatarNotFound(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSvc(t)

	_, _, err := svc.GetBinary(context.Background(), uuid.New(), domain.SizeOriginal)
	require.ErrorIs(t, err, domain.ErrAvatarNotFound)
}

func TestGetByUserIDNotFound(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSvc(t)

	_, err := svc.GetByUserID(context.Background(), "nobody")
	require.ErrorIs(t, err, domain.ErrAvatarNotFound)
}

func TestDeleteForbidden(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSvc(t)

	res, err := svc.Upload(context.Background(), "user-1", bytes.NewReader(validJPEG()), "p.jpg", int64(len(validJPEG())))
	require.NoError(t, err)

	err = svc.Delete(context.Background(), res.Avatar.ID, "intruder")
	require.ErrorIs(t, err, domain.ErrForbidden)
}

func TestDeleteSuccess(t *testing.T) {
	t.Parallel()
	svc, repo, _, pub := newSvc(t)

	res, err := svc.Upload(context.Background(), "user-1", bytes.NewReader(validJPEG()), "p.jpg", int64(len(validJPEG())))
	require.NoError(t, err)

	err = svc.Delete(context.Background(), res.Avatar.ID, "user-1")
	require.NoError(t, err)

	a, err := repo.GetByID(context.Background(), res.Avatar.ID)
	require.NoError(t, err)
	require.NotNil(t, a.DeletedAt)

	require.Len(t, pub.deletes, 1)
	require.Equal(t, res.Avatar.ID.String(), pub.deletes[0].AvatarID)
	require.Contains(t, pub.deletes[0].S3Keys, res.Avatar.S3Key)
}

func TestDeleteAllByUserID(t *testing.T) {
	t.Parallel()
	svc, repo, _, pub := newSvc(t)

	// Загружаем две аватарки для user-1 и одну для user-2.
	res1, err := svc.Upload(context.Background(), "user-1", bytes.NewReader(validJPEG()), "a.jpg", int64(len(validJPEG())))
	require.NoError(t, err)
	res2, err := svc.Upload(context.Background(), "user-1", bytes.NewReader(validJPEG()), "b.jpg", int64(len(validJPEG())))
	require.NoError(t, err)
	other, err := svc.Upload(context.Background(), "user-2", bytes.NewReader(validJPEG()), "c.jpg", int64(len(validJPEG())))
	require.NoError(t, err)

	pub.deletes = nil
	n, err := svc.DeleteAllByUserID(context.Background(), "user-1")
	require.NoError(t, err)
	require.Equal(t, 2, n)

	// Обе аватарки user-1 помечены удалёнными.
	a1, _ := repo.GetByID(context.Background(), res1.Avatar.ID)
	a2, _ := repo.GetByID(context.Background(), res2.Avatar.ID)
	require.NotNil(t, a1.DeletedAt)
	require.NotNil(t, a2.DeletedAt)
	// user-2 не тронут.
	aO, _ := repo.GetByID(context.Background(), other.Avatar.ID)
	require.Nil(t, aO.DeletedAt)
	// Опубликовано 2 delete-события.
	require.Len(t, pub.deletes, 2)
}

func TestParseThumbnailSizes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      []string
		wantErr bool
		wantLen int
	}{
		{"default", []string{"100x100", "300x300"}, false, 2},
		{"single", []string{"200x200"}, false, 1},
		{"empty", []string{}, true, 0},
		{"non-square", []string{"100x200"}, true, 0},
		{"malformed", []string{"abc"}, true, 0},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sizes, err := services.ParseThumbnailSizes(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if !tt.wantErr && len(sizes) != tt.wantLen {
				t.Errorf("got %d, want %d", len(sizes), tt.wantLen)
			}
		})
	}
}
