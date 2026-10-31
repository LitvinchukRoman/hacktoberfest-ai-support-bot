package support

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/handziurdmytro/skelar/internal/moderation"
	"github.com/handziurdmytro/skelar/internal/support/domain"
	"github.com/handziurdmytro/skelar/internal/support/guard"
	"github.com/handziurdmytro/skelar/internal/support/ports"
)

var (
	ErrConflict   = errors.New("already decided")
	ErrValidation = errors.New("validation failed")
)

const declinedReplyText = "Вибач, цю дію не підтвердили. Я передав деталі нашій команді, вони напишуть тобі окремо."

// Approve claims a pending support_actions row, executes the underlying
// effect (restore_idea goes through moderation.Decide) and sends the reply.
// A second Approve/Reject on the same action returns ErrConflict.
func (s *Service) Approve(ctx context.Context, actionID, decidedBy, editedReply string) error {
	return s.Store.WithinTx(ctx, func(ctx context.Context) error {
		a, ok, err := s.Store.ClaimApprove(ctx, actionID, decidedBy, editedReply)
		if err != nil {
			return err
		}
		if !ok {
			return ErrConflict
		}

		switch a.Type {
		case domain.ActRestoreIdea:
			ideaID := ""
			if a.IdeaID != nil {
				ideaID = *a.IdeaID
			}
			note := fmt.Sprintf("Відновлено через підтримку: ticket %s", a.TicketID)
			if err := s.Moderation.Decide(ctx, decidedBy, ideaID, moderation.Approved, note); err != nil {
				return err
			}
		case domain.ActDataRequest:
			// admin takes the GDPR request into work via CompleteDataRequest; nothing to execute here.
		}

		return s.finishApproved(ctx, a)
	})
}

func (s *Service) finishApproved(ctx context.Context, a ports.Action) error {
	reply := a.DraftReply
	if a.FinalReply != nil && *a.FinalReply != "" {
		reply = *a.FinalReply
	}
	// The action already executed (or there was nothing to execute, e.g.
	// a data request) — validate as auto: "I restored it" is now true.
	if ok, _ := guard.Validate(reply, domain.Decision{Autonomy: domain.AutonomyAuto}); !ok {
		reply = fallbackReplyText
	}

	ticket, found, err := s.Store.Get(ctx, a.TicketID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	ticket.Status = domain.StatusSolved
	auto := domain.AutonomyAuto
	ticket.Autonomy = &auto
	if ticket.FirstReplyAt == nil {
		now := s.now()
		ticket.FirstReplyAt = &now
	}
	if err := s.Store.UpdateAfterDecision(ctx, ticket); err != nil {
		return err
	}

	if _, err := s.Store.Insert(ctx, ports.NewMessageInput{ID: newID(), TicketID: ticket.ID, Author: "bot", Body: reply}); err != nil {
		return err
	}
	msgPayload, _ := json.Marshal(map[string]any{"author": "bot", "visibility": "public", "text": reply})
	if err := s.appendEvent(ctx, ticket.ID, "message", msgPayload, msgPayload); err != nil {
		return err
	}

	statusPayload, _ := json.Marshal(map[string]any{"status": "solved", "autonomy": "auto"})
	return s.appendEvent(ctx, ticket.ID, "status", statusPayload, statusPayload)
}

// Reject requires a reason (shown to staff only, never to the customer).
// The ticket stays escalated — a human already has it, declining the
// bot's proposal doesn't hand the conversation back to automation.
func (s *Service) Reject(ctx context.Context, actionID, decidedBy, reason string) error {
	if reason == "" {
		return ErrValidation
	}
	return s.Store.WithinTx(ctx, func(ctx context.Context) error {
		a, ok, err := s.Store.ClaimReject(ctx, actionID, decidedBy, reason)
		if err != nil {
			return err
		}
		if !ok {
			return ErrConflict
		}

		ticket, found, err := s.Store.Get(ctx, a.TicketID)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}

		ticket.Status = domain.StatusEscalated
		escalate := domain.AutonomyEscalate
		ticket.Autonomy = &escalate
		ticket.EscalationReason = "declined"
		if err := s.Store.UpdateAfterDecision(ctx, ticket); err != nil {
			return err
		}

		if _, err := s.Store.Insert(ctx, ports.NewMessageInput{ID: newID(), TicketID: ticket.ID, Author: "bot", Body: declinedReplyText}); err != nil {
			return err
		}
		msgPayload, _ := json.Marshal(map[string]any{"author": "bot", "visibility": "public", "text": declinedReplyText})
		if err := s.appendEvent(ctx, ticket.ID, "message", msgPayload, msgPayload); err != nil {
			return err
		}

		statusPayload, _ := json.Marshal(map[string]any{"status": "escalated", "reason": "declined"})
		return s.appendEvent(ctx, ticket.ID, "status", statusPayload, statusPayload)
	})
}
