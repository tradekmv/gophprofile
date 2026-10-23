package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRateLimiterDisabled(t *testing.T) {
	// rps=0 означает pass-through.
	rl := NewRateLimiter(0, 0)
	defer rl.Stop()
	for i := 0; i < 1000; i++ {
		require.True(t, rl.Allow("client"))
	}
}

func TestRateLimiterBurst(t *testing.T) {
	rl := NewRateLimiter(1, 3) // 1 rps, burst 3
	defer rl.Stop()
	// Первые 3 запроса должны пройти (burst).
	for i := 0; i < 3; i++ {
		require.True(t, rl.Allow("client"), "request %d should be allowed", i)
	}
	// 4-й должен быть отклонён.
	require.False(t, rl.Allow("client"), "4th request should be denied")
}

func TestRateLimiterDifferentKeys(t *testing.T) {
	rl := NewRateLimiter(1, 2)
	defer rl.Stop()
	// Разные IP — разные bucket'ы.
	require.True(t, rl.Allow("client-a"))
	require.True(t, rl.Allow("client-a"))
	require.False(t, rl.Allow("client-a"))
	// client-b имеет свой bucket.
	require.True(t, rl.Allow("client-b"))
}

func TestRateLimitMiddleware(t *testing.T) {
	rl := NewRateLimiter(1, 1)
	defer rl.Stop()
	calls := 0
	handler := RateLimitMiddleware(rl)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	// Первый запрос проходит.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 1, calls)
	// Второй — отклонён.
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req)
	require.Equal(t, http.StatusTooManyRequests, rr2.Code)
	require.Equal(t, 1, calls) // handler не вызван
	require.Equal(t, "1", rr2.Header().Get("Retry-After"))
}

// TestClientIPHeaderPriority проверяет, что за Ingress-контроллером
// rate limiter различает клиентов по X-Real-IP, а не по IP пода ingress'а.
func TestClientIPHeaderPriority(t *testing.T) {
	rl := NewRateLimiter(1, 1)
	defer rl.Stop()
	calls := 0
	handler := RateLimitMiddleware(rl)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	// RemoteAddr одинаковый (один ingress-под), но X-Real-IP разный.
	mk := func(realIP string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.5:12345" // IP ingress-пода
		if realIP != "" {
			req.Header.Set("X-Real-IP", realIP)
		}
		return req
	}
	// Разные клиенты через один ingress — оба проходят (разные бакеты).
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, mk("203.0.113.10"))
	require.Equal(t, http.StatusOK, rr.Code)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, mk("203.0.113.11"))
	require.Equal(t, http.StatusOK, rr2.Code)
	require.Equal(t, 2, calls)

	// Тот же клиент повторно — отклонён.
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, mk("203.0.113.10"))
	require.Equal(t, http.StatusTooManyRequests, rr3.Code)
}

func TestClientIPXFFFallback(t *testing.T) {
	// XFF: берётся левейшая запись (реальный клиент).
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.5:12345"
	r.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.1, 10.0.0.2")
	require.Equal(t, "198.51.100.7", clientIP(r))

	// Без заголовков — фолбэк на RemoteAddr без порта.
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.RemoteAddr = "192.0.2.55:4444"
	require.Equal(t, "192.0.2.55", clientIP(r2))
}
