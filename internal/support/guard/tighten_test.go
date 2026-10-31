package guard

import (
	"testing"

	"github.com/handziurdmytro/skelar/internal/support/domain"
)

func TestTighten(t *testing.T) {
	base := domain.Decision{Autonomy: domain.AutonomyAuto, Rule: "V6_live", Status: domain.StatusSolved}

	tests := []struct {
		name         string
		in           Input
		wantAutonomy domain.Autonomy
		wantRule     string
	}{
		{
			"self harm always wins, P1 to admins",
			Input{Decision: base, Signals: []Signal{SignalSelfHarm}},
			domain.AutonomyEscalate, "G_self_harm",
		},
		{
			"legal signal escalates to admins",
			Input{Decision: base, Signals: []Signal{SignalLegal}},
			domain.AutonomyEscalate, "G_legal",
		},
		{
			"legal intent without signal also escalates",
			Input{Decision: base, Class: domain.Classification{Intent: domain.IntentLegal}},
			domain.AutonomyEscalate, "G_legal",
		},
		{
			"prompt injection signal",
			Input{Decision: base, Signals: []Signal{SignalInjection}},
			domain.AutonomyEscalate, "G_prompt_injection",
		},
		{
			"prompt injection attempt from classifier signals",
			Input{Decision: base, Class: domain.Classification{Signals: []string{"prompt_injection_attempt"}}},
			domain.AutonomyEscalate, "G_prompt_injection",
		},
		{
			"data request becomes approve, not escalate",
			Input{Decision: base, Signals: []Signal{SignalDataRequest}},
			domain.AutonomyApprove, "G_data_request",
		},
		{
			"card number shared",
			Input{Decision: base, PII: []string{"card"}},
			domain.AutonomyEscalate, "G_payment_data_shared",
		},
		{
			"offensive intent",
			Input{Decision: base, Class: domain.Classification{Intent: domain.IntentOffensive}},
			domain.AutonomyEscalate, "G_offensive",
		},
		{
			"human request signal",
			Input{Decision: base, Signals: []Signal{SignalHuman}},
			domain.AutonomyEscalate, "G_human_requested",
		},
		{
			"low confidence",
			Input{Decision: base, Class: domain.Classification{Confidence: 0.4}},
			domain.AutonomyEscalate, "G_low_confidence",
		},
		{
			"unknown intent always escalates even at high confidence",
			Input{Decision: base, Class: domain.Classification{Intent: domain.IntentUnknown, Confidence: 0.99}},
			domain.AutonomyEscalate, "G_low_confidence",
		},
		{
			"angry repeat contact",
			Input{Decision: base, Class: domain.Classification{Emotion: domain.EmotionAngry, Confidence: 0.9}, RecentTickets: 3},
			domain.AutonomyEscalate, "G_repeat_contact",
		},
		{
			"angry but not a repeat contact stays auto",
			Input{Decision: base, Class: domain.Classification{Emotion: domain.EmotionAngry, Confidence: 0.9}, RecentTickets: 1},
			domain.AutonomyAuto, "V6_live",
		},
		{
			"clean input passes through unchanged",
			Input{Decision: base, Class: domain.Classification{Confidence: 0.9}},
			domain.AutonomyAuto, "V6_live",
		},
		{
			"approve_all mode bumps auto to approve",
			Input{Decision: base, Class: domain.Classification{Confidence: 0.9}, Mode: ModeApproveAll},
			domain.AutonomyApprove, "V6_live",
		},
		{
			"approve_all does not relax an already-escalated decision",
			Input{
				Decision: domain.Decision{Autonomy: domain.AutonomyEscalate, Rule: "A1_human_decision", Status: domain.StatusEscalated},
				Class:    domain.Classification{Confidence: 0.9},
				Mode:     ModeApproveAll,
			},
			domain.AutonomyEscalate, "A1_human_decision",
		},
		{
			"self harm beats a concurrent legal signal (priority order, still P1)",
			Input{Decision: base, Signals: []Signal{SignalSelfHarm, SignalLegal}},
			domain.AutonomyEscalate, "G_self_harm",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Tighten(tt.in, DefaultConfig)
			if got.Autonomy != tt.wantAutonomy || got.Rule != tt.wantRule {
				t.Fatalf("Tighten() = {%s, %s}, want {%s, %s}", got.Autonomy, got.Rule, tt.wantAutonomy, tt.wantRule)
			}
		})
	}
}

// TestHighRiskActionNeverAutoExecutes is the specific guarantee a buggy or
// compromised policy must never be able to break: restore_idea and
// data_request are never auto-executed, even if the policy itself proposed
// autonomy=auto for them (which it never should, but Tighten doesn't trust
// that — it enforces the floor independently).
func TestHighRiskActionNeverAutoExecutes(t *testing.T) {
	tests := []struct {
		name   string
		action domain.ActionType
	}{
		{"restore_idea", domain.ActRestoreIdea},
		{"data_request", domain.ActDataRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := Input{
				Decision: domain.Decision{
					Autonomy: domain.AutonomyAuto, Rule: "SOME_POLICY_RULE", Status: domain.StatusSolved,
					Actions: []domain.Action{{Type: tt.action}},
				},
				Class: domain.Classification{Confidence: 0.9},
			}
			got := Tighten(in, DefaultConfig)
			if got.Autonomy == domain.AutonomyAuto {
				t.Fatalf("%s got autonomy=auto, want at least approve — a policy bug must not auto-execute this", tt.action)
			}
		})
	}
}

func TestEscalateClearsActionsAndSuggestions(t *testing.T) {
	in := Input{
		Decision: domain.Decision{
			Autonomy:  domain.AutonomyAuto,
			Rule:      "V6_live",
			Actions:   []domain.Action{{Type: domain.ActRestoreIdea}},
			Suggested: []domain.SuggestedAction{{Label: "x", To: "/y"}},
		},
		Signals: []Signal{SignalSelfHarm},
	}
	got := Tighten(in, DefaultConfig)
	if len(got.Actions) != 0 {
		t.Errorf("escalated decision kept Actions: %v", got.Actions)
	}
	if len(got.Suggested) != 0 {
		t.Errorf("escalated decision kept Suggested: %v", got.Suggested)
	}
}

// TestTightenNeverRelaxes is the property the DoD calls out explicitly:
// across every decision x signal-set x mode, Tighten must never move a
// decision closer to auto than the policy already proposed.
func TestTightenNeverRelaxes(t *testing.T) {
	autonomies := []domain.Autonomy{domain.AutonomyAuto, domain.AutonomyApprove, domain.AutonomyEscalate}
	allSignals := []Signal{SignalSelfHarm, SignalLegal, SignalDataRequest, SignalInjection, SignalHuman}
	modes := []Mode{ModeStandard, ModeApproveAll, ModeShadow}
	intents := []domain.Intent{
		domain.IntentHowTo, domain.IntentUnknown, domain.IntentLegal,
		domain.IntentOffensive, domain.IntentHumanRequest, domain.IntentAccountData,
	}
	emotions := []domain.Emotion{domain.EmotionNeutral, domain.EmotionAngry}
	confidences := []float64{0.9, 0.3}
	recentTickets := []int{0, 5}

	cases := 0
	for _, a := range autonomies {
		for mask := 0; mask < (1 << len(allSignals)); mask++ {
			var sig []Signal
			for i, s := range allSignals {
				if mask&(1<<i) != 0 {
					sig = append(sig, s)
				}
			}
			for _, mode := range modes {
				for _, intent := range intents {
					for _, emotion := range emotions {
						for _, conf := range confidences {
							for _, recent := range recentTickets {
								d := domain.Decision{Autonomy: a, Rule: "R_test", Status: domain.StatusSolved}
								cls := domain.Classification{Intent: intent, Emotion: emotion, Confidence: conf}
								out := Tighten(Input{Decision: d, Class: cls, Signals: sig, Mode: mode, RecentTickets: recent}, DefaultConfig)
								if domain.Stricter(a, out.Autonomy) != out.Autonomy {
									t.Fatalf("relaxed: in=%s signals=%v mode=%s intent=%s emotion=%s conf=%v recent=%d -> out=%s",
										a, sig, mode, intent, emotion, conf, recent, out.Autonomy)
								}
								cases++
							}
						}
					}
				}
			}
		}
	}
	t.Logf("checked %d combinations", cases)
}
