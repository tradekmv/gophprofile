package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

// Router собирает HTTP-роутер с middleware и маршрутами.
func Router(h *Handlers, webDir string, maxBody int64) http.Handler {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(requestLogger)
	r.Use(chimw.Recoverer)
	r.Use(maxBodyBytes(maxBody))

	// API
	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/avatars", h.UploadAvatar)
		r.Get("/avatars/{id}", h.GetAvatar)
		r.Get("/avatars/{id}/metadata", h.GetMetadata)
		r.Delete("/avatars/{id}", h.DeleteAvatar)
		r.Get("/users/{user_id}/avatar", h.GetUserAvatar)
		r.Get("/users/{user_id}/avatars", h.ListUserAvatars)
	})

	// Web UI — статический SPA из директории webDir.
	if webDir != "" {
		r.Handle("/web/*", http.StripPrefix("/web/", http.FileServer(http.Dir(webDir))))
	}

	// Health.
	r.Get("/health", h.Health)

	return r
}
