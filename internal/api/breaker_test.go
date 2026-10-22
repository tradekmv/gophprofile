package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCircuitBreakerClosed(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Second)
	require.Equal(t, "closed", cb.State())
	require.NoError(t, cb.Allow())
}

func TestCircuitBreakerOpensAfterFailures(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Second)
	for i := 0; i < 3; i++ {
		cb.Failure()
	}
	require.Equal(t, "open", cb.State())
	require.ErrorIs(t, cb.Allow(), ErrCircuitOpen)
}

func TestCircuitBreakerHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker(2, 100*time.Millisecond)
	cb.Failure()
	cb.Failure()
	require.Equal(t, "open", cb.State())
	require.ErrorIs(t, cb.Allow(), ErrCircuitOpen)

	// Ждём, пока openDuration истечёт.
	time.Sleep(300 * time.Millisecond)

	// Первый Allow() после таймаута выполняет переход open → half-open.
	require.NoError(t, cb.Allow())
	require.Equal(t, "half-open", cb.State())

	// Второй Allow() в half-open отклоняется (проба уже выдана).
	require.ErrorIs(t, cb.Allow(), ErrCircuitOpen)

	// Успех в half-open → closed.
	cb.Success()
	require.Equal(t, "closed", cb.State())
}

func TestCircuitBreakerReopensOnFailureInHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)
	cb.Failure()
	cb.Failure()
	time.Sleep(60 * time.Millisecond)
	require.NoError(t, cb.Allow()) // half-open
	cb.Failure()                   // ошибка в half-open → open
	require.Equal(t, "open", cb.State())
}

func TestCircuitBreakerResetsOnSuccess(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Second)
	cb.Failure()
	cb.Failure()
	// Успех сбрасывает счётчик.
	cb.Success()
	cb.Failure() // не открывает
	require.Equal(t, "closed", cb.State())
}
