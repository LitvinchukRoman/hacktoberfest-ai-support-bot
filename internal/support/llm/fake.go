// Package llm turns a support message into a domain.Classification. Fake does it
// with keywords, Gemini with Gemma 4. Neither ever chooses what the bot does.
package llm

import (
	"context"
	"regexp"
	"slices"
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
	{domain.IntentBillingRefund, []string{"поверніть гроші", "поверніть кошти", "повернути гроші", "повернути кошти", "повернення коштів", "повернення грошей", "двічі списал", "подвійне списання", "списали двічі", "refund", "money back"}},
	{domain.IntentBillingCancel, []string{"скасу", "відпиш", "відписат", "відмовитися від", "відмовитись від", "не хочу платити", "дорого", "cancel", "unsubscribe"}},
	{domain.IntentBillingQuestion, []string{"тариф", "підписк", "спонсорств", "оплат", "списал", "«плюс»", "«про»", "pricing", "subscription", "billing"}},
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
	if in.State.Flow == domain.FlowRetention {
		retentionReply(&c, lower)
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

var (
	declinePhrases = []string{"не хочу", "все одно", "всеодно", "не підходить", "не треба", "не цікав", "no thanks"}
	declineWords   = []string{"ні", "нє", "no", "nope"}
	acceptPhrases  = []string{"лишаюсь", "лишаюся", "залишаюсь", "залишаюся", "залишусь", "лишуся", "підходить", "згоден", "згодна"}
	acceptWords    = []string{"так", "ок", "ok", "yes", "давай", "добре"}
)

// retentionReply reads a short answer to an offer to stay. "Ні, дякую" is a refusal
// even though it thanks; a bare "так" is a yes only when nothing else matched.
func retentionReply(c *domain.Classification, lower string) {
	words := strings.FieldsFunc(lower, func(r rune) bool { return !unicode.IsLetter(r) && r != '\'' })
	hasWord := func(set []string) bool {
		for _, w := range words {
			if slices.Contains(set, w) {
				return true
			}
		}
		return false
	}
	switch {
	case contains(lower, declinePhrases) || hasWord(declineWords):
		if c.Intent == domain.IntentUnknown || c.Intent == domain.IntentThanksResolved {
			c.Intent, c.Confidence, c.Reasoning = domain.IntentBillingCancel, 0.8, "declines the offer to stay"
		}
	case c.Intent == domain.IntentUnknown && (contains(lower, acceptPhrases) || hasWord(acceptWords)):
		c.Intent, c.Confidence, c.Reasoning = domain.IntentThanksResolved, 0.8, "accepts the offer to stay"
	}
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
