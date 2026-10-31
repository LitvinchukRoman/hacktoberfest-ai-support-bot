package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/handziurdmytro/skelar/internal/support/ports"
)

// Enqueue marks the ticket's job queued; if it's already running, it's
// flagged dirty instead so the worker loops once more when it finishes —
// a message that arrives mid-processing is never dropped.
func (s *Store) Enqueue(ctx context.Context, ticketID string) error {
	_, err := s.q(ctx).Exec(ctx, `
		INSERT INTO support_jobs (ticket_id) VALUES ($1)
		ON CONFLICT (ticket_id) DO UPDATE
		SET dirty = support_jobs.dirty OR support_jobs.state = 'running'`, ticketID)
	return err
}

// Claim picks up to n due jobs (queued and due, or running past its lock)
// with FOR UPDATE SKIP LOCKED, so concurrent worker ticks never pick the
// same ticket. This is one atomic statement — no explicit transaction needed.
func (s *Store) Claim(ctx context.Context, n int) ([]ports.Job, error) {
	rows, err := s.q(ctx).Query(ctx, `
		WITH picked AS (
			SELECT ticket_id FROM support_jobs
			WHERE (state = 'queued' AND run_after <= now()) OR (state = 'running' AND locked_until < now())
			ORDER BY run_after LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE support_jobs j SET state = 'running', dirty = false, attempts = j.attempts + 1,
		       locked_until = now() + interval '2 minutes'
		FROM picked WHERE j.ticket_id = picked.ticket_id
		RETURNING j.ticket_id, j.attempts`, n)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (ports.Job, error) {
		var j ports.Job
		var attempts int16
		if err := row.Scan(&j.TicketID, &attempts); err != nil {
			return ports.Job{}, err
		}
		j.Attempts = int(attempts)
		j.State = "running"
		return j, nil
	})
}

// Finish deletes the job unless it went dirty while running — in which
// case it's put straight back in the queue for one more pass, because a
// new customer message arrived mid-processing.
func (s *Store) Finish(ctx context.Context, ticketID string) error {
	_, err := s.q(ctx).Exec(ctx, `
		WITH done AS (DELETE FROM support_jobs WHERE ticket_id = $1 AND NOT dirty RETURNING 1)
		UPDATE support_jobs SET state = 'queued', dirty = false, attempts = 0, run_after = now(), locked_until = NULL
		WHERE ticket_id = $1 AND NOT EXISTS (SELECT 1 FROM done)`, ticketID)
	return err
}

// Fail requeues after backoff with a short error code — last_error is
// capped at 200 chars by the column check, so truncate defensively here too.
func (s *Store) Fail(ctx context.Context, ticketID string, backoff time.Duration, errCode string) error {
	if len(errCode) > 200 {
		errCode = errCode[:200]
	}
	_, err := s.q(ctx).Exec(ctx, `
		UPDATE support_jobs SET state = 'queued', run_after = now() + $2::interval, last_error = $3
		WHERE ticket_id = $1`, ticketID, fmt.Sprintf("%d milliseconds", backoff.Milliseconds()), errCode)
	return err
}
