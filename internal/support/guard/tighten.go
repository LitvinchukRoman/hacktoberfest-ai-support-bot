package guard

import (
	"slices"

	"github.com/handziurdmytro/skelar/internal/support/domain"
)

// Mode is the kill switch (platform_settings.support_mode).
type Mode string

const (
	ModeStandard   Mode = "standard"
	ModeApproveAll Mode = "approve_all"
	ModeShadow     Mode = "shadow"
)

type Config struct {
	MinConfidence  float64
	RepeatContacts int // angry + this many tickets in 24h -> escalate
}

var DefaultConfig = Config{MinConfidence: 0.6, RepeatContacts: 3}

// Input is everything Tighten needs: the policy's proposed decision plus
// everything guard saw independently of the LLM.
type Input struct {
	Decision      domain.Decision
	Class         domain.Classification
	Signals       []Signal
	PII           []string
	Mode          Mode
	RecentTickets int
}

// Tighten can only move a decision towards a human: auto -> approve ->
// escalate, never back. It is the last word on autonomy — policy proposes,
// Tighten disposes.
func Tighten(in Input, cfg Config) domain.Decision {
	d, c := in.Decision, in.Class
	has := func(s Signal) bool { return slices.Contains(in.Signals, s) }

	out := d
	switch {
	case has(SignalSelfHarm):
		out = escalate(d, "self_harm", "safety.support", domain.QueueAdmins, 1)
	case has(SignalLegal) || c.Intent == domain.IntentLegal:
		out = escalate(d, "legal", "escalation.legal", domain.QueueAdmins, 1)
	case has(SignalInjection) || slices.Contains(c.Signals, "prompt_injection_attempt"):
		out = escalate(d, "prompt_injection", "safety.cannot_do", domain.QueueAdmins, 2)
	case has(SignalDataRequest) || c.Intent == domain.IntentAccountData:
		out = domain.Decision{
			Autonomy:   domain.AutonomyApprove,
			Rule:       "G_data_request",
			TemplateID: "gdpr.request_received",
			Actions:    []domain.Action{{Type: domain.ActDataRequest, Queue: domain.QueueAdmins}},
			Status:     domain.StatusAwaitingApproval,
		}
	case slices.Contains(in.PII, "card") || slices.Contains(in.PII, "iban"):
		out = escalate(d, "payment_data_shared", "safety.pii_removed", domain.QueueAdmins, 2)
	case c.Intent == domain.IntentOffensive:
		out = escalate(d, "offensive", "escalation.offensive", domain.QueueModerators, 3)
	case has(SignalHuman) || c.Intent == domain.IntentHumanRequest:
		out = escalate(d, "human_requested", "escalation.human", domain.QueueModerators, 2)
	case c.Intent == domain.IntentUnknown || c.Confidence < cfg.MinConfidence:
		out = escalate(d, "low_confidence", "escalation.unclear", domain.QueueModerators, 3)
	case c.Emotion == domain.EmotionAngry && in.RecentTickets >= cfg.RepeatContacts:
		out = escalate(d, "repeat_contact", "escalation.human", domain.QueueModerators, 2)
	}

	if in.Mode == ModeApproveAll && out.Autonomy == domain.AutonomyAuto {
		out.Autonomy, out.Status = domain.AutonomyApprove, domain.StatusAwaitingApproval
	}

	if domain.Stricter(d.Autonomy, out.Autonomy) != out.Autonomy {
		// The candidate would relax what policy already decided — never
		// allowed, fall back to the original decision untouched.
		return d
	}
	return out
}

// escalate builds an escalated decision: rule gets a G_ prefix, actions and
// suggested buttons are cleared (an escalated ticket performs no actions),
// and the ticket moves to the given queue/priority.
func escalate(d domain.Decision, reason, template string, queue domain.Queue, priority int) domain.Decision {
	return domain.Decision{
		Autonomy:   domain.AutonomyEscalate,
		Rule:       "G_" + reason,
		Verdict:    d.Verdict,
		TemplateID: template,
		Slots:      d.Slots,
		Status:     domain.StatusEscalated,
		NextState:  d.NextState,
		Escalation: &domain.Escalation{Reason: reason, Queue: queue, Priority: priority},
	}
}
