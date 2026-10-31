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
	"billing.login_needed", "billing.plans_general", "billing.plans", "billing.plans_cheaper",
	"retention.offer_plan", "retention.offer_tier", "retention.goodwill", "retention.goodwill_pending", "retention.handoff",
	"retention.unavailable", "retention.already_ending", "retention.nothing_paid", "retention.accepted", "refund.duplicate",
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
	// Free days are said to be given only by the reply sent after they were.
	done := regexp.MustCompile(`відновив|відновлено|видалив|видалено|змінив роль|додав тобі|днів додано`)
	afterTheFact := map[string]bool{"appeal.restored": true, "retention.goodwill": true}
	for id, text := range render.Templates {
		if !afterTheFact[id] && done.MatchString(text) {
			t.Errorf("%s promises a finished action: %q", id, done.FindString(text))
		}
	}
}

func TestNoReplySaysMoneyMoved(t *testing.T) {
	// Cancelling and refunding are a manager's call, so no reply may claim either happened.
	moved := regexp.MustCompile(`(?i)скасовано|скасував|скасувала|кошти повернуто|гроші повернуто|повернув кошти|повернули кошти`)
	for id, text := range render.Templates {
		if moved.MatchString(text) {
			t.Errorf("%s says money or a plan moved: %q", id, moved.FindString(text))
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
