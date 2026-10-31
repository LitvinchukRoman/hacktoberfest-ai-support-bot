// Package identity hands the person's fantasm token from intake to the worker that
// processes the ticket. The token lets the bot read and change that person's
// billing, so it lives only in memory, for at most its own lifetime, and is never
// written to the database or the trace. After a restart the bot simply does not
// see billing until the next message brings a fresh token.
package identity

import (
	"sync"
	"time"
)

// TTL is fantasm's lifetime of a support token.
const TTL = 15 * time.Minute

type entry struct {
	token   string
	expires time.Time
}

type Store struct {
	mu      sync.Mutex
	tickets map[string]entry
	now     func() time.Time
}

func NewStore() *Store {
	return &Store{tickets: map[string]entry{}, now: time.Now}
}

// WithClock replaces the clock, for tests.
func (s *Store) WithClock(now func() time.Time) *Store { s.now = now; return s }

// Put remembers the token the latest message of the ticket came with. A message
// without one forgets the previous token: whoever writes now is not known, and the
// bot must not keep acting as the person who wrote before.
func (s *Store) Put(ticketID, token string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, e := range s.tickets {
		if !now.Before(e.expires) {
			delete(s.tickets, id)
		}
	}
	if token == "" {
		delete(s.tickets, ticketID)
		return
	}
	s.tickets[ticketID] = entry{token: token, expires: now.Add(TTL)}
}

// Get returns the ticket's token, or "" when there is none or it has expired.
func (s *Store) Get(ticketID string) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.tickets[ticketID]
	if !ok || !s.now().Before(e.expires) {
		return ""
	}
	return e.token
}
