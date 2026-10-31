package policy_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/handziurdmytro/skelar/internal/support/domain"
	"github.com/handziurdmytro/skelar/internal/support/policy"
	fx "github.com/handziurdmytro/skelar/internal/support/supporttest"
)

func intent(i domain.Intent) domain.Classification {
	return domain.Classification{Intent: i, Emotion: domain.EmotionNeutral, Confidence: 0.9}
}

func facts(user *domain.UserFacts, idea *domain.IdeaFacts) domain.Facts {
	return domain.Facts{Now: fx.Now, User: user, Idea: idea}
}

func actionTypes(d domain.Decision) []domain.ActionType {
	var out []domain.ActionType
	for _, a := range d.Actions {
		out = append(out, a.Type)
	}
	return out
}

func TestDecide(t *testing.T) {
	appealFlow := domain.ConversationState{Flow: domain.FlowAppeal, Step: 1}
	withReports := func(f domain.Facts, r []domain.ReportFacts) domain.Facts { f.Reports = r; return f }
	withState := func(f domain.Facts, s domain.ConversationState) domain.Facts { f.State = s; return f }
	withPush := func(p *domain.PushFacts) domain.Facts { f := facts(fx.GoogleNewcomer(), nil); f.Push = p; return f }
	withKnowledge := func(hits ...domain.KBHit) domain.Facts { f := facts(nil, nil); f.Knowledge = hits; return f }
	loginError := intent(domain.IntentLoginProblem)
	loginError.ErrorCode = "AADSTS50020"

	cases := []struct {
		name     string
		class    domain.Classification
		facts    domain.Facts
		rule     string
		autonomy domain.Autonomy
		status   domain.TicketStatus
		actions  []domain.ActionType
		verdict  domain.VerdictKind
	}{
		// Idea visibility (S1).
		{"S1: a pending idea is not a bug and needs no human", intent(domain.IntentIdeaVisibility), facts(fx.GoogleNewcomer(), fx.PendingIdea(3*time.Hour)),
			"V2_premoderation", domain.AutonomyAuto, domain.StatusSolved, nil, domain.VerdictNotABug},
		{"a pending idea past the 48 h SLA pings the moderators", intent(domain.IntentIdeaVisibility), facts(fx.GoogleNewcomer(), fx.PendingIdea(72*time.Hour)),
			"V3_premoderation_overdue", domain.AutonomyAuto, domain.StatusSolved, []domain.ActionType{domain.ActNotifyStaff}, domain.VerdictNotABug},
		{"an anonymous visitor is asked to sign in first", intent(domain.IntentIdeaVisibility), facts(nil, nil),
			"V0_login_needed", domain.AutonomyAuto, domain.StatusAwaitingCustomer, nil, ""},
		{"without a matching idea the bot asks which one", intent(domain.IntentIdeaVisibility), facts(fx.GoogleNewcomer(), nil),
			"V1_which_idea", domain.AutonomyAuto, domain.StatusAwaitingCustomer, nil, ""},
		{"a draft is only visible to its author", intent(domain.IntentIdeaVisibility), facts(fx.GoogleNewcomer(), fx.DraftIdea()),
			"V7_draft", domain.AutonomyAuto, domain.StatusSolved, nil, ""},
		{"a rejected idea explains the decision and offers an appeal", intent(domain.IntentIdeaVisibility), facts(fx.GoogleNewcomer(), fx.RejectedIdea()),
			"V4_rejected", domain.AutonomyAuto, domain.StatusAwaitingCustomer, nil, domain.VerdictNotABug},
		{"a members-only idea is not a bug", intent(domain.IntentIdeaVisibility), facts(fx.GoogleNewcomer(), fx.LiveIdea(domain.VisibilityMembers)),
			"V5_restricted_visibility", domain.AutonomyAuto, domain.StatusSolved, nil, domain.VerdictNotABug},
		{"a public approved idea is live", intent(domain.IntentIdeaVisibility), facts(fx.GoogleNewcomer(), fx.LiveIdea(domain.VisibilityPublic)),
			"V6_live", domain.AutonomyAuto, domain.StatusSolved, nil, ""},
		{"asking where a hidden idea went opens the appeal", intent(domain.IntentIdeaVisibility), withReports(facts(fx.GoogleNewcomer(), fx.HiddenByReports()), fx.BrigadeReports()),
			"A2_suspected_report_abuse", domain.AutonomyApprove, domain.StatusAwaitingApproval, []domain.ActionType{domain.ActRestoreIdea}, domain.VerdictSuspectedAbuse},

		// Appeals (S2).
		{"S2: coordinated reports — propose restoring, a moderator decides", intent(domain.IntentModerationAppeal), withReports(facts(fx.GoogleNewcomer(), fx.HiddenByReports()), fx.BrigadeReports()),
			"A2_suspected_report_abuse", domain.AutonomyApprove, domain.StatusAwaitingApproval, []domain.ActionType{domain.ActRestoreIdea}, domain.VerdictSuspectedAbuse},
		{"the bot never overrides a moderator", intent(domain.IntentModerationAppeal), withReports(facts(fx.GoogleNewcomer(), fx.HiddenByModerator()), fx.BrigadeReports()),
			"A1_human_decision", domain.AutonomyEscalate, domain.StatusEscalated, nil, ""},
		{"honest reports go to a moderator with the verdict", intent(domain.IntentModerationAppeal), withReports(facts(fx.GoogleNewcomer(), fx.HiddenByReports()), fx.HonestReports()),
			"A3_reports_need_review", domain.AutonomyEscalate, domain.StatusEscalated, nil, domain.VerdictUnclear},
		{"a hidden idea without a reports case is a person's call", intent(domain.IntentModerationAppeal), facts(fx.GoogleNewcomer(), fx.HiddenWithoutReportsCase()),
			"A4_unknown_case", domain.AutonomyEscalate, domain.StatusEscalated, nil, ""},
		{"appealing a live idea says it is not hidden", intent(domain.IntentModerationAppeal), facts(fx.GoogleNewcomer(), fx.LiveIdea(domain.VisibilityPublic)),
			"A0_not_hidden", domain.AutonomyAuto, domain.StatusSolved, nil, ""},
		{"appealing a pending idea explains premoderation", intent(domain.IntentModerationAppeal), facts(fx.GoogleNewcomer(), fx.PendingIdea(3*time.Hour)),
			"V2_premoderation", domain.AutonomyAuto, domain.StatusSolved, nil, domain.VerdictNotABug},
		{"an appeal from an anonymous visitor asks to sign in", intent(domain.IntentModerationAppeal), facts(nil, nil),
			"V0_login_needed", domain.AutonomyAuto, domain.StatusAwaitingCustomer, nil, ""},
		{"a reply inside the appeal flow continues the appeal", intent(domain.IntentUnknown), withState(facts(fx.GoogleNewcomer(), fx.RejectedIdea()), appealFlow),
			"A1_human_decision", domain.AutonomyEscalate, domain.StatusEscalated, nil, ""},
		{"thanks ends an appeal conversation", intent(domain.IntentThanksResolved), withState(facts(fx.GoogleNewcomer(), fx.RejectedIdea()), appealFlow),
			"T1_thanks", domain.AutonomyAuto, domain.StatusSolved, nil, ""},

		// Seal (S4).
		{"S4: a Google sign-in needs Microsoft for the seal", intent(domain.IntentVerificationSeal), facts(fx.GoogleNewcomer(), nil),
			"S2_use_microsoft", domain.AutonomyAuto, domain.StatusSolved, nil, domain.VerdictUserSide},
		{"a verified member already has the seal", intent(domain.IntentVerificationSeal), facts(fx.VerifiedMember(), nil),
			"S1_already_verified", domain.AutonomyAuto, domain.StatusSolved, nil, ""},
		{"Microsoft from another tenant does not give the seal", intent(domain.IntentVerificationSeal), facts(fx.EntraOtherTenant(), nil),
			"S3_email_domain", domain.AutonomyAuto, domain.StatusSolved, nil, domain.VerdictUserSide},
		{"every seal signal without a seal is a bug for admins", intent(domain.IntentVerificationSeal), facts(fx.EntraUnverified(), nil),
			"S4_seal_inconsistent", domain.AutonomyAuto, domain.StatusSolved, []domain.ActionType{domain.ActBugReport}, domain.VerdictBug},

		// Knowledge (S5).
		{"S5: a how-to with a guide answers from it", intent(domain.IntentHowTo), withKnowledge(fx.TeamGuide()),
			"H1_knowledge_base", domain.AutonomyAuto, domain.StatusSolved, nil, ""},
		{"a how-to without a source is escalated, never invented", intent(domain.IntentHowTo), withKnowledge(),
			"H2_no_knowledge", domain.AutonomyEscalate, domain.StatusEscalated, nil, ""},

		// Login (S6) works for anonymous visitors.
		{"S6: a login error code is logged as a bug", loginError, facts(nil, nil),
			"L1_login_error_code", domain.AutonomyAuto, domain.StatusSolved, []domain.ActionType{domain.ActBugReport}, domain.VerdictBug},
		{"a login problem without a code gets a checklist", intent(domain.IntentLoginProblem), facts(nil, nil),
			"L0_login_checklist", domain.AutonomyAuto, domain.StatusSolved, nil, ""},

		// Push (S7).
		{"S7: no push subscription means push is off", intent(domain.IntentNotificationsIssue), withPush(&domain.PushFacts{}),
			"N1_push_not_enabled", domain.AutonomyAuto, domain.StatusSolved, nil, domain.VerdictUserSide},
		{"S7: stuck deliveries are a bug", intent(domain.IntentNotificationsIssue), withPush(&domain.PushFacts{Subscriptions: 1, Stuck: 3}),
			"N2_push_delivery_stuck", domain.AutonomyAuto, domain.StatusSolved, []domain.ActionType{domain.ActBugReport}, domain.VerdictBug},
		{"S7: healthy push gets a device checklist", intent(domain.IntentNotificationsIssue), withPush(&domain.PushFacts{Subscriptions: 2}),
			"N3_push_checklist", domain.AutonomyAuto, domain.StatusSolved, nil, ""},

		// The rest.
		{"S3: roles are never granted by support", intent(domain.IntentRoleRequest), facts(fx.GoogleNewcomer(), nil),
			"R1_roles_not_granted_by_support", domain.AutonomyAuto, domain.StatusSolved, nil, ""},
		{"reporting someone else's idea is explained", intent(domain.IntentReportContent), facts(fx.GoogleNewcomer(), nil),
			"C1_how_to_report", domain.AutonomyAuto, domain.StatusSolved, nil, ""},
		{"a bug report is passed to the team", intent(domain.IntentBugReport), facts(nil, nil),
			"B1_bug_logged", domain.AutonomyAuto, domain.StatusSolved, []domain.ActionType{domain.ActBugReport}, ""},
		{"thanks closes the ticket", intent(domain.IntentThanksResolved), facts(fx.GoogleNewcomer(), nil),
			"T1_thanks", domain.AutonomyAuto, domain.StatusSolved, nil, ""},
	}
	for _, intent := range []domain.Intent{domain.IntentAccountData, domain.IntentHumanRequest, domain.IntentLegal, domain.IntentOffensive, domain.IntentUnknown} {
		cases = append(cases, struct {
			name     string
			class    domain.Classification
			facts    domain.Facts
			rule     string
			autonomy domain.Autonomy
			status   domain.TicketStatus
			actions  []domain.ActionType
			verdict  domain.VerdictKind
		}{"operations intent " + string(intent) + " is handed to guard", domain.Classification{Intent: intent}, facts(fx.GoogleNewcomer(), nil),
			"P0_operations_intent", domain.AutonomyEscalate, domain.StatusEscalated, nil, ""})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := policy.Decide(tc.class, tc.facts, policy.DefaultConfig)
			if d.Rule != tc.rule || d.Autonomy != tc.autonomy || d.Status != tc.status {
				t.Fatalf("got %s/%s/%s, want %s/%s/%s", d.Rule, d.Autonomy, d.Status, tc.rule, tc.autonomy, tc.status)
			}
			if got := actionTypes(d); !slices.Equal(got, tc.actions) {
				t.Fatalf("actions = %v, want %v", got, tc.actions)
			}
			var verdict domain.VerdictKind
			if d.Verdict != nil {
				verdict = d.Verdict.Kind
			}
			if verdict != tc.verdict {
				t.Fatalf("verdict = %q, want %q", verdict, tc.verdict)
			}
		})
	}
}

func TestPremoderationSlotsCarryTheNumbers(t *testing.T) {
	d := policy.Decide(intent(domain.IntentIdeaVisibility), facts(fx.GoogleNewcomer(), fx.PendingIdea(3*time.Hour)), policy.DefaultConfig)
	want := map[string]string{"title": "Кавовий автомат у бібліотеці", "waitedHours": "3", "approved": "0", "threshold": "3"}
	if !reflect.DeepEqual(d.Slots, want) {
		t.Fatalf("slots = %v, want %v", d.Slots, want)
	}
	if len(d.Suggested) != 1 || d.Suggested[0].To != "/ideas/kavovyi-avtomat" {
		t.Fatalf("suggested = %v", d.Suggested)
	}
}

func TestSuspectedAbuseCarriesEvidenceForTheModerator(t *testing.T) {
	f := facts(fx.GoogleNewcomer(), fx.HiddenByReports())
	f.Reports = fx.BrigadeReports()
	d := policy.Decide(intent(domain.IntentModerationAppeal), f, policy.DefaultConfig)
	if d.Verdict == nil || !slices.Contains(d.Verdict.Evidence, "4 з 5 скарг — акаунти, молодші за добу") {
		t.Fatalf("verdict = %+v", d.Verdict)
	}
	if d.Actions[0].IdeaID != "idea-hidden" || d.Escalation == nil || d.Escalation.Queue != domain.QueueModerators {
		t.Fatalf("restore must target the idea and reach moderators: %+v / %+v", d.Actions, d.Escalation)
	}
}

func TestRejectedIdeaStartsTheAppealFlow(t *testing.T) {
	d := policy.Decide(intent(domain.IntentIdeaVisibility), facts(fx.GoogleNewcomer(), fx.RejectedIdea()), policy.DefaultConfig)
	if d.NextState != (domain.ConversationState{Flow: domain.FlowAppeal, Step: 1}) {
		t.Fatalf("next state = %+v", d.NextState)
	}
	if d.Slots["note"] != "Комерційні оголошення не публікуємо." {
		t.Fatalf("the moderator's note must reach the reply: %v", d.Slots)
	}
}

// Invariants over the whole grid: they are the safety case, so they sweep every
// intent × person × idea × conversation state rather than a few examples.

func TestRestoreAlwaysNeedsApproval(t *testing.T) {
	for _, c := range fx.Grid() {
		d := policy.Decide(c.Class, c.Facts, policy.DefaultConfig)
		if slices.Contains(actionTypes(d), domain.ActRestoreIdea) && d.Autonomy != domain.AutonomyApprove {
			t.Fatalf("%s: restore_idea with autonomy %s (rule %s)", c.Name, d.Autonomy, d.Rule)
		}
	}
}

func TestBotNeverOverridesAModerator(t *testing.T) {
	for _, c := range fx.Grid() {
		if c.Facts.Idea == nil || !c.Facts.Idea.DecidedByHuman {
			continue
		}
		d := policy.Decide(c.Class, c.Facts, policy.DefaultConfig)
		if slices.Contains(actionTypes(d), domain.ActRestoreIdea) {
			t.Fatalf("%s: proposed restoring an idea a moderator decided on (rule %s)", c.Name, d.Rule)
		}
	}
}

func TestEveryDecisionIsComplete(t *testing.T) {
	for _, c := range fx.Grid() {
		d := policy.Decide(c.Class, c.Facts, policy.DefaultConfig)
		switch {
		case d.Rule == "" || d.TemplateID == "" || d.Status == "":
			t.Fatalf("%s: incomplete decision %+v", c.Name, d)
		case d.Autonomy == domain.AutonomyEscalate && (d.Escalation == nil || d.Escalation.Queue == ""):
			t.Fatalf("%s: escalation without a queue (rule %s)", c.Name, d.Rule)
		case d.Autonomy == domain.AutonomyEscalate && len(d.Actions) > 0:
			t.Fatalf("%s: an escalated decision must not run actions (rule %s)", c.Name, d.Rule)
		}
		for _, s := range d.Suggested {
			if !strings.HasPrefix(s.To, "/") || strings.HasPrefix(s.To, "//") || s.Label == "" {
				t.Fatalf("%s: button %+v must be a labelled internal path", c.Name, s)
			}
		}
	}
}

func TestDecideIsDeterministic(t *testing.T) {
	for _, c := range fx.Grid() {
		a := policy.Decide(c.Class, c.Facts, policy.DefaultConfig)
		b := policy.Decide(c.Class, c.Facts, policy.DefaultConfig)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%s: two calls disagree", c.Name)
		}
	}
}
