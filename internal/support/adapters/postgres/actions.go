package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/handziurdmytro/skelar/internal/support/domain"
	"github.com/handziurdmytro/skelar/internal/support/ports"
)

const actionColumns = `id, ticket_id, type, idea_id, params, status, idempotency_key, rule, evidence,
	draft_reply, final_reply, edited, reason, decided_by, decided_at, error, created_at`

func scanAction(row rowScanner) (ports.Action, error) {
	var (
		a           ports.Action
		typ, status string
		paramsJSON  []byte
	)
	if err := row.Scan(
		&a.ID, &a.TicketID, &typ, &a.IdeaID, &paramsJSON, &status, &a.IdempotencyKey, &a.Rule, &a.Evidence,
		&a.DraftReply, &a.FinalReply, &a.Edited, &a.Reason, &a.DecidedBy, &a.DecidedAt, &a.Error, &a.CreatedAt,
	); err != nil {
		return ports.Action{}, err
	}
	a.Type = domain.ActionType(typ)
	a.Status = status
	if len(paramsJSON) > 0 {
		_ = json.Unmarshal(paramsJSON, &a.Params)
	}
	return a, nil
}

func (s *Store) GetAction(ctx context.Context, id string) (ports.Action, bool, error) {
	row := s.q(ctx).QueryRow(ctx, `SELECT `+actionColumns+` FROM support_actions WHERE id = $1`, id)
	a, err := scanAction(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.Action{}, false, nil
	}
	if err != nil {
		return ports.Action{}, false, err
	}
	return a, true, nil
}

func (s *Store) ClaimApprove(ctx context.Context, id, decidedBy, editedReply string) (ports.Action, bool, error) {
	row := s.q(ctx).QueryRow(ctx, `
		UPDATE support_actions SET status = 'approved', decided_by = $2, decided_at = now(),
			final_reply = CASE WHEN $3 = '' THEN final_reply ELSE $3 END,
			edited = CASE WHEN $3 = '' THEN edited ELSE true END
		WHERE id = $1 AND status = 'pending'
		RETURNING `+actionColumns,
		id, decidedBy, editedReply,
	)
	a, err := scanAction(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.Action{}, false, nil
	}
	if err != nil {
		return ports.Action{}, false, err
	}
	return a, true, nil
}

func (s *Store) ClaimReject(ctx context.Context, id, decidedBy, reason string) (ports.Action, bool, error) {
	row := s.q(ctx).QueryRow(ctx, `
		UPDATE support_actions SET status = 'rejected', decided_by = $2, decided_at = now(), reason = $3
		WHERE id = $1 AND status = 'pending'
		RETURNING `+actionColumns,
		id, decidedBy, reason,
	)
	a, err := scanAction(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.Action{}, false, nil
	}
	if err != nil {
		return ports.Action{}, false, err
	}
	return a, true, nil
}

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
