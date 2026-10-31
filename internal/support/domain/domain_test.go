package domain

import (
	"encoding/json"
	"slices"
	"testing"
)

func keys(t *testing.T, v any) []string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// These objects travel to the browser inside trace steps and messages, where the
// frontend's types read them by name. Renaming a field breaks the console, so the
// names are pinned here.
func TestWireFormatMatchesTheFrontendTypes(t *testing.T) {
	cases := []struct {
		name string
		v    any
		want []string
	}{
		{"button under a reply", SuggestedAction{Label: "Відкрити ідею", To: "/ideas/x"}, []string{"label", "to"}},
		{"classify trace data", Classification{}, []string{"confidence", "emotion", "errorCode", "ideaHint", "intent", "language", "reasoning", "signals", "userQuestions"}},
		{"investigate trace data", Verdict{Kind: VerdictBug}, []string{"evidence", "explanation", "kind"}},
		{"decide trace data", Decision{
			Autonomy: AutonomyApprove, Rule: "A2_suspected_report_abuse", Verdict: &Verdict{}, Actions: []Action{{}},
			TemplateID: "appeal.sent_to_moderator", Slots: map[string]string{"title": "x"}, Suggested: []SuggestedAction{{}},
			Status: StatusAwaitingApproval, NextState: ConversationState{Flow: FlowAppeal, Step: 1}, Escalation: &Escalation{Note: "n"},
		}, []string{"actions", "autonomy", "escalation", "nextState", "rule", "slots", "status", "suggested", "templateId", "verdict"}},
		{"escalation", Escalation{Note: "n"}, []string{"note", "priority", "queue", "reason"}},
		{"proposed action", Action{Type: ActRestoreIdea, IdeaID: "i", Queue: QueueModerators, Note: "n", Params: map[string]string{"a": "b"}}, []string{"ideaId", "note", "params", "queue", "type"}},
		{"conversation state", ConversationState{}, []string{"flow", "step"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := keys(t, tc.v); !slices.Equal(got, tc.want) {
				t.Fatalf("JSON fields = %v, want %v", got, tc.want)
			}
		})
	}
}

// The values below are strings the console switches on.
func TestWireValuesMatchTheFrontendTypes(t *testing.T) {
	if got, want := []Autonomy{AutonomyAuto, AutonomyApprove, AutonomyEscalate}, []Autonomy{"auto", "approve", "escalate"}; !slices.Equal(got, want) {
		t.Fatalf("autonomy = %v", got)
	}
	status := []TicketStatus{StatusProcessing, StatusAwaitingCustomer, StatusAwaitingApproval, StatusEscalated, StatusSolved}
	if want := []TicketStatus{"processing", "awaiting_customer", "awaiting_approval", "escalated", "solved"}; !slices.Equal(status, want) {
		t.Fatalf("status = %v", status)
	}
	kinds := []VerdictKind{VerdictNotABug, VerdictBug, VerdictSuspectedAbuse, VerdictUserSide, VerdictUnclear}
	if want := []VerdictKind{"not_a_bug", "bug", "suspected_abuse", "user_side", "unclear"}; !slices.Equal(kinds, want) {
		t.Fatalf("verdict kinds = %v", kinds)
	}
}

func TestEveryListedIntentIsValidAndNothingElse(t *testing.T) {
	seen := map[Intent]bool{}
	for _, i := range Intents {
		if !i.Valid() || seen[i] {
			t.Errorf("intent %q is invalid or listed twice", i)
		}
		seen[i] = true
	}
	for _, bad := range []Intent{"", "refund", "IDEA_VISIBILITY"} {
		if bad.Valid() {
			t.Errorf("%q must not be a valid intent", bad)
		}
	}
	if !EmotionAngry.Valid() || Emotion("furious").Valid() {
		t.Error("emotion validity is wrong")
	}
}
