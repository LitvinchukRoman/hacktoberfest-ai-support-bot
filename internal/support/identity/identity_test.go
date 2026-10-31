package identity

import (
	"testing"
	"time"
)

func TestStore(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	s := NewStore().WithClock(func() time.Time { return now })

	s.Put("t1", "token-a")
	if got := s.Get("t1"); got != "token-a" {
		t.Fatalf("Get = %q", got)
	}
	if got := s.Get("t2"); got != "" {
		t.Fatalf("an unknown ticket has no token, got %q", got)
	}

	s.Put("t1", "")
	if got := s.Get("t1"); got != "" {
		t.Fatalf("a message without a token must forget the old one, got %q", got)
	}

	s.Put("t1", "token-b")
	now = now.Add(TTL)
	if got := s.Get("t1"); got != "" {
		t.Fatalf("an expired token must not be used, got %q", got)
	}
	s.Put("t3", "token-c")
	if _, ok := s.tickets["t1"]; ok {
		t.Fatal("expired tokens must be dropped, not kept in memory")
	}
}

func TestNilStoreIsOff(t *testing.T) {
	var s *Store
	s.Put("t1", "token")
	if s.Get("t1") != "" {
		t.Fatal("a nil store holds nothing")
	}
}
