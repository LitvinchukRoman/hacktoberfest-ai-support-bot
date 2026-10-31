package postgres

import (
	"context"
	"encoding/json"

	"github.com/handziurdmytro/skelar/internal/support/ports"
)

func (s *Store) InsertAction(ctx context.Context, in ports.NewActionInput) error {
	params := in.Params
	if params == nil {
		params = map[string]string{}
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return err
	}
	evidence := in.Evidence
	if evidence == nil {
		evidence = json.RawMessage("[]")
	}
	_, err = s.q(ctx).Exec(ctx, `
		INSERT INTO support_actions (id, ticket_id, type, idea_id, params, status, idempotency_key, rule, evidence, draft_reply, decided_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		in.ID, in.TicketID, string(in.Type), in.IdeaID, paramsJSON, in.Status, in.IdempotencyKey, in.Rule, evidence, in.DraftReply, in.DecidedAt,
	)
	return err
}
