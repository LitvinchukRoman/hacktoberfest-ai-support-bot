package guard

import (
	"slices"
	"strconv"

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
	State         domain.ConversationState // where the conversation was before this message
}

// aboutMoney reports whether the conversation is about what the person pays: a
// soft escalation then goes to a support manager, who can see and change billing,
// instead of to moderators, who can do neither.
func aboutMoney(in Input) bool {
	if in.State.Flow == domain.FlowRetention {
		return true
	}
	if e := in.Decision.Escalation; e != nil && e.Queue == domain.QueueSupport {
		return true
	}
	switch in.Class.Intent {
	case domain.IntentBillingCancel, domain.IntentBillingRefund, domain.IntentBillingQuestion:
		return true
	}
	return false
}

// Tighten can only move a decision towards a human: auto -> approve ->
// escalate, never back. It is the last word on autonomy — policy proposes,
// Tighten disposes.
func Tighten(in Input, cfg Config) domain.Decision {
	d, c := in.Decision, in.Class
	has := func(s Signal) bool { return slices.Contains(in.Signals, s) }
	soft := domain.QueueModerators
	if aboutMoney(in) {
		soft = domain.QueueSupport
	}

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
		out = escalate(d, "human_requested", "escalation.human", soft, 2)
	case c.Intent == domain.IntentUnknown || c.Confidence < cfg.MinConfidence:
		out = escalate(d, "low_confidence", "escalation.unclear", soft, 3)
	case c.Emotion == domain.EmotionAngry && in.RecentTickets >= cfg.RepeatContacts:
		out = escalate(d, "repeat_contact", "escalation.human", soft, 2)
	}

	if in.Mode == ModeApproveAll && out.Autonomy == domain.AutonomyAuto {
		out.Autonomy, out.Status = domain.AutonomyApprove, domain.StatusAwaitingApproval
	}

	// Hard floor, independent of whatever policy proposed: an irreversible
	// or money/rights-changing action is never auto-executed. This does not
	// trust the policy test suite's own invariant — it enforces the same
	// guarantee again here, on the Ops side.
	if out.Autonomy == domain.AutonomyAuto && hasHighRiskAction(out.Actions) {
		out.Autonomy, out.Status = domain.AutonomyApprove, domain.StatusAwaitingApproval
	}

	// Free days that wait for a colleague have not been given yet, so the reply
	// must not say they were.
	if out.Autonomy == domain.AutonomyApprove && out.TemplateID == "retention.goodwill" {
		out.TemplateID = "retention.goodwill_pending"
	}

	if domain.Stricter(d.Autonomy, out.Autonomy) != out.Autonomy {
		// The candidate would relax what policy already decided — never
		// allowed, fall back to the original decision untouched.
		return d
	}
	return out
}

// highRiskActions never auto-execute, no matter what autonomy a policy
// proposes: restoring content overrides a moderator's prior call, and a
// GDPR action touches the customer's rights. Both require a human.
var highRiskActions = map[domain.ActionType]bool{
	domain.ActRestoreIdea: true,
	domain.ActDataRequest: true,
}

// MaxGoodwillDays caps the free days the bot gives on its own; a larger gift is a
// colleague's call.
const MaxGoodwillDays = 14

func hasHighRiskAction(actions []domain.Action) bool {
	for _, a := range actions {
		if highRiskActions[a.Type] || (a.Type == domain.ActExtendPlan && !smallGift(a)) {
			return true
		}
	}
	return false
}

func smallGift(a domain.Action) bool {
	days, err := strconv.Atoi(a.Params["days"])
	return err == nil && days >= 1 && days <= MaxGoodwillDays && a.Params["handle"] != ""
}

// escalate builds an escalated decision: rule gets a G_ prefix, actions and
// suggested buttons are cleared (an escalated ticket performs no actions),
// and the ticket moves to the given queue/priority. The policy's note stays:
// it is what the colleague reads first.
func escalate(d domain.Decision, reason, template string, queue domain.Queue, priority int) domain.Decision {
	note := ""
	if d.Escalation != nil {
		note = d.Escalation.Note
	}
	return domain.Decision{
		Autonomy:   domain.AutonomyEscalate,
		Rule:       "G_" + reason,
		Verdict:    d.Verdict,
		TemplateID: template,
		Slots:      d.Slots,
		Status:     domain.StatusEscalated,
		NextState:  d.NextState,
		Escalation: &domain.Escalation{Reason: reason, Queue: queue, Priority: priority, Note: note},
	}
}
