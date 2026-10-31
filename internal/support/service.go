package support

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/handziurdmytro/skelar/internal/moderation"
	"github.com/handziurdmytro/skelar/internal/platform/settings"
	"github.com/handziurdmytro/skelar/internal/support/billing"
	"github.com/handziurdmytro/skelar/internal/support/domain"
	"github.com/handziurdmytro/skelar/internal/support/guard"
	"github.com/handziurdmytro/skelar/internal/support/identity"
	"github.com/handziurdmytro/skelar/internal/support/policy"
	"github.com/handziurdmytro/skelar/internal/support/ports"
	"github.com/handziurdmytro/skelar/internal/support/stream"
)

type Store interface {
	ports.TicketRepo
	ports.MessageRepo
	ports.EventRepo
	ports.JobQueue
	ports.ActionRepo
	ports.DecisionRepo
	ports.DataRequestRepo
	ports.TxManager
}

type Service struct {
	Store         Store
	Broker        *stream.Broker
	Brain         domain.Brain
	Fallback      domain.Classifier
	Moderation    *moderation.Service
	Mode          func(ctx context.Context) guard.Mode
	Now           func() time.Time
	RetentionDays int
	// Identities holds each ticket's fantasm token; Billing makes the one billing
	// change the bot is allowed. Without Billing the bot hands such cases to a manager.
	Identities *identity.Store
	Billing    domain.BillingActions
}

// appendEvent persists the event and schedules its broadcast for right
// after the enclosing transaction (if any) commits — never before.
func (s *Service) appendEvent(ctx context.Context, ticketID, typ string, staff, public json.RawMessage) error {
	e, err := s.Store.Append(ctx, ticketID, typ, staff, public)
	if err != nil {
		return err
	}
	if s.Broker != nil {
		ports.AfterCommit(ctx, func() { s.Broker.Publish(e) })
	}
	return nil
}

const (
	claimBatch   = 20
	maxAttempts  = 3
	backoffFirst = 2 * time.Second
	backoffNext  = 8 * time.Second

	fallbackReplyText = "Вибач, у мене сталася технічна помилка. Я передав твоє звернення команді підтримки."
)

func (s *Service) mode(ctx context.Context) guard.Mode {
	if s.Mode == nil {
		return guard.ModeStandard
	}
	return s.Mode(ctx)
}

func (s *Service) now() time.Time {
	if s.Now == nil {
		return time.Now().UTC()
	}
	return s.Now()
}

func (s *Service) ProcessDue(ctx context.Context) error {
	jobs, err := s.Store.Claim(ctx, claimBatch)
	if err != nil {
		return fmt.Errorf("claim jobs: %w", err)
	}
	for _, j := range jobs {
		if err := s.processTicket(ctx, j.TicketID); err != nil {
			slog.Error("support: process ticket", "ticket", j.TicketID, "attempt", j.Attempts, "err", err)
			s.fail(ctx, j.TicketID, j.Attempts)
			continue
		}
		if err := s.Store.Finish(ctx, j.TicketID); err != nil {
			slog.Error("support: finish job", "ticket", j.TicketID, "err", err)
		}
	}
	return nil
}

func (s *Service) fail(ctx context.Context, ticketID string, attempts int) {
	if attempts >= maxAttempts {
		s.escalateTechnical(ctx, ticketID)
		if err := s.Store.Finish(ctx, ticketID); err != nil {
			slog.Error("support: drop job after permanent failure", "ticket", ticketID, "err", err)
		}
		return
	}
	backoff := backoffFirst
	if attempts >= 2 {
		backoff = backoffNext
	}
	if err := s.Store.Fail(ctx, ticketID, backoff, "processing_error"); err != nil {
		slog.Error("support: requeue after failure", "ticket", ticketID, "err", err)
	}
}

func (s *Service) escalateTechnical(ctx context.Context, ticketID string) {
	ticket, ok, err := s.Store.Get(ctx, ticketID)
	if err != nil || !ok {
		return
	}
	final := domain.Decision{
		Autonomy:   domain.AutonomyEscalate,
		Rule:       "G_technical",
		Status:     domain.StatusEscalated,
		Escalation: &domain.Escalation{Reason: "technical", Queue: domain.QueueAdmins, Priority: 1},
	}
	if err := s.execute(ctx, executeInput{Ticket: ticket, Final: final, Reply: fallbackReplyText}); err != nil {
		slog.Error("support: escalate after permanent failure", "ticket", ticketID, "err", err)
	}
}

func (s *Service) processTicket(ctx context.Context, ticketID string) error {
	ticket, ok, err := s.Store.Get(ctx, ticketID)
	if err != nil {
		return fmt.Errorf("get ticket: %w", err)
	}
	if !ok {
		return nil
	}

	msgs, err := s.Store.Unprocessed(ctx, ticketID)
	if err != nil {
		return fmt.Errorf("unprocessed messages: %w", err)
	}
	if len(msgs) == 0 {
		return nil
	}

	sig, pii := aggregateSignals(msgs)
	s.trace(ctx, ticketID, "prefilter", "Перевіряю повідомлення…", map[string]any{"signals": sig, "pii": pii})

	classifyIn := domain.ClassifyInput{Text: joinBodies(msgs), State: ticket.State, Page: ticket.Page}
	cls, usage, err := s.Brain.Classifier.Classify(ctx, classifyIn)
	if err != nil {
		cls, usage, err = s.Fallback.Classify(ctx, classifyIn)
		if err != nil {
			return fmt.Errorf("fallback classify: %w", err)
		}
		usage.Model = "fake"
	}
	s.trace(ctx, ticketID, "classify", "Розбираюсь, про що питання…", cls)

	userID := ""
	if ticket.UserID != nil {
		userID = *ticket.UserID
	}
	identityToken := s.Identities.Get(ticketID)
	facts, err := s.Brain.Facts.Facts(ctx, domain.FactsInput{
		UserID: userID, Identity: identityToken, IdeaSlug: ticket.IdeaSlug, IdeaHint: cls.IdeaHint,
		Intent: cls.Intent, Question: first(cls.UserQuestions), State: ticket.State, Now: s.now(),
	})
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	s.trace(ctx, ticketID, "context", "Дивлюсь твій акаунт…", facts)

	proposed := s.Brain.Decide(cls, facts)
	if proposed.Verdict != nil {
		s.trace(ctx, ticketID, "investigate", "Перевіряю, що сталося…", proposed.Verdict)
	}

	final := guard.Tighten(guard.Input{
		Decision: proposed, Class: cls, Signals: sig, PII: pii,
		Mode: s.mode(ctx), RecentTickets: facts.RecentTickets, State: ticket.State,
	}, guard.DefaultConfig)
	s.trace(ctx, ticketID, "decide", "Вирішую, що робити…",
		map[string]any{"policy": proposed.Rule, "final": final.Rule, "autonomy": final.Autonomy})

	final = s.giveGoodwill(ctx, ticketID, identityToken, final)

	render := func(template string, slots map[string]string) (string, error) {
		reply, _, err := s.Brain.Renderer.Render(ctx, domain.RenderInput{
			TemplateID: template, Slots: slots, Language: cls.Language,
			Emotion: cls.Emotion, Question: first(cls.UserQuestions), Knowledge: facts.Knowledge,
		})
		return reply, err
	}
	reply, err := render(final.TemplateID, final.Slots)
	validated, checks := guard.Validate(reply, final)
	s.trace(ctx, ticketID, "validate", "Перевіряю відповідь…", checks)
	if err != nil || !validated {
		final = fallbackEscalation(final, guard.FailedCheck(checks))
		reply = fallbackReplyText
	}

	// Free days waiting for a colleague: the person hears they are asked for now,
	// and the colleague's approval sends the reply that says they were given.
	draft := ""
	if final.TemplateID == "retention.goodwill_pending" {
		if done, err := render("retention.goodwill", final.Slots); err == nil {
			draft = done
		}
	}

	s.trace(ctx, ticketID, "route", routeSummary(final), final.Escalation)

	return s.execute(ctx, executeInput{
		Ticket: ticket, Messages: msgs, Class: cls, Facts: facts, Proposed: proposed, Final: final,
		Checks: checks, Reply: reply, Draft: draft, Usage: usage,
	})
}

// giveGoodwill makes the one billing change the bot does on its own, free days of
// the plan, before the reply says they were given. When fantasm does not apply
// them, the person goes to a support manager instead of hearing a promise.
func (s *Service) giveGoodwill(ctx context.Context, ticketID, identityToken string, d domain.Decision) domain.Decision {
	if d.Autonomy != domain.AutonomyAuto || s.mode(ctx) == guard.ModeShadow {
		return d
	}
	a, ok := extendAction(d)
	if !ok {
		return d
	}
	if s.Billing == nil {
		return goodwillFailed(d, "білінг fantasm не підключений до бота")
	}
	days, _ := strconv.Atoi(a.Params["days"])
	ext, err := s.Billing.ExtendPlan(ctx, identityToken, billing.GoodwillActionID(a.Params["handle"]), days)
	if err != nil {
		slog.Warn("support: extend plan", "ticket", ticketID, "err", err)
		return goodwillFailed(d, "fantasm не застосував подарунок днів: "+err.Error())
	}
	s.trace(ctx, ticketID, "act", "Додаю дні до плану…", map[string]any{"periodEnd": ext.PeriodEnd, "replayed": ext.Replayed})
	if ext.PeriodEnd.IsZero() {
		return d
	}
	out := d
	out.Slots = maps.Clone(d.Slots)
	out.Slots["until"] = policy.Day(ext.PeriodEnd)
	return out
}

func extendAction(d domain.Decision) (domain.Action, bool) {
	for _, a := range d.Actions {
		if a.Type == domain.ActExtendPlan {
			return a, true
		}
	}
	return domain.Action{}, false
}

func goodwillFailed(d domain.Decision, why string) domain.Decision {
	return domain.Decision{
		Autonomy:   domain.AutonomyEscalate,
		Rule:       "G_billing_action_failed",
		TemplateID: "retention.handoff",
		Status:     domain.StatusEscalated,
		NextState:  d.NextState,
		Escalation: &domain.Escalation{Reason: "billing_action_failed", Queue: domain.QueueSupport, Priority: 2,
			Note: "клієнт хоче скасувати підписку; бот хотів подарувати безкоштовні дні, але " + why},
	}
}

func fallbackEscalation(d domain.Decision, failedCheck string) domain.Decision {
	reason := "fallback"
	if failedCheck != "" {
		reason = "fallback:" + failedCheck
	}
	return domain.Decision{
		Autonomy:   domain.AutonomyEscalate,
		Rule:       "L6_veto",
		Verdict:    d.Verdict,
		TemplateID: "escalation.fallback",
		Slots:      d.Slots,
		Status:     domain.StatusEscalated,
		NextState:  d.NextState,
		Escalation: &domain.Escalation{Reason: reason, Queue: domain.QueueAdmins, Priority: 2},
	}
}

func routeSummary(d domain.Decision) string {
	switch d.Autonomy {
	case domain.AutonomyEscalate:
		return "Передаю команді підтримки."
	case domain.AutonomyApprove:
		return "Пропоную рішення, чекаю підтвердження колеги."
	default:
		return "Вирішую автоматично."
	}
}

func aggregateSignals(msgs []ports.Message) ([]guard.Signal, []string) {
	var sig []guard.Signal
	var pii []string
	seenSig, seenPII := map[string]bool{}, map[string]bool{}
	for _, m := range msgs {
		for _, s := range m.Signals {
			if !seenSig[s] {
				seenSig[s] = true
				sig = append(sig, guard.Signal(s))
			}
		}
		for _, p := range m.PIIFound {
			if !seenPII[p] {
				seenPII[p] = true
				pii = append(pii, p)
			}
		}
	}
	return sig, pii
}

func joinBodies(msgs []ports.Message) string {
	parts := make([]string, len(msgs))
	for i, m := range msgs {
		parts[i] = m.Body
	}
	return strings.Join(parts, "\n")
}

func first(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[0]
}

func (s *Service) trace(ctx context.Context, ticketID, stage, summary string, data any) {
	type payload struct {
		Stage   string `json:"stage"`
		Summary string `json:"summary"`
		Data    any    `json:"data,omitempty"`
	}
	staff, err := json.Marshal(payload{Stage: stage, Summary: summary, Data: data})
	if err != nil {
		slog.Error("support: marshal trace", "stage", stage, "err", err)
		return
	}
	public, err := json.Marshal(payload{Stage: stage, Summary: summary})
	if err != nil {
		slog.Error("support: marshal trace", "stage", stage, "err", err)
		return
	}
	if err := s.appendEvent(ctx, ticketID, "trace", staff, public); err != nil {
		slog.Error("support: append trace", "ticket", ticketID, "stage", stage, "err", err)
	}
}

func newID() string { return uuid.NewString() }

const SettingsKeySupportMode = "support_mode"

// ModeFromSettings reads the support_mode kill switch from settings,
// defaulting to standard for an unset or unrecognized value.
func ModeFromSettings(store *settings.Store) func(context.Context) guard.Mode {
	return func(ctx context.Context) guard.Mode {
		v, err := store.Get(ctx, SettingsKeySupportMode)
		if err != nil {
			return guard.ModeStandard
		}
		switch guard.Mode(v) {
		case guard.ModeApproveAll, guard.ModeShadow, guard.ModeStandard:
			return guard.Mode(v)
		default:
			return guard.ModeStandard
		}
	}
}
