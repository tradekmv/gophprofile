// Package worker — определения метрик обработчика событий.
package worker

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// WorkerMetrics — набор метрик для воркера обработки событий аватарок.
type WorkerMetrics struct {
	registry *prometheus.Registry

	ProcessedTotal *prometheus.CounterVec
	FailedTotal    *prometheus.CounterVec
	Duration       *prometheus.HistogramVec
}

// NewWorkerMetrics создаёт и регистрирует метрики.
func NewWorkerMetrics() *WorkerMetrics {
	reg := prometheus.NewRegistry()
	m := &WorkerMetrics{registry: reg}

	m.ProcessedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "avatars_worker_processed_total",
			Help: "Total avatar processing events handled by the worker by status.",
		},
		[]string{"status"},
	)
	m.FailedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "avatars_worker_failed_total",
			Help: "Total failed avatar processing events by reason.",
		},
		[]string{"reason"},
	)
	m.Duration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "avatars_worker_processing_duration_seconds",
			Help:    "Worker avatar processing duration by status.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"status"},
	)
	reg.MustRegister(m.ProcessedTotal, m.FailedTotal, m.Duration)
	return m
}

// Handler возвращает HTTP-хендлер с /metrics для прямого Prometheus pull.
func (m *WorkerMetrics) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}
