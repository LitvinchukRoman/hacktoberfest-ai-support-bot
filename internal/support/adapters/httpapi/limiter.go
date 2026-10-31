package httpapi

import (
	"sync"
	"time"
)

// Limiter is a simple in-memory sliding-window counter — single-process,
// same assumption the Postgres job queue already makes (no Redis).
type Limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func NewLimiter() *Limiter {
	return &Limiter{hits: map[string][]time.Time{}}
}

func (l *Limiter) Allow(key string, window time.Duration, max int, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := now.Add(-window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= max {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}
