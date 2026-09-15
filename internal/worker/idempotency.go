// Package worker — Kafka-консьюмер, обрабатывающий события аватарок.
package worker

import (
	"sync"
	"time"
)

// ProcessedStore хранит ID уже обработанных событий, чтобы не обрабатывать
// дубли при at-least-once доставке из Kafka.
type ProcessedStore interface {
	Seen(id string) (bool, error)
	Mark(id string) error
}

// InMemoryProcessedStore — TTL-bounded in-memory хранилище для одного
// инстанса воркера. Удаляет записи лениво при обращении.
type InMemoryProcessedStore struct {
	mu   sync.RWMutex
	data map[string]time.Time
	ttl  time.Duration
}

// NewInMemoryProcessedStore создаёт хранилище с указанным TTL.
// ttl <= 0 означает 24 часа.
func NewInMemoryProcessedStore(ttl time.Duration) *InMemoryProcessedStore {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &InMemoryProcessedStore{
		data: make(map[string]time.Time),
		ttl:  ttl,
	}
}

// Seen возвращает true, если id уже помечен в TTL-окне.
func (s *InMemoryProcessedStore) Seen(id string) (bool, error) {
	s.mu.RLock()
	last, ok := s.data[id]
	s.mu.RUnlock()
	if !ok {
		return false, nil
	}
	if time.Since(last) > s.ttl {
		// Ленивое удаление протухшей записи.
		s.mu.Lock()
		delete(s.data, id)
		s.mu.Unlock()
		return false, nil
	}
	return true, nil
}

// Mark записывает id как обработанный.
func (s *InMemoryProcessedStore) Mark(id string) error {
	s.mu.Lock()
	s.data[id] = time.Now()
	s.mu.Unlock()
	return nil
}
