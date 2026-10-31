package postgres

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/handziurdmytro/skelar/internal/support/ports"
)

// Append assigns the ticket's next seq (the UPDATE's row lock orders
// concurrent appends) and inserts the event. Call it inside the same
// WithinTx as whatever else this processing pass writes, so the seq
// bump, the event and e.g. a message insert commit together or not at all.
func (s *Store) Append(ctx context.Context, ticketID string, typ string, staff, public json.RawMessage) (ports.Event, error) {
	var seq int64
	if err := s.q(ctx).QueryRow(ctx,
		`UPDATE support_tickets SET last_seq = last_seq + 1 WHERE id = $1 RETURNING last_seq`, ticketID,
	).Scan(&seq); err != nil {
		return ports.Event{}, err
	}

	e := ports.Event{TicketID: ticketID, Seq: seq, Type: typ, Staff: staff, Public: public}
	if err := s.q(ctx).QueryRow(ctx, `
		INSERT INTO support_events (ticket_id, seq, type, staff, public)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at`,
		ticketID, seq, typ, staff, public,
	).Scan(&e.CreatedAt); err != nil {
		return ports.Event{}, err
	}
	return e, nil
}

// After returns events after seq, in order — used both for SSE backlog
// replay (Last-Event-ID) and for building a ticket snapshot's trace.
func (s *Store) After(ctx context.Context, ticketID string, seq int64) ([]ports.Event, error) {
	rows, err := s.q(ctx).Query(ctx, `
		SELECT ticket_id, seq, type, staff, public, created_at
		FROM support_events WHERE ticket_id = $1 AND seq > $2 ORDER BY seq`, ticketID, seq)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (ports.Event, error) {
		var e ports.Event
		err := row.Scan(&e.TicketID, &e.Seq, &e.Type, &e.Staff, &e.Public, &e.CreatedAt)
		return e, err
	})
}
