package api

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// BusinessMetrics — набор стандартных метрик HTTP-слоя и бизнес-операций.
// Все метрики с префиксом avatars_.
type BusinessMetrics struct {
	registry *prometheus.Registry

	// RED-метрики HTTP-слоя.
	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPRequestDuration *prometheus.HistogramVec

	// Бизнес-метрики домена.
	UploadsTotal      *prometheus.CounterVec
	UploadDuration    *prometheus.HistogramVec
	UploadErrorsTotal *prometheus.CounterVec

	// Инфраструктурные метрики (выставляются из pgx/worker).
	DBConnectionsOpen prometheus.Gauge

	// Storage usage (bytes) — gauge из ТЗ Sprint 12. Без label (один общий счётчик
	// на сервис), чтобы избежать unbounded cardinality.
	StorageBytes prometheus.Gauge
}

// NewBusinessMetrics создаёт и регистрирует все метрики.
func NewBusinessMetrics() *BusinessMetrics {
	reg := prometheus.NewRegistry()
	m := &BusinessMetrics{registry: reg}

	m.HTTPRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "avatars_http_requests_total",
			Help: "Total HTTP requests by method, route and status.",
		},
		[]string{"method", "route", "status"},
	)
	m.HTTPRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "avatars_http_request_duration_seconds",
			Help:    "HTTP request duration by method, route and status.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "route", "status"},
	)
	m.UploadsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "avatars_uploads_total",
			Help: "Total number of avatar upload attempts by status.",
		},
		[]string{"status"},
	)
	m.UploadDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "avatars_upload_duration_seconds",
			Help:    "Avatar upload processing duration by status.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"status"},
	)
	m.UploadErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "avatars_upload_errors_total",
			Help: "Total avatar upload errors by reason.",
		},
		[]string{"reason"},
	)
	m.DBConnectionsOpen = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "avatars_db_connections_open",
			Help: "Number of open database connections.",
		},
	)
	m.StorageBytes = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "avatars_storage_bytes",
			Help: "Total storage used by avatars (bytes, service-wide).",
		},
	)
	reg.MustRegister(
		m.HTTPRequestsTotal,
		m.HTTPRequestDuration,
		m.UploadsTotal,
		m.UploadDuration,
		m.UploadErrorsTotal,
		m.DBConnectionsOpen,
		m.StorageBytes,
	)
	return m
}

// Registry возвращает внутренний реестр Prometheus для дополнительной
// регистрации кастомных метрик.
func (m *BusinessMetrics) Registry() *prometheus.Registry { return m.registry }

// Handler возвращает HTTP-хендлер с /metrics и /healthz для прямого Prometheus pull.
func (m *BusinessMetrics) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}
