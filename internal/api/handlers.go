package api

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/tradekmv/gophprofile/internal/domain"
	"github.com/tradekmv/gophprofile/pkg/httperr"
	"github.com/tradekmv/gophprofile/pkg/imgutil"
)

const (
	headerUserID  = "X-User-ID"
	maxFormMemory = 2 << 20 // 2 МиБ — порог для spill-to-disk
)

// Handlers хранит зависимости HTTP-хендлеров.
type Handlers struct {
	Service   AvatarService
	Healthers []HealthChecker
	BaseURL   string
}

// userIDFromHeader возвращает обязательный заголовок X-User-ID.
func userIDFromHeader(r *http.Request) (string, error) {
	uid := r.Header.Get(headerUserID)
	if uid == "" {
		return "", errMissingUserID()
	}
	if len(uid) > 255 {
		return "", httperr.WithDetails(httperr.ErrBadRequest, "X-User-ID too long")
	}
	return uid, nil
}

// avatarPublicURL возвращает публичный URL оригинала аватарки.
func (h *Handlers) avatarPublicURL(id uuid.UUID) string {
	return h.BaseURL + "/api/v1/avatars/" + id.String()
}

// thumbnailPublicURL возвращает публичный URL миниатюры.
func (h *Handlers) thumbnailPublicURL(id uuid.UUID, size string) string {
	return h.BaseURL + "/api/v1/avatars/" + id.String() + "?size=" + size
}

// UploadAvatar — обработчик POST /api/v1/avatars.
func (h *Handlers) UploadAvatar(w http.ResponseWriter, r *http.Request) {
	userID, err := userIDFromHeader(r)
	if err != nil {
		writeError(w, r, err)
		return
	}

	if err := r.ParseMultipartForm(maxFormMemory); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeError(w, r, httperr.ErrPayloadTooLarge)
			return
		}
		writeError(w, r, httperr.WithDetails(httperr.ErrBadRequest, "invalid multipart form"))
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, r, httperr.WithDetails(httperr.ErrBadRequest, "form field 'file' is required"))
		return
	}
	defer func() { _ = file.Close() }()

	result, err := h.Service.Upload(r.Context(), userID, file, header.Filename, header.Size)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidImage):
			writeError(w, r, httperr.ErrUnsupportedMedia)
		case errors.Is(err, domain.ErrAvatarNotFound):
			writeError(w, r, errAvatarNotFound())
		default:
			writeError(w, r, err)
		}
		return
	}

	writeJSON(w, http.StatusCreated, UploadResponse{
		ID:        result.Avatar.ID.String(),
		UserID:    result.Avatar.UserID,
		URL:       result.URL,
		Status:    result.Avatar.ProcessingStatus,
		CreatedAt: result.Avatar.CreatedAt,
	})
}

// GetAvatar — обработчик GET /api/v1/avatars/{id}?size=100x100|300x300|original.
func (h *Handlers) GetAvatar(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, httperr.WithDetails(httperr.ErrBadRequest, "invalid avatar id"))
		return
	}
	size := r.URL.Query().Get("size")
	if size == "" {
		size = domain.SizeOriginal
	}
	if _, _, err := imgutil.ParseSize(size); err != nil {
		writeError(w, r, httperr.ErrUnsupportedMedia)
		return
	}

	body, contentType, err := h.Service.GetBinary(r.Context(), id, size)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrAvatarNotFound):
			writeError(w, r, errAvatarNotFound())
		case errors.Is(err, domain.ErrThumbnailNotReady):
			writeError(w, r, errThumbnailNotReady())
		default:
			writeError(w, r, err)
		}
		return
	}
	defer func() { _ = body.Close() }()

	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("ETag", makeETag(id, size))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, body)
}

// GetMetadata — обработчик GET /api/v1/avatars/{id}/metadata.
func (h *Handlers) GetMetadata(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, httperr.WithDetails(httperr.ErrBadRequest, "invalid avatar id"))
		return
	}
	a, err := h.Service.GetMetadata(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrAvatarNotFound) {
			writeError(w, r, errAvatarNotFound())
			return
		}
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toMetadataResponse(a, h))
}

// DeleteAvatar — обработчик DELETE /api/v1/avatars/{id}.
func (h *Handlers) DeleteAvatar(w http.ResponseWriter, r *http.Request) {
	userID, err := userIDFromHeader(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, httperr.WithDetails(httperr.ErrBadRequest, "invalid avatar id"))
		return
	}
	if err := h.Service.Delete(r.Context(), id, userID); err != nil {
		switch {
		case errors.Is(err, domain.ErrAvatarNotFound):
			writeError(w, r, errAvatarNotFound())
		case errors.Is(err, domain.ErrForbidden):
			writeError(w, r, errForbidden())
		default:
			writeError(w, r, err)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetUserAvatar — обработчик GET /api/v1/users/{user_id}/avatar.
func (h *Handlers) GetUserAvatar(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		writeError(w, r, httperr.WithDetails(httperr.ErrBadRequest, "user_id is required"))
		return
	}
	size := r.URL.Query().Get("size")

	avatar, err := h.Service.GetByUserID(r.Context(), userID)
	if err != nil {
		if errors.Is(err, domain.ErrAvatarNotFound) {
			writeError(w, r, errAvatarNotFound())
			return
		}
		writeError(w, r, err)
		return
	}

	if size == "" || size == domain.SizeOriginal {
		body, ct, err := h.Service.GetBinary(r.Context(), avatar.ID, domain.SizeOriginal)
		if err != nil {
			writeError(w, r, err)
			return
		}
		defer func() { _ = body.Close() }()
		w.Header().Set("Content-Type", ctOrDefault(ct))
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("ETag", makeETag(avatar.ID, domain.SizeOriginal))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, body)
		return
	}

	if _, _, err := imgutil.ParseSize(size); err != nil {
		writeError(w, r, httperr.ErrUnsupportedMedia)
		return
	}
	body, ct, err := h.Service.GetBinary(r.Context(), avatar.ID, size)
	if err != nil {
		if errors.Is(err, domain.ErrThumbnailNotReady) {
			writeError(w, r, errThumbnailNotReady())
			return
		}
		writeError(w, r, err)
		return
	}
	defer func() { _ = body.Close() }()
	w.Header().Set("Content-Type", ctOrDefault(ct))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("ETag", makeETag(avatar.ID, size))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, body)
}

// DeleteUserAvatar — обработчик DELETE /api/v1/users/{user_id}/avatar.
// Удаляет все аватарки пользователя (soft delete + события на очистку S3).
// 204 при успехе независимо от того, были ли аватарки.
func (h *Handlers) DeleteUserAvatar(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		writeError(w, r, httperr.WithDetails(httperr.ErrBadRequest, "user_id is required"))
		return
	}
	if _, err := h.Service.DeleteAllByUserID(r.Context(), userID); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListUserAvatars — обработчик GET /api/v1/users/{user_id}/avatars.
func (h *Handlers) ListUserAvatars(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		writeError(w, r, httperr.WithDetails(httperr.ErrBadRequest, "user_id is required"))
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	avatars, err := h.Service.ListByUserID(r.Context(), userID, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}

	items := make([]AvatarListItem, 0, len(avatars))
	for _, a := range avatars {
		items = append(items, toAvatarListItem(a, h))
	}
	writeJSON(w, http.StatusOK, UserAvatarsResponse{UserID: userID, Items: items})
}

// Health — обработчик GET /health.
// Внутренние детали сбоя (строка подключения, сетевые ошибки и т.п.) НЕ
// отдаются клиенту: в JSON попадает только статус "down". Реальную ошибку
// каждый HealthChecker должен логировать сам внутри Check.
func (h *Handlers) Health(w http.ResponseWriter, r *http.Request) {
	resp := HealthResponse{Status: "ok", Components: make(map[string]ComponentHealth, len(h.Healthers))}
	overallOK := true
	for _, hc := range h.Healthers {
		c := ComponentHealth{Status: "ok"}
		if err := hc.Check(r.Context()); err != nil {
			c.Status = "down"
			// Намеренно не раскрываем err.Error() клиенту.
			overallOK = false
		}
		resp.Components[hc.Name()] = c
	}
	if !overallOK {
		resp.Status = "degraded"
	}
	w.Header().Set("Content-Type", "application/json")
	status := http.StatusOK
	if !overallOK {
		status = http.StatusServiceUnavailable
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

// writeJSON — хелпер для JSON-ответов.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// toMetadataResponse собирает DTO метаданных для аватарки.
func toMetadataResponse(a *domain.Avatar, h *Handlers) MetadataResponse {
	sizes := []string{domain.Size100x100, domain.Size300x300}
	thumbs := make([]ThumbnailDTO, 0, len(sizes))
	for _, s := range sizes {
		if a.ThumbnailKey(s) != "" {
			thumbs = append(thumbs, ThumbnailDTO{Size: s, URL: h.thumbnailPublicURL(a.ID, s)})
		}
	}
	return MetadataResponse{
		ID:               a.ID.String(),
		UserID:           a.UserID,
		FileName:         a.FileName,
		MimeType:         a.MimeType,
		SizeBytes:        a.SizeBytes,
		UploadStatus:     a.UploadStatus,
		ProcessingStatus: a.ProcessingStatus,
		Thumbnails:       thumbs,
		CreatedAt:        a.CreatedAt,
		UpdatedAt:        a.UpdatedAt,
	}
}

// toAvatarListItem собирает DTO элемента списка.
func toAvatarListItem(a *domain.Avatar, h *Handlers) AvatarListItem {
	sizes := []string{domain.Size100x100, domain.Size300x300}
	thumbs := make([]ThumbnailDTO, 0, len(sizes))
	for _, s := range sizes {
		if a.ThumbnailKey(s) != "" {
			thumbs = append(thumbs, ThumbnailDTO{Size: s, URL: h.thumbnailPublicURL(a.ID, s)})
		}
	}
	return AvatarListItem{
		ID:         a.ID.String(),
		UserID:     a.UserID,
		FileName:   a.FileName,
		MimeType:   a.MimeType,
		SizeBytes:  a.SizeBytes,
		URL:        h.avatarPublicURL(a.ID),
		CreatedAt:  a.CreatedAt,
		Thumbnails: thumbs,
	}
}

// makeETag возвращает короткий ETag на основе id и размера.
func makeETag(id uuid.UUID, size string) string {
	sum := md5.Sum([]byte(id.String() + "|" + strings.ToLower(size)))
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

// ctOrDefault возвращает content-type или дефолт.
func ctOrDefault(ct string) string {
	if ct == "" {
		return "application/octet-stream"
	}
	return ct
}
