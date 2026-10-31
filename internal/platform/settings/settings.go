// Package settings stands in for fantasm's real platform_settings store
// (lives in the main product, keyed on things like "premoderation" too —
// out of scope here). A 30s cache per key, same as the real one per the
// brief ("після Set виклич Forget, бо кеш живе 30 с").
package settings

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const ttl = 30 * time.Second

type DB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type entry struct {
	value   string
	expires time.Time
}

type Store struct {
	db DB

	mu     sync.Mutex
	cached map[string]entry
}

func New(db DB) *Store {
	return &Store{db: db, cached: map[string]entry{}}
}

func (s *Store) Get(ctx context.Context, key string) (string, error) {
	s.mu.Lock()
	if e, ok := s.cached[key]; ok && time.Now().Before(e.expires) {
		s.mu.Unlock()
		return e.value, nil
	}
	s.mu.Unlock()

	var value string
	err := s.db.QueryRow(ctx, `SELECT value FROM platform_settings WHERE key = $1`, key).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		value = ""
	} else if err != nil {
		return "", err
	}

	s.mu.Lock()
	s.cached[key] = entry{value: value, expires: time.Now().Add(ttl)}
	s.mu.Unlock()
	return value, nil
}

func (s *Store) Set(ctx context.Context, key, value string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO platform_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, value)
	if err != nil {
		return err
	}
	s.Forget(key)
	return nil
}

func (s *Store) Forget(key string) {
	s.mu.Lock()
	delete(s.cached, key)
	s.mu.Unlock()
}
