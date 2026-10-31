// Package llm turns a support message into a domain.Classification. Fake does it
// with keywords, Gemini with Gemma 4. Neither ever chooses what the bot does.
package llm

import (
	"context"
	"regexp"
	"strings"
	"unicode"

	"github.com/handziurdmytro/skelar/internal/support/domain"
)

// FakeModel is the Usage.Model of the keyword classifier, so the trace shows the model was not used.
const FakeModel = "fake"

// Fake is a deterministic keyword classifier. It runs the offline demo, stands in
// when the model fails, and gives unit tests a classifier without the network.
type Fake struct{}

type keywordRule struct {
	intent   domain.Intent
	keywords []string
}

// fakeRules are tried in order and the first match wins: a complaint about a hidden
// idea beats "not in the feed", and anything specific beats a polite "дякую".
var fakeRules = []keywordRule{
	{domain.IntentModerationAppeal, []string{"сховал", "приховал", "оскарж", "заблокувал", "was hidden", "appeal"}},
	{domain.IntentIdeaVisibility, []string{"немає в стрічці", "нема в стрічці", "немає у стрічці", "зникла", "на модерації", "премодерац", "не бачу свою ідею", "not in the feed", "can't see my idea"}},
	{domain.IntentVerificationSeal, []string{"печатк", "верифік", "могилянц", "badge", "verified"}},
	{domain.IntentLoginProblem, []string{"aadsts", "не можу увійти", "не можу зайти", "не виходить увійти", "can't log in", "cannot log in", "can't sign in"}},
	{domain.IntentNotificationsIssue, []string{"сповіщен", "push", "notification"}},
	{domain.IntentRoleRequest, []string{"модератором", "адміном", "адміністратором", "права модератора", "make me admin", "become a moderator"}},
	{domain.IntentHowTo, []string{"як знайти", "як створити", "як написати", "як додати", "як подати", "how do i", "how to"}},
	{domain.IntentThanksResolved, []string{"дякую", "спасибі", "thanks", "thank you"}},
}

var (
	injectionPhrases  = []string{"ігноруй", "ignore previous", "ignore all", "забудь правила", "ти адмін", "ти тепер", "you are now", "system prompt", "системний промпт", "jailbreak"}
	angryPhrases      = []string{"нічого не поруш", "обур", "жах", "безпредел", "що за", "!!"}
	frustratedPhrases = []string{"знову", "досі не", "вже вдруге", "скільки можна", "again"}
	confusedPhrases   = []string{"не розумію", "незрозуміло", "confused"}

	errorCodePattern = regexp.MustCompile(`(?i)\baadsts\d{4,}`)
	quotedPattern    = regexp.MustCompile(`[«"“]([^»"”]{3,120})[»"”]`)
)

func (Fake) Classify(_ context.Context, in domain.ClassifyInput) (domain.Classification, domain.Usage, error) {
	text := strings.TrimSpace(in.Text)
	lower := strings.ToLower(text)
	c := domain.Classification{
		Intent:        domain.IntentUnknown,
		Emotion:       domain.EmotionNeutral,
		Language:      language(text),
		Confidence:    0.3,
		UserQuestions: questions(text),
		Signals:       []string{},
		Reasoning:     "no keyword matched",
	}
	for _, rule := range fakeRules {
		if kw, ok := firstContained(lower, rule.keywords); ok {
			c.Intent, c.Confidence, c.Reasoning = rule.intent, 0.8, "keyword: "+kw
			break
		}
	}
	if code := errorCodePattern.FindString(text); code != "" {
		c.ErrorCode = strings.ToUpper(code)
	}
	if m := quotedPattern.FindStringSubmatch(text); m != nil {
		c.IdeaHint = strings.TrimSpace(m[1])
	}
	if _, ok := firstContained(lower, injectionPhrases); ok {
		c.Signals = append(c.Signals, domain.SignalPromptInjection)
	}
	switch {
	case c.Intent == domain.IntentThanksResolved:
		c.Emotion = domain.EmotionSatisfied
	case contains(lower, angryPhrases):
		c.Emotion = domain.EmotionAngry
	case contains(lower, frustratedPhrases):
		c.Emotion = domain.EmotionFrustrated
	case contains(lower, confusedPhrases):
		c.Emotion = domain.EmotionConfused
	}
	return c, domain.Usage{Model: FakeModel}, nil
}

func firstContained(s string, needles []string) (string, bool) {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return n, true
		}
	}
	return "", false
}

func contains(s string, needles []string) bool {
	_, ok := firstContained(s, needles)
	return ok
}

// language tells Ukrainian from English by script: good enough for choosing a reply language.
func language(text string) string {
	var cyrillic, latin int
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Cyrillic, r):
			cyrillic++
		case unicode.Is(unicode.Latin, r):
			latin++
		}
	}
	if latin > cyrillic {
		return "en"
	}
	return "uk"
}

// questions returns the sentences that end with a question mark, or the whole
// message when there are none, so knowledge search always has a query.
func questions(text string) []string {
	var out []string
	start := 0
	for i, r := range text {
		switch r {
		case '?':
			if q := strings.TrimSpace(text[start : i+1]); q != "" {
				out = append(out, q)
			}
			start = i + 1
		case '.', '!', '\n':
			start = i + 1
		}
	}
	if len(out) == 0 && text != "" {
		out = []string{truncateRunes(text, 300)}
	}
	return out
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
