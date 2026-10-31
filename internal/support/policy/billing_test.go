package policy_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/handziurdmytro/skelar/internal/support/domain"
	"github.com/handziurdmytro/skelar/internal/support/policy"
	fx "github.com/handziurdmytro/skelar/internal/support/supporttest"
)

func billingFacts(b *domain.BillingFacts, step int) domain.Facts {
	f := domain.Facts{Now: fx.Now, User: fx.GoogleNewcomer(), Billing: b}
	if step > 0 {
		f.State = domain.ConversationState{Flow: domain.FlowRetention, Step: step}
	}
	return f
}

func TestRetentionLadder(t *testing.T) {
	cancel, refund := intent(domain.IntentBillingCancel), intent(domain.IntentBillingRefund)
	anonymous := billingFacts(nil, 0)
	anonymous.User = nil

	cases := []struct {
		name     string
		class    domain.Classification
		facts    domain.Facts
		rule     string
		autonomy domain.Autonomy
		queue    domain.Queue
		next     domain.ConversationState
	}{
		{"first a cheaper plan that still holds every idea", cancel, billingFacts(fx.ProAuthor(4), 0),
			"M2_offer_cheaper_plan", domain.AutonomyAuto, "", domain.ConversationState{Flow: domain.FlowRetention, Step: 1}},
		{"no cheaper plan fits, so free days come first", cancel, billingFacts(fx.ProAuthor(20), 0),
			"M3_goodwill_days", domain.AutonomyAuto, "", domain.ConversationState{Flow: domain.FlowRetention, Step: 2}},
		{"the cheapest paid plan goes straight to free days", cancel, billingFacts(fx.PlusAuthor(), 0),
			"M3_goodwill_days", domain.AutonomyAuto, "", domain.ConversationState{Flow: domain.FlowRetention, Step: 2}},
		{"a refusal of the cheaper plan gets free days", cancel, billingFacts(fx.ProAuthor(4), 1),
			"M3_goodwill_days", domain.AutonomyAuto, "", domain.ConversationState{Flow: domain.FlowRetention, Step: 2}},
		{"a refusal of free days reaches a manager", cancel, billingFacts(fx.ProAuthor(4), 2),
			"M4_insists", domain.AutonomyEscalate, domain.QueueSupport, domain.ConversationState{}},
		{"free days are given once per person", cancel, billingFacts(fx.GoodwillGiven(), 0),
			"M4_insists", domain.AutonomyEscalate, domain.QueueSupport, domain.ConversationState{}},
		{"an unclear reply to an offer is still a refusal", intent(domain.IntentUnknown), billingFacts(fx.ProAuthor(4), 1),
			"M3_goodwill_days", domain.AutonomyAuto, "", domain.ConversationState{Flow: domain.FlowRetention, Step: 2}},
		{"a refund request walks the same ladder", refund, billingFacts(fx.ProAuthor(4), 0),
			"M2_offer_cheaper_plan", domain.AutonomyAuto, "", domain.ConversationState{Flow: domain.FlowRetention, Step: 1}},
		{"a sponsor is offered a lower level", cancel, billingFacts(fx.Sponsor(), 0),
			"M2_offer_lower_tier", domain.AutonomyAuto, "", domain.ConversationState{Flow: domain.FlowRetention, Step: 1}},
		{"a sponsor who refuses the lower level reaches a manager", cancel, billingFacts(fx.Sponsor(), 1),
			"M4_insists", domain.AutonomyEscalate, domain.QueueSupport, domain.ConversationState{}},
		{"a double charge is our mistake: no talking out of it", refund, billingFacts(fx.DoubleCharged(), 0),
			"M5_duplicate_charge", domain.AutonomyEscalate, domain.QueueSupport, domain.ConversationState{}},
		{"a refund with nothing renewing goes to a manager", refund, billingFacts(fx.EndingPlan(), 0),
			"M6_refund_requested", domain.AutonomyEscalate, domain.QueueSupport, domain.ConversationState{}},
		{"cancelling a plan that already ends says so", cancel, billingFacts(fx.EndingPlan(), 0),
			"M1_plan_already_ending", domain.AutonomyAuto, "", domain.ConversationState{}},
		{"cancelling with nothing paid says nothing will be charged", cancel, billingFacts(fx.FreeAuthor(), 0),
			"M1_nothing_paid", domain.AutonomyAuto, "", domain.ConversationState{}},
		{"an anonymous visitor is asked to sign in", cancel, anonymous,
			"M0_login_needed", domain.AutonomyAuto, "", domain.ConversationState{}},
		{"billing that did not load goes to a manager", cancel, billingFacts(nil, 0),
			"M0_billing_unavailable", domain.AutonomyEscalate, domain.QueueSupport, domain.ConversationState{}},
		{"agreeing to an offer ends retention", intent(domain.IntentThanksResolved), billingFacts(fx.ProAuthor(4), 1),
			"M7_offer_accepted", domain.AutonomyAuto, "", domain.ConversationState{}},
		{"asking for a person mid-retention reaches a manager with the note", intent(domain.IntentHumanRequest), billingFacts(fx.ProAuthor(4), 1),
			"M4_human_requested", domain.AutonomyEscalate, domain.QueueSupport, domain.ConversationState{}},
		{"a price question mid-retention keeps the conversation where it was", intent(domain.IntentBillingQuestion), billingFacts(fx.ProAuthor(4), 1),
			"M8_plans_cheaper_fits", domain.AutonomyAuto, "", domain.ConversationState{Flow: domain.FlowRetention, Step: 1}},
		{"a price question names the cheaper plan that fits", intent(domain.IntentBillingQuestion), billingFacts(fx.ProAuthor(4), 0),
			"M8_plans_cheaper_fits", domain.AutonomyAuto, "", domain.ConversationState{}},
		{"a price question without billing answers in general", intent(domain.IntentBillingQuestion), anonymous,
			"M8_plans_general", domain.AutonomyAuto, "", domain.ConversationState{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := policy.Decide(tc.class, tc.facts, policy.DefaultConfig)
			if d.Rule != tc.rule || d.Autonomy != tc.autonomy {
				t.Fatalf("got %s/%s, want %s/%s", d.Rule, d.Autonomy, tc.rule, tc.autonomy)
			}
			var queue domain.Queue
			if d.Escalation != nil {
				queue = d.Escalation.Queue
			}
			if queue != tc.queue {
				t.Fatalf("queue = %q, want %q", queue, tc.queue)
			}
			if d.NextState != tc.next {
				t.Fatalf("next state = %+v, want %+v", d.NextState, tc.next)
			}
		})
	}
}

func TestCheaperPlanOfferCarriesTheNumbers(t *testing.T) {
	d := policy.Decide(intent(domain.IntentBillingCancel), billingFacts(fx.ProAuthor(4), 0), policy.DefaultConfig)
	for slot, want := range map[string]string{"plan": "Про", "offer": "Плюс", "price": "399 ₴", "offerPrice": "149 ₴",
		"saving": "250 ₴", "limit": "до 15 активних ідей", "ideas": "4", "until": "12 листопада"} {
		if d.Slots[slot] != want {
			t.Errorf("slot %s = %q, want %q", slot, d.Slots[slot], want)
		}
	}
	if len(d.Suggested) != 1 || d.Suggested[0].To != "/pricing" {
		t.Fatalf("suggested = %+v", d.Suggested)
	}
}

func TestGoodwillIsOneSmallExtension(t *testing.T) {
	d := policy.Decide(intent(domain.IntentBillingCancel), billingFacts(fx.ProAuthor(20), 0), policy.DefaultConfig)
	if len(d.Actions) != 1 {
		t.Fatalf("actions = %+v", d.Actions)
	}
	a := d.Actions[0]
	if a.Type != domain.ActExtendPlan || a.Params["days"] != "7" || a.Params["handle"] != "olena" || a.Queue != domain.QueueSupport {
		t.Fatalf("action = %+v", a)
	}
	if d.Slots["until"] != "19 листопада" {
		t.Fatalf("until = %q, want the period end plus 7 days", d.Slots["until"])
	}
	off := policy.DefaultConfig
	off.GoodwillDays = 0
	if d := policy.Decide(intent(domain.IntentBillingCancel), billingFacts(fx.ProAuthor(20), 0), off); d.Rule != "M4_insists" {
		t.Fatalf("with free days off the next step is a manager, got %s", d.Rule)
	}
}

func TestHandoffNoteTellsTheManagerEverything(t *testing.T) {
	b := fx.ProAuthor(4)
	d := policy.Decide(intent(domain.IntentBillingCancel), billingFacts(b, 2), policy.DefaultConfig)
	note := d.Escalation.Note
	for _, want := range []string{"«Про» 399 ₴/міс", "оплачено до 12 листопада", "активних ідей 4", "Плюс (−250 ₴/міс)", "7 днів безкоштовно",
		"платежів, які ще можна повернути, немає"} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q: %s", want, note)
		}
	}
	b.Payments[0].CreatedAt = fx.Now.Add(-3 * 24 * time.Hour)
	d = policy.Decide(intent(domain.IntentBillingRefund), billingFacts(b, 2), policy.DefaultConfig)
	if !strings.Contains(d.Escalation.Note, "можна повернути (до 14 днів): 399 ₴ від 28 жовтня «План «Про»» (pay-1)") {
		t.Errorf("a fresh payment must be offered for refund: %s", d.Escalation.Note)
	}
	dup := policy.Decide(intent(domain.IntentBillingRefund), billingFacts(fx.DoubleCharged(), 0), policy.DefaultConfig)
	if !strings.Contains(dup.Escalation.Note, "pay-2") || dup.Escalation.Priority != 2 {
		t.Fatalf("duplicate note = %+v", dup.Escalation)
	}
}

// The bot keeps people, it never lets them go by itself: over the whole grid the
// only billing action is free days, and nothing escalated to a manager runs one.
func TestBotNeverCancelsOrRefunds(t *testing.T) {
	allowed := []domain.ActionType{domain.ActRestoreIdea, domain.ActNotifyStaff, domain.ActBugReport, domain.ActDataRequest, domain.ActExtendPlan}
	for _, c := range fx.Grid() {
		d := policy.Decide(c.Class, c.Facts, policy.DefaultConfig)
		for _, a := range d.Actions {
			if !slices.Contains(allowed, a.Type) {
				t.Fatalf("%s: unknown action %s", c.Name, a.Type)
			}
			if a.Type == domain.ActExtendPlan && (c.Facts.Billing == nil || c.Facts.Billing.GoodwillUsed || a.Params["days"] != "7") {
				t.Fatalf("%s: free days without a fresh paid plan: %+v", c.Name, a)
			}
		}
		if d.Escalation != nil && d.Escalation.Queue == domain.QueueSupport && d.Escalation.Note == "" {
			t.Fatalf("%s: a manager gets the case without a note (rule %s)", c.Name, d.Rule)
		}
	}
}
