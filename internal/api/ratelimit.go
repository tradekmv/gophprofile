package api

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// RateLimiter — простой token-bucket rate limiter для middleware.
// Production-grade варианты (golang.org/x/time/rate) используются в реальных
// системах; здесь — компактная реализация без внешних зависимостей.
type RateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	rps      float64
	burst    float64
	stopCh   chan struct{}
	stopOnce sync.Once
}

type bucket struct {
	tokens   float64
	lastFill time.Time
}

// NewRateLimiter создаёт лимитер: rps — запросов в секунду, burst — пиковый размер.
// passThrough=true (или rps<=0) — лимитер пропускает все запросы.
func NewRateLimiter(rps float64, burst int) *RateLimiter {
	rl := &RateLimiter{
		buckets: make(map[string]*bucket),
		rps:     rps,
		burst:   float64(burst),
		stopCh:  make(chan struct{}),
	}
	if rps > 0 && burst > 0 {
		go rl.gc()
	}
	return rl
}

// Stop освобождает ресурсы.
func (rl *RateLimiter) Stop() {
	rl.stopOnce.Do(func() { close(rl.stopCh) })
}

func (rl *RateLimiter) gc() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-rl.stopCh:
			return
		case <-ticker.C:
			rl.mu.Lock()
			now := time.Now()
			for k, b := range rl.buckets {
				if now.Sub(b.lastFill) > 10*time.Minute {
					delete(rl.buckets, k)
				}
			}
			rl.mu.Unlock()
		}
	}
}

// Allow проверяет, разрешён ли запрос от key (обычно IP-адрес).
func (rl *RateLimiter) Allow(key string) bool {
	if rl.rps <= 0 || rl.burst <= 0 {
		return true
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok {
		b = &bucket{tokens: rl.burst, lastFill: now}
		rl.buckets[key] = b
	}
	// Пополняем токены.
	elapsed := now.Sub(b.lastFill).Seconds()
	b.tokens += elapsed * rl.rps
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}
	b.lastFill = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// RateLimitMiddleware возвращает middleware, ограничивающее частоту запросов по IP.
// Используется для защиты от перегрузки и базового rate limiting.
func RateLimitMiddleware(rl *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := clientIP(r)
			if !rl.Allow(key) {
				w.Header().Set("Retry-After", "1")
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP извлекает IP клиента для rate limiting.
//
// За Ingress-контроллером (nginx-ingress) r.RemoteAddr содержит IP пода ingress'а,
// а не реального клиента. Поэтому приоритет у заголовка X-Real-IP, который
// ingress-контроллер проставляет из X-Forwarded-For.
//
// Доверие к заголовку ограничено самим ingress: напрямую из интернета (в обход
// ingress) X-Real-IP можно подделать. Поэтому при его отсутствии фолбэк на
// RemoteAddr, и Service-сеть закрыта NetworkPolicy — прямой доступ к поду снаружи
// невозможен.
func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		// XFF = "client, proxy1, proxy2" — берём перв��ю (левейшую) запись.
		if idx := strings.IndexByte(v, ','); idx > 0 {
			return strings.TrimSpace(v[:idx])
		}
		return strings.TrimSpace(v)
	}
	addr := r.RemoteAddr
	// Обрезка порта (IPv4 "1.2.3.4:5678" или IPv6 "[::1]:5678").
	if idx := strings.LastIndexByte(addr, ':'); idx > 0 {
		return addr[:idx]
	}
	return addr
}
