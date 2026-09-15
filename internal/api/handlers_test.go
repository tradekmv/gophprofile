package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/tradekmv/gophprofile/internal/api"
	"github.com/tradekmv/gophprofile/internal/domain"
)

// fakeService is an in-memory implementation of api.AvatarService for tests.
type fakeService struct {
	uploadFn  func(ctx context.Context, userID string, file io.Reader, fileName string, size int64) (*api.UploadResult, error)
	getMetaFn func(ctx context.Context, id uuid.UUID) (*domain.Avatar, error)
	getBinFn  func(ctx context.Context, id uuid.UUID, size string) (io.ReadCloser, string, error)
	getByUsr  func(ctx context.Context, userID string) (*domain.Avatar, error)
	listFn    func(ctx context.Context, userID string, limit int) ([]*domain.Avatar, error)
	delFn     func(ctx context.Context, id uuid.UUID, requester string) error
}

func (f *fakeService) Upload(ctx context.Context, userID string, file io.Reader, fileName string, size int64) (*api.UploadResult, error) {
	return f.uploadFn(ctx, userID, file, fileName, size)
}
func (f *fakeService) GetMetadata(ctx context.Context, id uuid.UUID) (*domain.Avatar, error) {
	return f.getMetaFn(ctx, id)
}
func (f *fakeService) GetBinary(ctx context.Context, id uuid.UUID, size string) (io.ReadCloser, string, error) {
	return f.getBinFn(ctx, id, size)
}
func (f *fakeService) GetByUserID(ctx context.Context, userID string) (*domain.Avatar, error) {
	return f.getByUsr(ctx, userID)
}
func (f *fakeService) ListByUserID(ctx context.Context, userID string, limit int) ([]*domain.Avatar, error) {
	return f.listFn(ctx, userID, limit)
}
func (f *fakeService) Delete(ctx context.Context, id uuid.UUID, requester string) error {
	return f.delFn(ctx, id, requester)
}

// fakeHealth always reports ok.
type fakeHealth struct{ name string }

func (f *fakeHealth) Name() string { return f.name }
func (f *fakeHealth) Check(_ context.Context) error { return nil }

type failingHealth struct{ name string }

func (f *failingHealth) Name() string { return f.name }
func (f *failingHealth) Check(_ context.Context) error { return errors.New("down") }

// newServer wires a router with the given service and healthers.
func newServer(svc api.AvatarService, healthers ...api.HealthChecker) http.Handler {
	h := &api.Handlers{Service: svc, BaseURL: "http://example.com", Healthers: healthers}
	return api.Router(h, "", 10<<20)
}

func sampleJPEG() []byte {
	return []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01}
}

func newAvatarWithThumbnails(userID string) *domain.Avatar {
	id := uuid.New()
	return &domain.Avatar{
		ID:               id,
		UserID:           userID,
		FileName:         "pic.jpg",
		MimeType:         "image/jpeg",
		SizeBytes:        1234,
		S3Key:            "avatars/u/" + id.String() + "/pic.jpg",
		UploadStatus:     domain.UploadStatusUploaded,
		ProcessingStatus: domain.ProcessingStatusCompleted,
		ThumbnailS3Keys: map[string]string{
			"100x100": "thumbnails/u/x/100x100.jpg",
			"300x300": "thumbnails/u/x/300x300.jpg",
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

func TestUploadSuccess(t *testing.T) {
	t.Parallel()
	avatar := newAvatarWithThumbnails("user-1")
	svc := &fakeService{
		uploadFn: func(_ context.Context, uid string, _ io.Reader, fn string, _ int64) (*api.UploadResult, error) {
			require.Equal(t, "user-1", uid)
			require.Equal(t, "pic.jpg", fn)
			return &api.UploadResult{Avatar: avatar, URL: "http://example.com/api/v1/avatars/" + avatar.ID.String()}, nil
		},
	}
	srv := newServer(svc, &fakeHealth{"db"})

	body, contentType := buildMultipart(t, "file", "pic.jpg", sampleJPEG())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/avatars", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-User-ID", "user-1")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code)
	var resp api.UploadResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, avatar.ID.String(), resp.ID)
	require.Equal(t, "user-1", resp.UserID)
}

func TestUploadMissingUserID(t *testing.T) {
	t.Parallel()
	srv := newServer(&fakeService{
		uploadFn: func(context.Context, string, io.Reader, string, int64) (*api.UploadResult, error) {
			t.Fatal("service should not be called")
			return nil, nil
		},
	})
	body, contentType := buildMultipart(t, "file", "pic.jpg", sampleJPEG())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/avatars", body)
	req.Header.Set("Content-Type", contentType)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestUploadMissingFile(t *testing.T) {
	t.Parallel()
	srv := newServer(&fakeService{})
	body := strings.NewReader("--X--")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/avatars", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=X")
	req.Header.Set("X-User-ID", "user-1")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestUploadInvalidMagicBytes(t *testing.T) {
	t.Parallel()
	svc := &fakeService{
		uploadFn: func(context.Context, string, io.Reader, string, int64) (*api.UploadResult, error) {
			return nil, domain.ErrInvalidImage
		},
	}
	srv := newServer(svc)
	body, contentType := buildMultipart(t, "file", "pic.jpg", []byte("not an image"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/avatars", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-User-ID", "user-1")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusUnsupportedMediaType, rr.Code)
}

func TestUploadPayloadTooLarge(t *testing.T) {
	t.Parallel()
	// Cap at 100 bytes to provoke MaxBytesError easily.
	h := &api.Handlers{
		Service: &fakeService{
			uploadFn: func(context.Context, string, io.Reader, string, int64) (*api.UploadResult, error) {
				t.Fatal("service should not be called")
				return nil, nil
			},
		},
		BaseURL: "http://example.com",
	}
	r := api.Router(h, "", 100)
	body := bytes.NewReader(make([]byte, 200))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/avatars", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=X")
	req.Header.Set("X-User-ID", "user-1")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
}

func TestGetAvatarSuccess(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	svc := &fakeService{
		getBinFn: func(_ context.Context, gotID uuid.UUID, size string) (io.ReadCloser, string, error) {
			require.Equal(t, id, gotID)
			require.Equal(t, "original", size)
			return io.NopCloser(strings.NewReader("binary-data")), "image/jpeg", nil
		},
	}
	srv := newServer(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+id.String(), nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "image/jpeg", rr.Header().Get("Content-Type"))
	require.Contains(t, rr.Header().Get("Cache-Control"), "max-age=86400")
	require.NotEmpty(t, rr.Header().Get("ETag"))
	require.Equal(t, "binary-data", rr.Body.String())
}

func TestGetAvatarThumbnailSuccess(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	svc := &fakeService{
		getBinFn: func(_ context.Context, gotID uuid.UUID, size string) (io.ReadCloser, string, error) {
			require.Equal(t, id, gotID)
			require.Equal(t, "100x100", size)
			return io.NopCloser(strings.NewReader("thumb-bytes")), "image/jpeg", nil
		},
	}
	srv := newServer(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+id.String()+"?size=100x100", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "thumb-bytes", rr.Body.String())
}

func TestGetAvatarInvalidID(t *testing.T) {
	t.Parallel()
	srv := newServer(&fakeService{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/not-a-uuid", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestGetAvatarInvalidSize(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	srv := newServer(&fakeService{
		getBinFn: func(context.Context, uuid.UUID, string) (io.ReadCloser, string, error) {
			t.Fatal("should not reach service")
			return nil, "", nil
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+id.String()+"?size=abc", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusUnsupportedMediaType, rr.Code)
}

func TestGetAvatarNotFound(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	svc := &fakeService{
		getBinFn: func(context.Context, uuid.UUID, string) (io.ReadCloser, string, error) {
			return nil, "", domain.ErrAvatarNotFound
		},
	}
	srv := newServer(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+id.String(), nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestGetAvatarThumbnailNotReady(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	svc := &fakeService{
		getBinFn: func(context.Context, uuid.UUID, string) (io.ReadCloser, string, error) {
			return nil, "", domain.ErrThumbnailNotReady
		},
	}
	srv := newServer(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+id.String()+"?size=100x100", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestGetMetadataSuccess(t *testing.T) {
	t.Parallel()
	avatar := newAvatarWithThumbnails("user-1")
	svc := &fakeService{
		getMetaFn: func(_ context.Context, id uuid.UUID) (*domain.Avatar, error) {
			require.Equal(t, avatar.ID, id)
			return avatar, nil
		},
	}
	srv := newServer(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+avatar.ID.String()+"/metadata", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var resp api.MetadataResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, avatar.ID.String(), resp.ID)
	require.Len(t, resp.Thumbnails, 2)
}

func TestGetMetadataNotFound(t *testing.T) {
	t.Parallel()
	svc := &fakeService{
		getMetaFn: func(context.Context, uuid.UUID) (*domain.Avatar, error) {
			return nil, domain.ErrAvatarNotFound
		},
	}
	srv := newServer(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+uuid.New().String()+"/metadata", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestDeleteAvatarSuccess(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	svc := &fakeService{
		delFn: func(_ context.Context, gotID uuid.UUID, requester string) error {
			require.Equal(t, id, gotID)
			require.Equal(t, "user-1", requester)
			return nil
		},
	}
	srv := newServer(svc)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/avatars/"+id.String(), nil)
	req.Header.Set("X-User-ID", "user-1")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestDeleteAvatarMissingUserID(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	svc := &fakeService{
		delFn: func(context.Context, uuid.UUID, string) error {
			t.Fatal("service should not be called")
			return nil
		},
	}
	srv := newServer(svc)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/avatars/"+id.String(), nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestDeleteAvatarForbidden(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	svc := &fakeService{
		delFn: func(context.Context, uuid.UUID, string) error {
			return domain.ErrForbidden
		},
	}
	srv := newServer(svc)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/avatars/"+id.String(), nil)
	req.Header.Set("X-User-ID", "intruder")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)
}

func TestGetUserAvatarSuccess(t *testing.T) {
	t.Parallel()
	avatar := newAvatarWithThumbnails("user-1")
	svc := &fakeService{
		getByUsr: func(_ context.Context, uid string) (*domain.Avatar, error) {
			require.Equal(t, "user-1", uid)
			return avatar, nil
		},
		getBinFn: func(_ context.Context, _ uuid.UUID, size string) (io.ReadCloser, string, error) {
			require.Equal(t, "original", size)
			return io.NopCloser(strings.NewReader("orig-bytes")), "image/jpeg", nil
		},
	}
	srv := newServer(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/user-1/avatar", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "orig-bytes", rr.Body.String())
}

func TestGetUserAvatarThumbnail(t *testing.T) {
	t.Parallel()
	avatar := newAvatarWithThumbnails("user-1")
	svc := &fakeService{
		getByUsr: func(context.Context, string) (*domain.Avatar, error) {
			return avatar, nil
		},
		getBinFn: func(_ context.Context, _ uuid.UUID, size string) (io.ReadCloser, string, error) {
			require.Equal(t, "300x300", size)
			return io.NopCloser(strings.NewReader("thumb-300")), "image/jpeg", nil
		},
	}
	srv := newServer(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/user-1/avatar?size=300x300", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "thumb-300", rr.Body.String())
}

func TestGetUserAvatarNotFound(t *testing.T) {
	t.Parallel()
	svc := &fakeService{
		getByUsr: func(context.Context, string) (*domain.Avatar, error) {
			return nil, domain.ErrAvatarNotFound
		},
	}
	srv := newServer(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/nobody/avatar", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestListUserAvatars(t *testing.T) {
	t.Parallel()
	avatar := newAvatarWithThumbnails("user-1")
	svc := &fakeService{
		listFn: func(_ context.Context, uid string, _ int) ([]*domain.Avatar, error) {
			require.Equal(t, "user-1", uid)
			return []*domain.Avatar{avatar}, nil
		},
	}
	srv := newServer(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/user-1/avatars", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var resp api.UserAvatarsResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, "user-1", resp.UserID)
	require.Len(t, resp.Items, 1)
}

func TestHealthAllOK(t *testing.T) {
	t.Parallel()
	svc := &fakeService{
		getMetaFn: func(context.Context, uuid.UUID) (*domain.Avatar, error) { return nil, nil },
	}
	srv := newServer(svc, &fakeHealth{"db"}, &fakeHealth{"s3"})
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var resp api.HealthResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, "ok", resp.Status)
	require.Equal(t, "ok", resp.Components["db"].Status)
	require.Equal(t, "ok", resp.Components["s3"].Status)
}

func TestHealthDegraded(t *testing.T) {
	t.Parallel()
	svc := &fakeService{}
	srv := newServer(svc, &fakeHealth{"db"}, &failingHealth{"s3"})
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusServiceUnavailable, rr.Code)
	var resp api.HealthResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, "degraded", resp.Status)
	require.Equal(t, "down", resp.Components["s3"].Status)
	require.Equal(t, "down", resp.Components["s3"].Error)
}

func TestETagStableForSameIDAndSize(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	etag1 := ""
	etag2 := ""
	etag3 := ""

	svc := &fakeService{
		getBinFn: func(_ context.Context, _ uuid.UUID, size string) (io.ReadCloser, string, error) {
			return io.NopCloser(strings.NewReader("x")), "image/jpeg", nil
		},
	}
	srv := newServer(svc)

	for _, size := range []string{"100x100", "100x100", "300x300"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+id.String()+"?size="+size, nil)
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		switch size {
		case "100x100":
			if etag1 == "" {
				etag1 = rr.Header().Get("ETag")
			} else {
				etag2 = rr.Header().Get("ETag")
			}
		case "300x300":
			etag3 = rr.Header().Get("ETag")
		}
	}
	require.NotEmpty(t, etag1)
	require.Equal(t, etag1, etag2)
	require.NotEqual(t, etag1, etag3)
}

// buildMultipart constructs an in-memory multipart/form-data with one "file" field.
func buildMultipart(t *testing.T, field, filename string, payload []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile(field, filename)
	require.NoError(t, err)
	_, err = fw.Write(payload)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return &buf, w.FormDataContentType()
}
