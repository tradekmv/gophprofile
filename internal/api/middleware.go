package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"

	"github.com/tradekmv/gophprofile/pkg/logger"
)

// RequestLogger — структурированный логгер каждого запроса.
// Включает trace_id/span_id из активного span (если есть) для корреляции с трассировкой.
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		logger.LContext(r.Context()).Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", ww.Status()).
			Int("bytes", ww.BytesWritten()).
			Dur("duration", time.Since(start)).
			Msg("http")
	})
}

// MetricsMiddleware записывает RED-метрики через переданный *BusinessMetrics.
// Оборачивает ResponseWriter для получения финального статуса.
//
// route берётся из chi.RouteContext (шаблон маршрута, например "/api/v1/avatars/{id}"),
// а не из r.URL.Path (фактический URL). Это критично для bounded cardinality
// меток — иначе каждый UUID породит уникальную серию в Prometheus.
func MetricsMiddleware(m *BusinessMetrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			route := routePattern(r)
			status := strconv.Itoa(ww.Status())
			m.HTTPRequestsTotal.WithLabelValues(r.Method, route, status).Inc()
			m.HTTPRequestDuration.WithLabelValues(r.Method, route, status).Observe(time.Since(start).Seconds())
		})
	}
}

// routePattern возвращает chi-шаблон маршрута ("/api/v1/avatars/{id}") или
// "unknown" если шаблон не определён (например, для 404). Это ограничивает
// cardinality меток в Prometheus, защищая от взрывного роста при сканировании.
func routePattern(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return "unknown"
	}
	p := rctx.RoutePattern()
	if p == "" {
		return "unknown"
	}
	return p
}

// TracingMiddleware оборачивает хендлер в otelhttp.NewHandler для создания
// HTTP-серверных спанов с автоматическим контекстом и атрибутами.
func TracingMiddleware(serviceName string) func(http.Handler) http.Handler {
	tp := otel.GetTracerProvider()
	return func(next http.Handler) http.Handler {
		return otelhttp.NewHandler(next, serviceName,
			otelhttp.WithTracerProvider(tp),
			otelhttp.WithMessageEvents(otelhttp.ReadEvents, otelhttp.WriteEvents),
		)
	}
}

// MaxBodyBytes ограничивает размер тела запроса.
func MaxBodyBytes(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}
