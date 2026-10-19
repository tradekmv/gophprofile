package worker

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewWorkerMetrics(t *testing.T) {
	m := NewWorkerMetrics()
	require.NotNil(t, m)
	require.NotNil(t, m.ProcessedTotal)
	require.NotNil(t, m.FailedTotal)
	require.NotNil(t, m.Duration)
	require.NotNil(t, m.Handler())

	m.ProcessedTotal.WithLabelValues("success").Inc()
	m.FailedTotal.WithLabelValues("decode_error").Inc()
	m.Duration.WithLabelValues("success").Observe(0.5)
}

func TestWorkerMetricsHandler(t *testing.T) {
	m := NewWorkerMetrics()
	m.ProcessedTotal.WithLabelValues("success").Inc()

	req := httptest.NewRequest("GET", "/metrics", nil)
	rw := httptest.NewRecorder()
	m.Handler().ServeHTTP(rw, req)
	require.Equal(t, http.StatusOK, rw.Code, "status=%d", rw.Code)
	body := rw.Body.String()
	require.Contains(t, body, "avatars_worker_processed_total")
	require.Contains(t, strings.ToLower(body), "worker")
}
