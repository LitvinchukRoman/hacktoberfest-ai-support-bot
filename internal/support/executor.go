package support

import (
	"context"
	"encoding/json"
	"time"

	"github.com/handziurdmytro/skelar/internal/support/domain"
	"github.com/handziurdmytro/skelar/internal/support/guard"
	"github.com/handziurdmytro/skelar/internal/support/ports"
)

type executeInput struct {
	Ticket   ports.Ticket
	Messages []ports.Message
	Class    domain.Classification
	Facts    domain.Facts
	Proposed domain.Decision
	Final    domain.Decision
	Checks   []guard.Check
	Reply    string
	Usage    domain.Usage
}

func (s *Service) execute(ctx context.Context, in executeInput) error {
	shadow := s.mode(ctx) == guard.ModeShadow

	return s.Store.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.recordDecision(ctx, in); err != nil {
			return err
		}

		ids := make([]string, len(in.Messages))
		for i, m := range in.Messages {
			ids[i] = m.ID
		}
		if len(ids) > 0 {
			if err := s.Store.MarkProcessed(ctx, ids); err != nil {
				return err
			}
		}

		if shadow {
			return nil
		}

		t := in.Ticket
		t.Status = in.Final.Status
		autonomy := in.Final.Autonomy
		t.Autonomy = &autonomy
		t.Intent = in.Class.Intent
		t.Emotion = in.Class.Emotion
		t.State = in.Final.NextState
		if in.Facts.Idea != nil {
			t.IdeaID = idPtr(in.Facts.Idea.ID)
		}
		switch {
		case in.Final.Escalation != nil:
			t.Priority = in.Final.Escalation.Priority
			queue := in.Final.Escalation.Queue
			t.Queue = &queue
			t.EscalationReason = in.Final.Escalation.Reason
		case in.Final.Autonomy == domain.AutonomyApprove:
			q := firstActionQueue(in.Final.Actions)
			if q != "" {
				t.Queue = &q
				t.Priority = 2
			}
		}
		if in.Reply != "" && t.FirstReplyAt == nil {
			now := s.now()
			t.FirstReplyAt = &now
		}
		if err := s.Store.UpdateAfterDecision(ctx, t); err != nil {
			return err
		}

		if in.Reply != "" {
			if _, err := s.Store.Insert(ctx, ports.NewMessageInput{ID: newID(), TicketID: t.ID, Author: "bot", Body: in.Reply}); err != nil {
				return err
			}
			payload, _ := json.Marshal(map[string]any{"author": "bot", "visibility": "public", "text": in.Reply})
			if err := s.appendEvent(ctx, t.ID, "message", payload, payload); err != nil {
				return err
			}
		}

		for _, a := range in.Final.Actions {
			if err := s.recordAction(ctx, t, in.Final, a, in.Reply); err != nil {
				return err
			}
		}

		statusPayload, _ := json.Marshal(map[string]any{
			"status": t.Status, "autonomy": autonomy, "reason": t.EscalationReason,
		})
		return s.appendEvent(ctx, t.ID, "status", statusPayload, statusPayload)
	})
}

func (s *Service) recordDecision(ctx context.Context, in executeInput) error {
	cls, err := json.Marshal(in.Class)
	if err != nil {
		return err
	}
	decision, err := json.Marshal(in.Final)
	if err != nil {
		return err
	}
	checks := in.Checks
	if checks == nil {
		checks = []guard.Check{}
	}
	checksJSON, err := json.Marshal(checks)
	if err != nil {
		return err
	}

	return s.Store.InsertDecision(ctx, ports.NewDecisionInput{
		ID:               newID(),
		TicketID:         in.Ticket.ID,
		Classification:   cls,
		PolicyRule:       in.Proposed.Rule,
		FinalRule:        in.Final.Rule,
		Autonomy:         in.Final.Autonomy,
		Decision:         decision,
		Checks:           checksJSON,
		Model:            in.Usage.Model,
		PromptTokens:     in.Usage.PromptTokens,
		CompletionTokens: in.Usage.CompletionTokens,
		LatencyMS:        in.Usage.LatencyMS,
	})
}

func (s *Service) recordAction(ctx context.Context, t ports.Ticket, final domain.Decision, a domain.Action, reply string) error {
	evidence := json.RawMessage("[]")
	if final.Verdict != nil && len(final.Verdict.Evidence) > 0 {
		var err error
		evidence, err = json.Marshal(final.Verdict.Evidence)
		if err != nil {
			return err
		}
	}

	status := "pending"
	var decidedAt *time.Time
	if final.Autonomy == domain.AutonomyAuto {
		status = "executed"
		now := s.now()
		decidedAt = &now
	}

	if err := s.Store.InsertAction(ctx, ports.NewActionInput{
		ID:             newID(),
		TicketID:       t.ID,
		Type:           a.Type,
		IdeaID:         idPtr(a.IdeaID),
		Params:         a.Params,
		Status:         status,
		IdempotencyKey: t.ID + ":" + final.Rule + ":" + string(a.Type),
		Rule:           final.Rule,
		Evidence:       evidence,
		DraftReply:     reply,
		DecidedAt:      decidedAt,
	}); err != nil {
		return err
	}

	if a.Type == domain.ActDataRequest {
		return s.createDataRequest(ctx, t, a)
	}
	return nil
}

func firstActionQueue(actions []domain.Action) domain.Queue {
	for _, a := range actions {
		if a.Queue != "" {
			return a.Queue
		}
	}
	return ""
}

func idPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
