package postgres

import (
	"context"
	"time"

	"github.com/handziurdmytro/skelar/internal/support/ports"
)

func (s *Store) InsertDecision(ctx context.Context, in ports.NewDecisionInput) error {
	_, err := s.q(ctx).Exec(ctx, `
		INSERT INTO support_decisions (id, ticket_id, classification, policy_rule, final_rule, autonomy,
			decision, checks, model, prompt_tokens, completion_tokens, latency_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		in.ID, in.TicketID, in.Classification, in.PolicyRule, in.FinalRule, string(in.Autonomy),
		in.Decision, in.Checks, in.Model, in.PromptTokens, in.CompletionTokens, in.LatencyMS,
	)
	return err
}

func (s *Store) PurgeBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.q(ctx).Exec(ctx, `DELETE FROM support_decisions WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
