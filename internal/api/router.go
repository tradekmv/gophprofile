package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

// RouterOptions — параметры роутера.
type RouterOptions struct {
	WebDir          string
	MaxBody         int64
	ServiceName     string           // для otelhttp.NewHandler (если пусто — трассинг не подключается)
	BusinessMetrics *BusinessMetrics // если nil — метрики не собираются
}

// Router собирает HTTP-роутер с middleware и маршрутами.
//
// Подключает (по возможности):
//   - request_id (chimw)
//   - logger (структурированный JSON-лог каждого запроса)
//   - recoverer (chimw, от паник)
//   - metrics (RED-метрики, если указаны opts.BusinessMetrics)
//   - tracing (otelhttp.NewHandler, если указано opts.ServiceName)
//   - max_body_bytes
func Router(h *Handlers, opts RouterOptions) http.Handler {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.Recoverer)
	// Tracing должен идти ДО RequestLogger, чтобы span был активен
	// при логировании запроса — иначе trace_id не попадёт в логи.
	if opts.ServiceName != "" {
		r.Use(TracingMiddleware(opts.ServiceName))
	}
	if opts.BusinessMetrics != nil {
		r.Use(MetricsMiddleware(opts.BusinessMetrics))
	}
	r.Use(RequestLogger)
	r.Use(MaxBodyBytes(opts.MaxBody))

	// API
	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/avatars", h.UploadAvatar)
		r.Get("/avatars/{id}", h.GetAvatar)
		r.Get("/avatars/{id}/metadata", h.GetMetadata)
		r.Delete("/avatars/{id}", h.DeleteAvatar)
		r.Get("/users/{user_id}/avatar", h.GetUserAvatar)
		r.Delete("/users/{user_id}/avatar", h.DeleteUserAvatar)
		r.Get("/users/{user_id}/avatars", h.ListUserAvatars)
	})

	// Web UI — статический SPA из директории webDir.
	if opts.WebDir != "" {
		r.Handle("/web/*", http.StripPrefix("/web/", http.FileServer(http.Dir(opts.WebDir))))
	}

	// Health.
	r.Get("/health", h.Health)

	return r
}
