package api

import (
	"net/http"
	"strconv"
	"time"

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
func MetricsMiddleware(m *BusinessMetrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			route := r.URL.Path
			status := strconv.Itoa(ww.Status())
			m.HTTPRequestsTotal.WithLabelValues(r.Method, route, status).Inc()
			m.HTTPRequestDuration.WithLabelValues(r.Method, route, status).Observe(time.Since(start).Seconds())
		})
	}
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
