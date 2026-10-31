package llm

import (
	"math"
	"slices"
	"testing"

	"github.com/handziurdmytro/skelar/internal/support/domain"
)

func TestExtractJSON(t *testing.T) {
	clean := `{"intent":"how_to"}`
	cases := map[string]string{
		clean:                         clean,
		"```json\n" + clean + "\n```": clean,
		"Here you go: " + clean + " Anything else?": clean,
		"  " + clean + "\n":                         clean,
		"no json at all":                            "no json at all",
	}
	for in, want := range cases {
		if got := extractJSON(in); got != want {
			t.Errorf("extractJSON(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeMakesModelOutputSafe(t *testing.T) {
	cases := []struct {
		name string
		in   domain.Classification
		want func(domain.Classification) bool
	}{
		{"an intent outside the list becomes unknown with confidence 0",
			domain.Classification{Intent: "refund", Confidence: 0.99},
			func(c domain.Classification) bool { return c.Intent == domain.IntentUnknown && c.Confidence == 0 }},
		{"a confidence above 1 is not trusted",
			domain.Classification{Intent: domain.IntentHowTo, Confidence: 7},
			func(c domain.Classification) bool { return c.Intent == domain.IntentUnknown && c.Confidence == 0 }},
		{"NaN confidence is not trusted",
			domain.Classification{Intent: domain.IntentHowTo, Confidence: math.NaN()},
			func(c domain.Classification) bool { return c.Intent == domain.IntentUnknown }},
		{"a valid answer keeps its intent and confidence",
			domain.Classification{Intent: domain.IntentHowTo, Confidence: 0.7},
			func(c domain.Classification) bool { return c.Intent == domain.IntentHowTo && c.Confidence == 0.7 }},
		{"an unknown emotion becomes neutral, an empty language Ukrainian",
			domain.Classification{Intent: domain.IntentHowTo, Emotion: "furious"},
			func(c domain.Classification) bool { return c.Emotion == domain.EmotionNeutral && c.Language == "uk" }},
		{"an error code is upper-cased; prose in that field is dropped",
			domain.Classification{Intent: domain.IntentLoginProblem, ErrorCode: " aadsts50020 "},
			func(c domain.Classification) bool { return c.ErrorCode == "AADSTS50020" }},
		{"prose in the error code field is dropped",
			domain.Classification{Intent: domain.IntentLoginProblem, ErrorCode: "some error with spaces"},
			func(c domain.Classification) bool { return c.ErrorCode == "" }},
		{"signals are lower-cased, deduplicated and capped",
			domain.Classification{Intent: domain.IntentRoleRequest, Signals: []string{"Prompt_Injection_Attempt", "prompt_injection_attempt", "", "a", "b", "c", "d", "e"}},
			func(c domain.Classification) bool {
				return len(c.Signals) == 5 && c.Signals[0] == domain.SignalPromptInjection && !slices.Contains(c.Signals, "")
			}},
		{"nil lists become empty lists",
			domain.Classification{Intent: domain.IntentHowTo},
			func(c domain.Classification) bool { return c.UserQuestions != nil && c.Signals != nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalize(tc.in); !tc.want(got) {
				t.Fatalf("normalize(%+v) = %+v", tc.in, got)
			}
		})
	}
}
