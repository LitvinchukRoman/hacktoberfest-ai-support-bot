package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/handziurdmytro/skelar/internal/support/ports"
)

func (s *Store) Metrics(ctx context.Context, since time.Time, avgHandleMin float64) (ports.Metrics, error) {
	var m ports.Metrics
	var autoResolvedPct, escalatedPct, medianFirstReply *float64
	if err := s.q(ctx).QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE status = 'solved' AND queue IS NULL)::float8 / NULLIF(count(*), 0),
		       count(*) FILTER (WHERE status = 'escalated')::float8 / NULLIF(count(*), 0),
		       count(*) FILTER (WHERE status = 'awaiting_approval'),
		       percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM first_reply_at - created_at))
		FROM support_tickets WHERE created_at > $1`,
		since,
	).Scan(&m.TicketsTotal, &autoResolvedPct, &escalatedPct, &m.AwaitingApproval, &medianFirstReply); err != nil {
		return ports.Metrics{}, err
	}
	if autoResolvedPct != nil {
		m.AutoResolvedPct = *autoResolvedPct
	}
	if escalatedPct != nil {
		m.EscalatedPct = *escalatedPct
	}
	if medianFirstReply != nil {
		m.MedianFirstReplySec = *medianFirstReply
	}

	autoResolvedCount := 0.0
	if m.TicketsTotal > 0 {
		autoResolvedCount = m.AutoResolvedPct * float64(m.TicketsTotal)
	}
	m.HoursSaved = autoResolvedCount * avgHandleMin / 60

	var acceptedUnchangedPct *float64
	if err := s.q(ctx).QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE NOT edited)::float8 / NULLIF(count(*), 0)
		FROM support_actions WHERE status IN ('approved', 'executed') AND created_at > $1`,
		since,
	).Scan(&acceptedUnchangedPct); err != nil {
		return ports.Metrics{}, err
	}
	if acceptedUnchangedPct != nil {
		m.AcceptedUnchangedPct = *acceptedUnchangedPct
	}

	if err := s.q(ctx).QueryRow(ctx, `
		SELECT coalesce(sum(prompt_tokens + completion_tokens), 0) FROM support_decisions WHERE created_at > $1`,
		since,
	).Scan(&m.TotalTokens); err != nil {
		return ports.Metrics{}, err
	}

	intents, err := s.groupCount(ctx, `
		SELECT intent, count(*) FROM support_tickets WHERE created_at > $1 AND intent != '' GROUP BY intent`, since)
	if err != nil {
		return ports.Metrics{}, err
	}
	m.Intents = intents

	reasons, err := s.groupCount(ctx, `
		SELECT escalation_reason, count(*) FROM support_tickets
		WHERE created_at > $1 AND escalation_reason IS NOT NULL AND escalation_reason != '' GROUP BY escalation_reason`, since)
	if err != nil {
		return ports.Metrics{}, err
	}
	m.EscalationsByReason = reasons

	return m, nil
}

func (s *Store) groupCount(ctx context.Context, sql string, since time.Time) (map[string]int, error) {
	rows, err := s.q(ctx).Query(ctx, sql, since)
	if err != nil {
		return nil, err
	}
	result, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (struct {
		Key   string
		Count int
	}, error) {
		var kv struct {
			Key   string
			Count int
		}
		err := row.Scan(&kv.Key, &kv.Count)
		return kv, err
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(result))
	for _, kv := range result {
		out[kv.Key] = kv.Count
	}
	return out, nil
}
