// Package moderation stands in for fantasm's real moderation.Service,
// which lives in the main product and isn't part of either hackathon
// backend role. This repo is standalone, so a minimal stub closes the
// idea/moderation_cases loop just enough for restore_idea approvals to be
// demonstrably correct: idea state flips, decided_by is the approving
// moderator. The real service also notifies the author and resolves
// reports — out of scope here.
package moderation

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

type Decision string

const (
	Approved Decision = "APPROVED"
	Rejected Decision = "REJECTED"
)

var ErrNotFound = errors.New("idea not found")

type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type Service struct {
	DB DB
}

func (s *Service) Decide(ctx context.Context, viewerID, ideaID string, decision Decision, note string) error {
	tag, err := s.DB.Exec(ctx, `UPDATE ideas SET moderation_state = $2 WHERE id = $1`, ideaID, string(decision))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, err = s.DB.Exec(ctx, `
		INSERT INTO moderation_cases (id, idea_id, source, state, decided_by, note)
		VALUES (gen_random_uuid(), $1, 'REPORTS', $2, $3, $4)`,
		ideaID, string(decision), viewerID, note)
	return err
}
