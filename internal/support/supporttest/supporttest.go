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

// Fantasm's price list: three author plans and three sponsorship levels.
func catalog() ([]domain.PlanFacts, []domain.TierFacts) {
	return []domain.PlanFacts{
		{Code: "FREE", Name: "Старт", PriceMinor: 0, ActiveIdeas: 3},
		{Code: "PLUS", Name: "Плюс", PriceMinor: 14900, ActiveIdeas: 15},
		{Code: "PRO", Name: "Про", PriceMinor: 39900},
	}, []domain.TierFacts{
		{Code: "SUPPORTER", Name: "Друг ідеї", PriceMinor: 5000},
		{Code: "BACKER", Name: "Меценат", PriceMinor: 20000},
		{Code: "PATRON", Name: "Партнер", PriceMinor: 50000},
	}
}

func billing(planCode string, activeIdeas int) *domain.BillingFacts {
	plans, tiers := catalog()
	b := &domain.BillingFacts{Handle: "olena", ActiveIdeas: activeIdeas, Plans: plans, Tiers: tiers}
	for _, p := range plans {
		if p.Code == planCode {
			b.Plan = p
		}
	}
	if b.Plan.Paid() {
		b.PeriodEnd = Now.Add(12 * day)
		b.Payments = []domain.PaymentFacts{{ID: "pay-1", Kind: "PLAN", AmountMinor: b.Plan.PriceMinor, Status: domain.PaymentSucceeded,
			Description: "План «" + b.Plan.Name + "»", CreatedAt: Now.Add(-18 * day)}}
	}
	return b
}

// ProAuthor pays for «Про» and has this many active ideas: 4 fit «Плюс», 20 do not.
func ProAuthor(activeIdeas int) *domain.BillingFacts { return billing("PRO", activeIdeas) }

// PlusAuthor pays for «Плюс», the cheapest paid plan, so there is nothing cheaper to offer.
func PlusAuthor() *domain.BillingFacts { return billing("PLUS", 4) }

// EndingPlan already will not renew.
func EndingPlan() *domain.BillingFacts {
	b := ProAuthor(4)
	b.CancelAtPeriodEnd = true
	return b
}

// FreeAuthor pays for nothing.
func FreeAuthor() *domain.BillingFacts { return billing("FREE", 2) }

// Sponsor is on the free plan and backs one idea at «Меценат».
func Sponsor() *domain.BillingFacts {
	b := FreeAuthor()
	b.Sponsorships = []domain.SponsorshipFacts{{ID: "sp-1", IdeaSlug: "sadok-na-dakhu", IdeaTitle: "Сад на даху корпусу", Tier: "BACKER",
		TierName: "Меценат", PriceMinor: 20000, PeriodEnd: Now.Add(20 * day)}}
	b.Payments = []domain.PaymentFacts{{ID: "pay-sp", Kind: "SPONSORSHIP", AmountMinor: 20000, Status: domain.PaymentSucceeded,
		Description: "Спонсорство «Сад на даху корпусу»", CreatedAt: Now.Add(-10 * day)}}
	return b
}

// DoubleCharged paid for «Плюс» twice within an hour, two days ago.
func DoubleCharged() *domain.BillingFacts {
	b := PlusAuthor()
	b.Payments = []domain.PaymentFacts{
		{ID: "pay-2", Kind: "PLAN", AmountMinor: 14900, Status: domain.PaymentSucceeded, Description: "План «Плюс»", CreatedAt: Now.Add(-47 * time.Hour)},
		{ID: "pay-1", Kind: "PLAN", AmountMinor: 14900, Status: domain.PaymentSucceeded, Description: "План «Плюс»", CreatedAt: Now.Add(-48 * time.Hour)},
	}
	return b
}

// GoodwillGiven already got free days from the bot once.
func GoodwillGiven() *domain.BillingFacts {
	b := ProAuthor(20)
	b.GoodwillUsed = true
	return b
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
	states := []domain.ConversationState{{}, {Flow: domain.FlowAppeal, Step: 1},
		{Flow: domain.FlowRetention, Step: 1}, {Flow: domain.FlowRetention, Step: 2}}
	contexts := []struct {
		name      string
		push      *domain.PushFacts
		knowledge []domain.KBHit
		code      string
		billing   func() *domain.BillingFacts
	}{
		{"bare", nil, nil, "", nil},
		{"no push, guide, code", &domain.PushFacts{}, []domain.KBHit{TeamGuide()}, "AADSTS50020", nil},
		{"stuck push, guide, code", &domain.PushFacts{Subscriptions: 1, Stuck: 2}, []domain.KBHit{TeamGuide()}, "AADSTS50020", nil},
		{"healthy push", &domain.PushFacts{Subscriptions: 2}, nil, "", nil},
		{"pro, 4 ideas", nil, nil, "", func() *domain.BillingFacts { return ProAuthor(4) }},
		{"pro, 20 ideas", nil, nil, "", func() *domain.BillingFacts { return ProAuthor(20) }},
		{"plus", nil, nil, "", PlusAuthor},
		{"plan ending", nil, nil, "", EndingPlan},
		{"free", nil, nil, "", FreeAuthor},
		{"sponsor", nil, nil, "", Sponsor},
		{"double charged", nil, nil, "", DoubleCharged},
		{"goodwill given", nil, nil, "", GoodwillGiven},
	}

	var out []Case
	n := 0
	for _, intent := range domain.Intents {
		for _, u := range users {
			for _, mkIdea := range ideas {
				for _, state := range states {
					for _, ctx := range contexts {
						iv := mkIdea()
						var b *domain.BillingFacts
						if ctx.billing != nil {
							b = ctx.billing()
						}
						emotion := domain.EmotionNeutral
						if n%3 == 0 {
							emotion = domain.EmotionAngry
						}
						n++
						out = append(out, Case{
							Name: fmt.Sprintf("%s / %s / %s / %s / %s", intent, u.name, iv.name, state.Flow, ctx.name),
							Class: domain.Classification{Intent: intent, Emotion: emotion, Language: "uk", Confidence: 0.9,
								ErrorCode: ctx.code, UserQuestions: []string{"питання"}, Signals: []string{}},
							Facts: domain.Facts{Now: Now, User: u.user(), Idea: iv.idea, Reports: iv.reports, Push: ctx.push, Knowledge: ctx.knowledge, State: state, Billing: b},
						})
					}
				}
			}
		}
	}
	return out
}
