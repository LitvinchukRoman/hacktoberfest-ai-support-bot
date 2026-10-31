// Package supporttest builds inputs for support tests: a fixed clock, the demo
// people and ideas, and a grid of classifications × facts that reaches every
// policy branch. Only tests import it.
package supporttest

import (
	"fmt"
	"time"

	"github.com/handziurdmytro/skelar/internal/support/domain"
)

// Now is the clock every support test runs at.
var Now = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

const (
	day  = 24 * time.Hour
	year = 365 * day
)

// GoogleNewcomer signed in with Google and has no approved idea yet (Олена, Тарас).
func GoogleNewcomer() *domain.UserFacts {
	return &domain.UserFacts{ID: "user-olena", Role: "USER", Provider: domain.ProviderGoogle, CreatedAt: Now.Add(-30 * day)}
}

// EntraOtherTenant signed in with a Microsoft account that is not the university's.
func EntraOtherTenant() *domain.UserFacts {
	return &domain.UserFacts{ID: "user-entra", Role: "USER", Provider: domain.ProviderEntra, CreatedAt: Now.Add(-year)}
}

// EntraUnverified has every seal signal and still no seal: a platform bug.
func EntraUnverified() *domain.UserFacts {
	return &domain.UserFacts{ID: "user-bug", Role: "USER", Provider: domain.ProviderEntra, TenantMatches: true, UKMAEmail: true, CreatedAt: Now.Add(-year)}
}

// VerifiedMember holds the seal and three approved ideas.
func VerifiedMember() *domain.UserFacts {
	return &domain.UserFacts{ID: "user-member", Role: "USER", Provider: domain.ProviderEntra, TenantMatches: true, UKMAEmail: true, Verified: true, ApprovedIdeas: 3, CreatedAt: Now.Add(-year)}
}

// PendingIdea waits in premoderation since age ago (S1 uses 3 h).
func PendingIdea(age time.Duration) *domain.IdeaFacts {
	return &domain.IdeaFacts{ID: "idea-pending", Slug: "kavovyi-avtomat", Title: "Кавовий автомат у бібліотеці", Status: "OPEN",
		Moderation: domain.ModerationPending, Visibility: domain.VisibilityPublic, CreatedAt: Now.Add(-age),
		CaseSource: domain.CasePremoderation, CaseOpenedAt: Now.Add(-age)}
}

// HiddenByReports was taken down automatically by five reports, no moderator has looked yet (S2).
func HiddenByReports() *domain.IdeaFacts {
	return &domain.IdeaFacts{ID: "idea-hidden", Slug: "khakaton-dlia-pershokursnykiv", Title: "Хакатон для першокурсників", Status: "OPEN",
		Moderation: domain.ModerationHidden, Visibility: domain.VisibilityPublic, CreatedAt: Now.Add(-10 * day),
		CaseSource: domain.CaseReports, CaseOpenedAt: Now.Add(-2 * time.Hour), OpenReports: 5}
}

// HiddenByModerator was hidden by a person's decision.
func HiddenByModerator() *domain.IdeaFacts {
	i := HiddenByReports()
	i.CaseSource, i.CaseOpenedAt, i.DecidedByHuman, i.DecisionNote, i.OpenReports = "", time.Time{}, true, "Порушує правила спільноти.", 0
	return i
}

// HiddenWithoutReportsCase is hidden while its open case came from premoderation.
func HiddenWithoutReportsCase() *domain.IdeaFacts {
	i := HiddenByReports()
	i.CaseSource = domain.CasePremoderation
	return i
}

// RejectedIdea was rejected by a moderator with a note.
func RejectedIdea() *domain.IdeaFacts {
	return &domain.IdeaFacts{ID: "idea-rejected", Slug: "prodazh-konspektiv", Title: "Продаж конспектів", Status: "OPEN",
		Moderation: domain.ModerationRejected, Visibility: domain.VisibilityPublic, CreatedAt: Now.Add(-5 * day),
		DecidedByHuman: true, DecisionNote: "Комерційні оголошення не публікуємо."}
}

// LiveIdea is approved and visible with the given visibility.
func LiveIdea(visibility string) *domain.IdeaFacts {
	return &domain.IdeaFacts{ID: "idea-live", Slug: "sadok-na-dakhu", Title: "Сад на даху корпусу", Status: "OPEN",
		Moderation: domain.ModerationApproved, Visibility: visibility, CreatedAt: Now.Add(-20 * day)}
}

// DraftIdea is saved but never published.
func DraftIdea() *domain.IdeaFacts {
	i := LiveIdea(domain.VisibilityPublic)
	i.ID, i.Slug, i.Title, i.Status, i.Moderation = "idea-draft", "chernetka-proiektu", "Чернетка проєкту", domain.IdeaDraft, domain.ModerationPending
	return i
}

// BrigadeReports: four accounts younger than a day and one old one, all within 12 minutes, none with an approved idea (S2).
func BrigadeReports() []domain.ReportFacts {
	at := Now.Add(-2 * time.Hour)
	return []domain.ReportFacts{
		{ReporterAge: 2 * time.Hour, CreatedAt: at},
		{ReporterAge: 3 * time.Hour, CreatedAt: at.Add(3 * time.Minute)},
		{ReporterAge: 5 * time.Hour, CreatedAt: at.Add(7 * time.Minute)},
		{ReporterAge: time.Hour, CreatedAt: at.Add(10 * time.Minute)},
		{ReporterAge: year, CreatedAt: at.Add(12 * time.Minute)},
	}
}

// HonestReports: established people reporting over several days.
func HonestReports() []domain.ReportFacts {
	at := Now.Add(-5 * day)
	out := make([]domain.ReportFacts, 5)
	for i := range out {
		out[i] = domain.ReportFacts{ReporterAge: year, ReporterApproved: i + 1, CreatedAt: at.Add(time.Duration(i) * day)}
	}
	return out
}

// TeamGuide is the knowledge-base hit for S5.
func TeamGuide() domain.KBHit {
	return domain.KBHit{Path: "/campus/yak-znaity-komandu-dlia-proiektu", Title: "Як знайти команду для курсової, хакатону чи стартапу",
		Snippet: "Зареєструйся індивідуально, вкажи навички й напиши в чат учасників: роль, навички, ідея, контакт.", Score: 4.2}
}

// Case is one input to the brain: what the model understood and what the database says.
type Case struct {
	Name  string
	Class domain.Classification
	Facts domain.Facts
}

type ideaVariant struct {
	name    string
	idea    *domain.IdeaFacts
	reports []domain.ReportFacts
}

// Grid crosses every intent with anonymous and signed-in people, every kind of
// idea, both conversation states and the push/knowledge/error-code variants.
// It is large on purpose: invariants must hold for all of it.
func Grid() []Case {
	users := []struct {
		name string
		user func() *domain.UserFacts
	}{
		{"anonymous", func() *domain.UserFacts { return nil }},
		{"google newcomer", GoogleNewcomer},
		{"entra other tenant", EntraOtherTenant},
		{"entra unverified", EntraUnverified},
		{"verified member", VerifiedMember},
	}
	ideas := []func() ideaVariant{
		func() ideaVariant { return ideaVariant{"no idea", nil, nil} },
		func() ideaVariant { return ideaVariant{"pending 3h", PendingIdea(3 * time.Hour), nil} },
		func() ideaVariant { return ideaVariant{"pending 3 days", PendingIdea(72 * time.Hour), nil} },
		func() ideaVariant { return ideaVariant{"draft", DraftIdea(), nil} },
		func() ideaVariant { return ideaVariant{"rejected", RejectedIdea(), nil} },
		func() ideaVariant { return ideaVariant{"hidden, brigade", HiddenByReports(), BrigadeReports()} },
		func() ideaVariant { return ideaVariant{"hidden, honest reports", HiddenByReports(), HonestReports()} },
		func() ideaVariant { return ideaVariant{"hidden by moderator", HiddenByModerator(), BrigadeReports()} },
		func() ideaVariant { return ideaVariant{"hidden, premoderation case", HiddenWithoutReportsCase(), nil} },
		func() ideaVariant { return ideaVariant{"live public", LiveIdea(domain.VisibilityPublic), nil} },
		func() ideaVariant { return ideaVariant{"live members only", LiveIdea(domain.VisibilityMembers), nil} },
		func() ideaVariant {
			return ideaVariant{"live organization only", LiveIdea(domain.VisibilityOrganization), nil}
		},
	}
	states := []domain.ConversationState{{}, {Flow: domain.FlowAppeal, Step: 1}}
	contexts := []struct {
		name      string
		push      *domain.PushFacts
		knowledge []domain.KBHit
		code      string
	}{
		{"bare", nil, nil, ""},
		{"no push, guide, code", &domain.PushFacts{}, []domain.KBHit{TeamGuide()}, "AADSTS50020"},
		{"stuck push, guide, code", &domain.PushFacts{Subscriptions: 1, Stuck: 2}, []domain.KBHit{TeamGuide()}, "AADSTS50020"},
		{"healthy push", &domain.PushFacts{Subscriptions: 2}, nil, ""},
	}

	var out []Case
	n := 0
	for _, intent := range domain.Intents {
		for _, u := range users {
			for _, mkIdea := range ideas {
				for _, state := range states {
					for _, ctx := range contexts {
						iv := mkIdea()
						emotion := domain.EmotionNeutral
						if n%3 == 0 {
							emotion = domain.EmotionAngry
						}
						n++
						out = append(out, Case{
							Name: fmt.Sprintf("%s / %s / %s / %s / %s", intent, u.name, iv.name, state.Flow, ctx.name),
							Class: domain.Classification{Intent: intent, Emotion: emotion, Language: "uk", Confidence: 0.9,
								ErrorCode: ctx.code, UserQuestions: []string{"питання"}, Signals: []string{}},
							Facts: domain.Facts{Now: Now, User: u.user(), Idea: iv.idea, Reports: iv.reports, Push: ctx.push, Knowledge: ctx.knowledge, State: state},
						})
					}
				}
			}
		}
	}
	return out
}
