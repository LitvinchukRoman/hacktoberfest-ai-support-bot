// Package policy decides what the bot does with a classified message. Decide is a
// pure function of what the model understood and what the database says: no I/O,
// no clock (time comes from Facts.Now), no model, no randomness. Each branch is a
// named rule that appears in the trace. Safety signals, confidence and personal
// data are not looked at here: guard handles them and can only make a decision stricter.
package policy

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/handziurdmytro/skelar/internal/support/domain"
	"github.com/handziurdmytro/skelar/internal/support/investigate"
)

// trustedAfter is how many approved ideas make fantasm publish an author's next idea without premoderation.
const trustedAfter = 3

type Config struct {
	PremoderationSLA time.Duration // a pending idea waiting longer than this is overdue
	TrustThreshold   int           // approved ideas after which an author skips premoderation
	Reports          investigate.ReportRules
	GoodwillDays     int           // free days of the plan offered once to keep a person; 0 turns the offer off
	DuplicateWindow  time.Duration // two equal charges this close together look like a double charge
}

var DefaultConfig = Config{
	PremoderationSLA: 48 * time.Hour,
	TrustThreshold:   trustedAfter,
	Reports:          investigate.DefaultReportRules,
	GoodwillDays:     7,
	DuplicateWindow:  72 * time.Hour,
}

// Decide picks the rule for one classified message.
func Decide(c domain.Classification, f domain.Facts, cfg Config) domain.Decision {
	// An appeal in progress continues as an appeal, unless the person is closing the chat.
	if f.State.Flow == domain.FlowAppeal && f.Idea != nil && c.Intent != domain.IntentThanksResolved {
		return appeal(f, cfg)
	}
	// A retention conversation goes on until the person stays, asks a question or reaches a manager.
	if f.State.Flow == domain.FlowRetention && retentionHandles(c.Intent) {
		return retentionFlow(c, f, cfg)
	}
	switch c.Intent {
	case domain.IntentIdeaVisibility:
		return visibility(f, cfg)
	case domain.IntentModerationAppeal:
		return appeal(f, cfg)
	case domain.IntentVerificationSeal:
		return seal(f)
	case domain.IntentLoginProblem:
		return login(c)
	case domain.IntentNotificationsIssue:
		return push(f)
	case domain.IntentHowTo:
		return howTo(f)
	case domain.IntentRoleRequest:
		return answer("R1_roles_not_granted_by_support", "role.policy", nil)
	case domain.IntentReportContent:
		return answer("C1_how_to_report", "report.how", nil)
	case domain.IntentBugReport:
		d := answer("B1_bug_logged", "bug.thanks", nil)
		d.Actions = []domain.Action{{Type: domain.ActBugReport, Queue: domain.QueueAdmins, Note: "користувач повідомив про помилку"}}
		return d
	case domain.IntentThanksResolved:
		return answer("T1_thanks", "thanks.close", nil)
	case domain.IntentBillingCancel, domain.IntentBillingRefund:
		return retention(c, f, cfg)
	case domain.IntentBillingQuestion:
		return billingQuestion(f)
	default:
		// account_data, human_request, legal, offensive, unknown: guard decides how and to whom.
		return handoff("P0_operations_intent", "escalation.generic", "інтент "+string(c.Intent)+" обробляє guard", domain.QueueAdmins, nil)
	}
}

func visibility(f domain.Facts, cfg Config) domain.Decision {
	if f.User == nil {
		return loginNeeded()
	}
	idea := f.Idea
	if idea == nil {
		return ask("V1_which_idea", "visibility.which_idea", nil)
	}
	if idea.Status == domain.IdeaDraft {
		d := answer("V7_draft", "visibility.draft", titleSlot(idea))
		d.Suggested = openIdea(idea)
		return d
	}
	switch idea.Moderation {
	case domain.ModerationPending:
		return premoderation(f, cfg)
	case domain.ModerationHidden:
		return appeal(f, cfg)
	case domain.ModerationRejected:
		note := idea.DecisionNote
		if note == "" {
			note = "без коментаря"
		}
		d := ask("V4_rejected", "visibility.rejected", map[string]string{"title": idea.Title, "note": note})
		d.Verdict = &domain.Verdict{Kind: domain.VerdictNotABug, Evidence: []string{"ідею відхилив модератор"}, Explanation: "Ідею відхилено рішенням модератора."}
		d.NextState = domain.ConversationState{Flow: domain.FlowAppeal, Step: 1}
		return d
	}
	if idea.Visibility != domain.VisibilityPublic {
		d := answer("V5_restricted_visibility", "visibility.restricted", map[string]string{"title": idea.Title, "visibility": idea.Visibility})
		d.Verdict = &domain.Verdict{Kind: domain.VerdictNotABug, Evidence: []string{"видимість " + idea.Visibility}, Explanation: "Ідея опублікована з обмеженою видимістю."}
		d.Suggested = openIdea(idea)
		return d
	}
	d := answer("V6_live", "visibility.live", titleSlot(idea))
	d.Suggested = openIdea(idea)
	return d
}

func premoderation(f domain.Facts, cfg Config) domain.Decision {
	idea := f.Idea
	since := idea.CaseOpenedAt
	if since.IsZero() {
		since = idea.CreatedAt
	}
	waited := max(f.Now.Sub(since), 0)
	approved := 0
	if f.User != nil {
		approved = f.User.ApprovedIdeas
	}
	slots := map[string]string{
		"title":       idea.Title,
		"waitedHours": strconv.Itoa(int(waited.Hours())),
		"approved":    strconv.Itoa(approved),
		"threshold":   strconv.Itoa(cfg.TrustThreshold),
	}
	verdict := &domain.Verdict{
		Kind:        domain.VerdictNotABug,
		Evidence:    []string{fmt.Sprintf("ідея на премодерації %d год", int(waited.Hours())), fmt.Sprintf("у автора %d схвалених ідей із %d потрібних", approved, cfg.TrustThreshold)},
		Explanation: "Ідеї нових авторів спершу переглядає модератор.",
	}
	if waited > cfg.PremoderationSLA {
		d := answer("V3_premoderation_overdue", "visibility.premoderation_overdue", slots)
		d.Verdict = verdict
		d.Actions = []domain.Action{{Type: domain.ActNotifyStaff, IdeaID: idea.ID, Queue: domain.QueueModerators,
			Note: fmt.Sprintf("ідея «%s» чекає на премодерацію %d год", idea.Title, int(waited.Hours()))}}
		d.Suggested = openIdea(idea)
		return d
	}
	d := answer("V2_premoderation", "visibility.premoderation", slots)
	d.Verdict = verdict
	d.Suggested = openIdea(idea)
	return d
}

func appeal(f domain.Facts, cfg Config) domain.Decision {
	if f.User == nil {
		return loginNeeded()
	}
	idea := f.Idea
	if idea == nil {
		return ask("V1_which_idea", "visibility.which_idea", nil)
	}
	switch idea.Moderation {
	case domain.ModerationHidden, domain.ModerationRejected:
	case domain.ModerationApproved:
		d := answer("A0_not_hidden", "appeal.not_hidden", titleSlot(idea))
		d.Suggested = openIdea(idea)
		return d
	default:
		// A pending idea is not hidden, it is waiting: explain that instead.
		return visibility(f, cfg)
	}
	// The bot never overrides a moderator.
	if idea.DecidedByHuman {
		return handoff("A1_human_decision", "appeal.queued", fmt.Sprintf("оскарження рішення модератора щодо «%s»", idea.Title), domain.QueueModerators, titleSlot(idea))
	}
	if idea.CaseSource != domain.CaseReports {
		return handoff("A4_unknown_case", "appeal.queued", fmt.Sprintf("ідея «%s» прихована без кейсу зі скарг", idea.Title), domain.QueueModerators, titleSlot(idea))
	}
	v := investigate.Reports(f.Reports, cfg.Reports)
	if v.Kind == domain.VerdictSuspectedAbuse {
		return domain.Decision{
			Autonomy: domain.AutonomyApprove,
			Rule:     "A2_suspected_report_abuse",
			Verdict:  &v,
			Actions: []domain.Action{{Type: domain.ActRestoreIdea, IdeaID: idea.ID, Queue: domain.QueueModerators,
				Note: "скарги схожі на скоординовані: " + strings.Join(v.Evidence, "; ")}},
			TemplateID: "appeal.sent_to_moderator",
			Slots:      titleSlot(idea),
			Status:     domain.StatusAwaitingApproval,
			Escalation: &domain.Escalation{Reason: "approval_needed", Queue: domain.QueueModerators, Priority: 2,
				Note: fmt.Sprintf("пропоную відновити «%s»: %s", idea.Title, strings.Join(v.Evidence, "; "))},
		}
	}
	d := handoff("A3_reports_need_review", "appeal.queued", fmt.Sprintf("скарги на «%s» потребують перевірки людиною: %s", idea.Title, strings.Join(v.Evidence, "; ")), domain.QueueModerators, titleSlot(idea))
	d.Verdict = &v
	return d
}

func seal(f domain.Facts) domain.Decision {
	u := f.User
	if u == nil {
		return loginNeeded()
	}
	switch {
	case u.Verified:
		return answer("S1_already_verified", "seal.already", nil)
	case u.Provider != domain.ProviderEntra:
		d := answer("S2_use_microsoft", "seal.use_microsoft", nil)
		d.Verdict = &domain.Verdict{Kind: domain.VerdictUserSide, Evidence: []string{"вхід через " + u.Provider + ", Microsoft-входу немає"},
			Explanation: "Печатку дає лише університетський акаунт Microsoft; вхід через Microsoft створить окремий профіль."}
		d.Suggested = []domain.SuggestedAction{{Label: "Увійти через Microsoft", To: "/login"}}
		return d
	case !u.TenantMatches || !u.UKMAEmail:
		d := answer("S3_email_domain", "seal.email_domain", nil)
		d.Verdict = &domain.Verdict{Kind: domain.VerdictUserSide,
			Evidence:    []string{fmt.Sprintf("тенант НаУКМА: %s, пошта ukma.edu.ua: %s", yesNo(u.TenantMatches), yesNo(u.UKMAEmail))},
			Explanation: "Вхід через Microsoft, але не університетським акаунтом."}
		d.Suggested = []domain.SuggestedAction{{Label: "Увійти через Microsoft", To: "/login"}}
		return d
	default:
		d := answer("S4_seal_inconsistent", "seal.investigating", nil)
		d.Verdict = &domain.Verdict{Kind: domain.VerdictBug, Evidence: []string{"є вхід Microsoft з тенанта НаУКМА й підтверджена пошта ukma.edu.ua, але печатки немає"},
			Explanation: "Усі умови печатки виконані, а печатки немає: схоже на помилку платформи."}
		d.Actions = []domain.Action{{Type: domain.ActBugReport, Queue: domain.QueueAdmins, Note: "печатка не видана при виконаних умовах", Params: map[string]string{"area": "seal"}}}
		return d
	}
}

func login(c domain.Classification) domain.Decision {
	if c.ErrorCode == "" {
		return answer("L0_login_checklist", "login.checklist", nil)
	}
	d := answer("L1_login_error_code", "login.known_error", map[string]string{"code": c.ErrorCode})
	d.Verdict = &domain.Verdict{Kind: domain.VerdictBug, Evidence: []string{"код помилки входу " + c.ErrorCode}, Explanation: "Вхід повертає код помилки: команда має перевірити налаштування входу."}
	d.Actions = []domain.Action{{Type: domain.ActBugReport, Queue: domain.QueueAdmins, Note: "помилка входу " + c.ErrorCode, Params: map[string]string{"area": "login", "errorCode": c.ErrorCode}}}
	return d
}

func push(f domain.Facts) domain.Decision {
	if f.User == nil {
		return loginNeeded()
	}
	switch {
	case f.Push == nil || f.Push.Subscriptions == 0:
		d := answer("N1_push_not_enabled", "push.enable", nil)
		d.Verdict = &domain.Verdict{Kind: domain.VerdictUserSide, Evidence: []string{"0 підписок на push"}, Explanation: "Push не ввімкнено на жодному пристрої."}
		d.Suggested = []domain.SuggestedAction{{Label: "Відкрити налаштування", To: "/settings"}}
		return d
	case f.Push.Stuck > 0:
		d := answer("N2_push_delivery_stuck", "push.investigating", nil)
		d.Verdict = &domain.Verdict{Kind: domain.VerdictBug, Evidence: []string{fmt.Sprintf("%d сповіщень чекають на push понад 10 хв", f.Push.Stuck)}, Explanation: "Підписка є, але доставка затримується."}
		d.Actions = []domain.Action{{Type: domain.ActBugReport, Queue: domain.QueueAdmins, Note: "push не доставляється",
			Params: map[string]string{"area": "push", "stuck": strconv.Itoa(f.Push.Stuck)}}}
		return d
	default:
		return answer("N3_push_checklist", "push.checklist", nil)
	}
}

func howTo(f domain.Facts) domain.Decision {
	if len(f.Knowledge) == 0 {
		// Without a source the bot does not invent an answer.
		return handoff("H2_no_knowledge", "howto.unknown", "у базі знань немає відповіді", domain.QueueModerators, nil)
	}
	hit := f.Knowledge[0]
	d := answer("H1_knowledge_base", "howto.answer", map[string]string{"guide": hit.Title, "summary": hit.Snippet})
	d.Suggested = []domain.SuggestedAction{{Label: "Відкрити гайд", To: hit.Path}}
	return d
}

func loginNeeded() domain.Decision {
	d := ask("V0_login_needed", "account.login_needed", nil)
	d.Suggested = []domain.SuggestedAction{{Label: "Увійти", To: "/login"}}
	return d
}

// answer is a complete reply the bot gives on its own.
func answer(rule, template string, slots map[string]string) domain.Decision {
	return domain.Decision{Autonomy: domain.AutonomyAuto, Rule: rule, TemplateID: template, Slots: slots, Status: domain.StatusSolved}
}

// ask is a reply that needs something from the person before the bot can go on.
func ask(rule, template string, slots map[string]string) domain.Decision {
	d := answer(rule, template, slots)
	d.Status = domain.StatusAwaitingCustomer
	return d
}

// handoff gives the conversation to a person, with a note so nobody asks twice.
func handoff(rule, template, note string, q domain.Queue, slots map[string]string) domain.Decision {
	return domain.Decision{
		Autonomy: domain.AutonomyEscalate, Rule: rule, TemplateID: template, Slots: slots, Status: domain.StatusEscalated,
		Escalation: &domain.Escalation{Reason: "policy_handoff", Queue: q, Priority: 3, Note: note},
	}
}

func titleSlot(idea *domain.IdeaFacts) map[string]string {
	return map[string]string{"title": idea.Title}
}

func openIdea(idea *domain.IdeaFacts) []domain.SuggestedAction {
	return []domain.SuggestedAction{{Label: "Відкрити ідею", To: "/ideas/" + idea.Slug}}
}

func yesNo(b bool) string {
	if b {
		return "так"
	}
	return "ні"
}
