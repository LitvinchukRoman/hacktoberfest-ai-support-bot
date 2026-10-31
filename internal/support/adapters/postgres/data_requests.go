package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/handziurdmytro/skelar/internal/support/ports"
)

const dataRequestColumns = `id, ticket_id, user_id, kind, status, due_at, decided_by, note, completed_at, created_at`

func scanDataRequest(row rowScanner) (ports.DataRequest, error) {
	var (
		dr   ports.DataRequest
		kind string
	)
	if err := row.Scan(
		&dr.ID, &dr.TicketID, &dr.UserID, &kind, &dr.Status, &dr.DueAt, &dr.DecidedBy, &dr.Note,
		&dr.CompletedAt, &dr.CreatedAt,
	); err != nil {
		return ports.DataRequest{}, err
	}
	dr.Kind = ports.DataRequestKind(kind)
	return dr, nil
}

func (s *Store) CreateDataRequest(ctx context.Context, in ports.NewDataRequestInput) (ports.DataRequest, error) {
	row := s.q(ctx).QueryRow(ctx, `
		INSERT INTO support_data_requests (id, ticket_id, user_id, kind, due_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+dataRequestColumns,
		in.ID, in.TicketID, in.UserID, string(in.Kind), in.DueAt,
	)
	return scanDataRequest(row)
}

func (s *Store) GetDataRequest(ctx context.Context, id string) (ports.DataRequest, bool, error) {
	row := s.q(ctx).QueryRow(ctx, `SELECT `+dataRequestColumns+` FROM support_data_requests WHERE id = $1`, id)
	dr, err := scanDataRequest(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.DataRequest{}, false, nil
	}
	if err != nil {
		return ports.DataRequest{}, false, err
	}
	return dr, true, nil
}

func (s *Store) ListOpen(ctx context.Context) ([]ports.DataRequest, error) {
	rows, err := s.q(ctx).Query(ctx, `
		SELECT `+dataRequestColumns+` FROM support_data_requests WHERE status = 'received' ORDER BY due_at`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (ports.DataRequest, error) { return scanDataRequest(row) })
}

// Complete only affects a row still 'received' — completing an
// already-completed request is a no-op (ok=false), not an overwrite of who
// handled it and when.
func (s *Store) Complete(ctx context.Context, id string, decidedBy string, note string) (bool, error) {
	tag, err := s.q(ctx).Exec(ctx, `
		UPDATE support_data_requests SET status = 'completed', decided_by = $2, note = $3, completed_at = now()
		WHERE id = $1 AND status = 'received'`, id, decidedBy, nullIfEmpty(note))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
