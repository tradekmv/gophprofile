package api

import (
	"encoding/json"
	"net/http"

	"github.com/tradekmv/gophprofile/pkg/httperr"
	"github.com/tradekmv/gophprofile/pkg/logger"
)

// writeError сериализует ошибку (или оборачивает общую) в JSON.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	e := httperr.As(err)
	if e.Status >= 500 {
		logger.L().Error().Err(err).Str("path", r.URL.Path).Msg("internal server error")
	} else {
		logger.L().Debug().Err(err).Str("path", r.URL.Path).Int("status", e.Status).Msg("client error")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(e)
}

// errAvatarNotFound возвращает 404 для отсутствующей аватарки.
func errAvatarNotFound() error {
	return &httperr.Error{Status: http.StatusNotFound, Code: "avatar_not_found"}
}

// errThumbnailNotReady возвращает 404 если миниатюра ещё не готова.
func errThumbnailNotReady() error {
	return &httperr.Error{Status: http.StatusNotFound, Code: "thumbnail_not_ready"}
}

// errForbidden возвращает 403.
func errForbidden() error {
	return &httperr.Error{Status: http.StatusForbidden, Code: "forbidden"}
}

// errMissingUserID возвращает 400 если нет X-User-ID.
func errMissingUserID() error {
	return httperr.WithDetails(httperr.ErrBadRequest, "X-User-ID header is required")
}
