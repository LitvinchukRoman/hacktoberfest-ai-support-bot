package render_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/handziurdmytro/skelar/internal/support/domain"
	"github.com/handziurdmytro/skelar/internal/support/policy"
	"github.com/handziurdmytro/skelar/internal/support/render"
	"github.com/handziurdmytro/skelar/internal/support/supporttest"
)

// required is every template policy, guard or the operations side may name.
var required = []string{
	"account.login_needed",
	"visibility.which_idea", "visibility.draft", "visibility.premoderation", "visibility.premoderation_overdue",
	"visibility.rejected", "visibility.restricted", "visibility.live",
	"appeal.not_hidden", "appeal.queued", "appeal.sent_to_moderator", "appeal.restored", "appeal.declined",
	"seal.already", "seal.use_microsoft", "seal.email_domain", "seal.investigating",
	"login.checklist", "login.known_error",
	"push.enable", "push.investigating", "push.checklist",
	"howto.answer", "howto.unknown", "role.policy", "report.how", "bug.thanks", "thanks.close",
	"escalation.generic", "escalation.legal", "escalation.offensive", "escalation.human", "escalation.unclear", "escalation.fallback",
	"safety.support", "safety.cannot_do", "safety.pii_removed",
	"gdpr.request_received", "gdpr.done",
}

// sample fills every slot a template asks for.
func sample(id string) map[string]string {
	slots := map[string]string{}
	for _, name := range render.Slots(id) {
		slots[name] = "Тест"
	}
	if _, ok := slots["visibility"]; ok {
		slots["visibility"] = domain.VisibilityMembers
	}
	return slots
}

var (
	internalTerms = regexp.MustCompile(`(?i)\b[A-Z]\d_[a-z]|\bG_[a-z]|confidence|classifier|intent|prompt|policy|template`)
	links         = regexp.MustCompile(`(?i)https?://|www\.`)
)

func TestEveryRequiredTemplateExists(t *testing.T) {
	for _, id := range required {
		if _, ok := render.Templates[id]; !ok {
			t.Errorf("template %s is missing", id)
		}
	}
}

func TestTemplatesFollowTheWordingRules(t *testing.T) {
	for id := range render.Templates {
		text, err := render.Fill(id, sample(id))
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		switch {
		case strings.Contains(text, "{{") || strings.Contains(text, "}}"):
			t.Errorf("%s: a slot was left unfilled", id)
		case links.MatchString(text):
			t.Errorf("%s: replies carry no links, buttons do", id)
		case internalTerms.MatchString(text):
			t.Errorf("%s: internal term in a reply: %q", id, internalTerms.FindString(text))
		case len([]rune(text)) > 600:
			t.Errorf("%s: %d characters is too long for a chat reply", id, len([]rune(text)))
		}
	}
}

func TestSafetyReplyGivesTheHelplineNumbers(t *testing.T) {
	text := render.Templates["safety.support"]
	for _, number := range []string{"7333", "112"} {
		if !strings.Contains(text, number) {
			t.Errorf("safety.support must mention %s", number)
		}
	}
}

func TestNothingIsDoneBeforeAPersonApproves(t *testing.T) {
	// Only the reply sent after a moderator approved may say the idea is back.
	done := regexp.MustCompile(`відновив|відновлено|видалив|видалено|змінив роль`)
	for id, text := range render.Templates {
		if id != "appeal.restored" && done.MatchString(text) {
			t.Errorf("%s promises a finished action: %q", id, done.FindString(text))
		}
	}
}

func TestFillReportsWhatIsMissing(t *testing.T) {
	if _, err := render.Fill("visibility.premoderation", map[string]string{"title": "X"}); !errors.Is(err, render.ErrMissingSlot) {
		t.Fatalf("err = %v, want ErrMissingSlot", err)
	}
	if _, err := render.Fill("no.such", nil); !errors.Is(err, render.ErrUnknownTemplate) {
		t.Fatalf("err = %v, want ErrUnknownTemplate", err)
	}
}

func TestVisibilityIsSaidInHumanWords(t *testing.T) {
	text, err := render.Fill("visibility.restricted", map[string]string{"title": "Сад", "visibility": domain.VisibilityMembers})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "лише ті, хто увійшов на сайт") || strings.Contains(text, "MEMBERS_ONLY") {
		t.Fatalf("text = %q", text)
	}
}

func TestEmpathyOpenerForAngerButNotOnSafetyReplies(t *testing.T) {
	ctx := context.Background()
	angry, _, err := render.Templater{}.Render(ctx, domain.RenderInput{TemplateID: "thanks.close", Emotion: domain.EmotionAngry})
	if err != nil || !strings.HasPrefix(angry, "Розумію, що це неприємно") {
		t.Fatalf("angry reply = %q, %v", angry, err)
	}
	safety, _, err := render.Templater{}.Render(ctx, domain.RenderInput{TemplateID: "safety.support", Emotion: domain.EmotionAngry})
	if err != nil || strings.HasPrefix(safety, "Розумію") {
		t.Fatalf("safety reply = %q, %v", safety, err)
	}
}

// Every decision policy can make, on every input of the grid, renders into a
// complete reply: the template exists and policy filled all of its slots.
func TestEveryPolicyDecisionRenders(t *testing.T) {
	for _, c := range supporttest.Grid() {
		d := policy.Decide(c.Class, c.Facts, policy.DefaultConfig)
		text, usage, err := render.Templater{}.Render(context.Background(), domain.RenderInput{TemplateID: d.TemplateID, Slots: d.Slots, Emotion: c.Class.Emotion})
		if err != nil {
			t.Fatalf("%s: rule %s: %v", c.Name, d.Rule, err)
		}
		if text == "" || usage.Model != render.TemplateModel {
			t.Fatalf("%s: empty reply or wrong model %q", c.Name, usage.Model)
		}
	}
}
