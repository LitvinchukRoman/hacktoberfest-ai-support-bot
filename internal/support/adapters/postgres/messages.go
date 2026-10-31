package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/handziurdmytro/skelar/internal/support/ports"
)

const messageColumns = `id, ticket_id, author, agent_id, visibility, body, actions, pii_found, signals, processed_at, created_at`

// rowScanner is satisfied by both pgx.Row (QueryRow) and pgx.Rows/
// CollectableRow (Query + CollectRows) — scanMessage works for either.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanMessage(row rowScanner) (ports.Message, error) {
	var m ports.Message
	if err := row.Scan(
		&m.ID, &m.TicketID, &m.Author, &m.AgentID, &m.Visibility, &m.Body, &m.Actions, &m.PIIFound, &m.Signals,
		&m.ProcessedAt, &m.CreatedAt,
	); err != nil {
		return ports.Message{}, err
	}
	return m, nil
}

// Insert stores a message whose Body is already redacted — callers must
// run guard.Redact before this, never the raw customer text.
func (s *Store) Insert(ctx context.Context, in ports.NewMessageInput) (ports.Message, error) {
	piiFound, signals := in.PIIFound, in.Signals
	if piiFound == nil {
		piiFound = []string{}
	}
	if signals == nil {
		signals = []string{}
	}
	row := s.q(ctx).QueryRow(ctx, `
		INSERT INTO support_messages (id, ticket_id, author, body, pii_found, signals)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+messageColumns,
		in.ID, in.TicketID, in.Author, in.Body, piiFound, signals,
	)
	return scanMessage(row)
}

// Unprocessed returns customer messages not yet picked up by the worker,
// oldest first — a burst of quick follow-ups is handled together as one reply.
func (s *Store) Unprocessed(ctx context.Context, ticketID string) ([]ports.Message, error) {
	rows, err := s.q(ctx).Query(ctx, `
		SELECT `+messageColumns+` FROM support_messages
		WHERE ticket_id = $1 AND author = 'customer' AND processed_at IS NULL
		ORDER BY created_at`, ticketID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (ports.Message, error) { return scanMessage(row) })
}

func (s *Store) MarkProcessed(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.q(ctx).Exec(ctx, `UPDATE support_messages SET processed_at = now() WHERE id = ANY($1)`, ids)
	return err
}

// ForTicket returns messages for a ticket snapshot: public-only for the
// customer, or everything (including internal handoff notes) for staff.
func (s *Store) ForTicket(ctx context.Context, ticketID string, includeInternal bool) ([]ports.Message, error) {
	q := `SELECT ` + messageColumns + ` FROM support_messages WHERE ticket_id = $1`
	if !includeInternal {
		q += ` AND visibility = 'public'`
	}
	q += ` ORDER BY created_at`

	rows, err := s.q(ctx).Query(ctx, q, ticketID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (ports.Message, error) { return scanMessage(row) })
}
