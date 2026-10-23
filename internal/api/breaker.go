package api

import (
	"errors"
	"sync"
	"time"
)

// CircuitBreaker — простой circuit breaker для защиты от каскадных отказов.
// Состояния: closed (норма), open (отказ → все запросы отклоняются),
// half-open (пробный запрос).
type CircuitBreaker struct {
	mu            sync.Mutex
	state         state
	failures      int
	successes     int
	probeIssued   bool
	openedAt      time.Time
	openDuration  time.Duration
	failureThresh int
}

type state int

const (
	stateClosed state = iota
	stateOpen
	stateHalfOpen
)

// ErrCircuitOpen возвращается когда breaker открыт.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// NewCircuitBreaker — failureThresh подряд ошибок → open на openDuration.
func NewCircuitBreaker(failureThresh int, openDuration time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:         stateClosed,
		openDuration:  openDuration,
		failureThresh: failureThresh,
	}
}

// Allow проверяет, можно ли выполнить запрос. Возвращает nil, если можно,
// иначе ErrCircuitOpen. В half-open пропускает только 1 запрос для пробы.
func (cb *CircuitBreaker) Allow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	// Автоматический переход open → half-open по таймауту.
	if cb.state == stateOpen && cb.openDuration > 0 {
		elapsed := time.Since(cb.openedAt)
		if elapsed >= cb.openDuration {
			cb.state = stateHalfOpen
			cb.successes = 0
			cb.failures = 0
			cb.probeIssued = false
		}
	}
	switch cb.state {
	case stateClosed:
		return nil
	case stateOpen:
		return ErrCircuitOpen
	case stateHalfOpen:
		if cb.probeIssued {
			return ErrCircuitOpen
		}
		cb.probeIssued = true
		return nil
	}
	return nil
}

// Success вызывается после успешного запроса.
func (cb *CircuitBreaker) Success() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case stateClosed:
		cb.failures = 0
	case stateHalfOpen:
		cb.successes++
		if cb.successes >= 1 {
			cb.state = stateClosed
			cb.failures = 0
			cb.probeIssued = false
		}
	}
}

// Failure вызывается после ошибки.
func (cb *CircuitBreaker) Failure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case stateClosed:
		cb.failures++
		if cb.failures >= cb.failureThresh {
			cb.state = stateOpen
			cb.openedAt = time.Now()
		}
	case stateHalfOpen:
		cb.failures++
		cb.state = stateOpen
		cb.openedAt = time.Now()
	}
}

// State возвращает текущее состояние (для метрик/отладки).
func (cb *CircuitBreaker) State() string {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case stateClosed:
		return "closed"
	case stateOpen:
		return "open"
	case stateHalfOpen:
		return "half-open"
	}
	return "unknown"
}
