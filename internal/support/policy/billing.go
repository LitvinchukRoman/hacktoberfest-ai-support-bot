package policy

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // dates in replies are Kyiv dates on any host

	"github.com/handziurdmytro/skelar/internal/support/domain"
)

// Retention. A person who wants to cancel or get money back is first offered
// something better, a step at a time: a cheaper plan or sponsorship level that
// still covers what they use, then free days of their plan. Only a person who
// insists reaches a support manager, with a note of what they pay and what was
// offered. The bot never cancels and never refunds: the only billing change it
// makes is the one gift of free days per person.

var kyiv = mustKyiv()

func mustKyiv() *time.Location {
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		return time.UTC
	}
	return loc
}

// retentionStep is how many offers this conversation has already made.
func retentionStep(f domain.Facts) int {
	if f.State.Flow != domain.FlowRetention {
		return 0
	}
	return f.State.Step
}

func inRetention(step int) domain.ConversationState {
	return domain.ConversationState{Flow: domain.FlowRetention, Step: step}
}

// retentionHandles lists the intents a retention conversation answers itself. A
// refusal the classifier could not place ("ні", "все одно") is still a refusal.
func retentionHandles(i domain.Intent) bool {
	switch i {
	case domain.IntentBillingCancel, domain.IntentBillingRefund, domain.IntentUnknown,
		domain.IntentThanksResolved, domain.IntentHumanRequest, domain.IntentBillingQuestion:
		return true
	}
	return false
}

func retentionFlow(c domain.Classification, f domain.Facts, cfg Config) domain.Decision {
	switch c.Intent {
	case domain.IntentThanksResolved:
		d := answer("M7_offer_accepted", "retention.accepted", nil)
		d.Suggested = pricing("Відкрити тарифи")
		return d
	case domain.IntentHumanRequest:
		return retentionHandoff("M4_human_requested", f, cfg)
	case domain.IntentBillingQuestion:
		d := billingQuestion(f)
		d.NextState = f.State
		return d
	}
	return retention(c, f, cfg)
}

func retention(c domain.Classification, f domain.Facts, cfg Config) domain.Decision {
	b := f.Billing
	if b == nil {
		if f.User == nil {
			d := ask("M0_login_needed", "billing.login_needed", nil)
			d.Suggested = []domain.SuggestedAction{{Label: "Увійти", To: "/login"}}
			return d
		}
		return supportHandoff("M0_billing_unavailable", "retention.unavailable", "не вдалося отримати білінг клієнта з fantasm; клієнт питає про скасування чи повернення коштів", 2, nil)
	}
	if c.Intent == domain.IntentBillingRefund {
		if dup, ok := duplicateCharge(b, f.Now, cfg); ok {
			return supportHandoff("M5_duplicate_charge", "refund.duplicate",
				fmt.Sprintf("схоже на подвійне списання: два платежі «%s» по %s; повернути можна платіж %s", dup.Description, money(dup.AmountMinor), dup.ID),
				2, map[string]string{"amount": money(dup.AmountMinor), "date": day(dup.CreatedAt)})
		}
	}
	if !renews(b) {
		if c.Intent == domain.IntentBillingRefund {
			return retentionHandoff("M6_refund_requested", f, cfg)
		}
		return nothingRenews(b)
	}

	step := retentionStep(f)
	if step == 0 {
		if d, ok := cheaperOffer(b); ok {
			return d
		}
	}
	if step <= 1 {
		if d, ok := goodwill(b, cfg); ok {
			return d
		}
	}
	return retentionHandoff("M4_insists", f, cfg)
}

// renews reports whether anything will charge the person again.
func renews(b *domain.BillingFacts) bool {
	if b.Plan.Paid() && !b.CancelAtPeriodEnd {
		return true
	}
	for _, s := range b.Sponsorships {
		if !s.CancelAtPeriodEnd {
			return true
		}
	}
	return false
}

func nothingRenews(b *domain.BillingFacts) domain.Decision {
	if b.Plan.Paid() {
		d := answer("M1_plan_already_ending", "retention.already_ending", map[string]string{"plan": b.Plan.Name, "until": day(b.PeriodEnd)})
		d.Suggested = pricing("Відкрити тарифи")
		return d
	}
	d := answer("M1_nothing_paid", "retention.nothing_paid", nil)
	d.Suggested = pricing("Відкрити тарифи")
	return d
}

// cheaperOffer is the first offer: the dearest cheaper plan that still holds every
// active idea, or for a sponsor the next lower level of their dearest sponsorship.
// Going down to the free plan is not an offer: that is leaving, not staying.
func cheaperOffer(b *domain.BillingFacts) (domain.Decision, bool) {
	if b.Plan.Paid() && !b.CancelAtPeriodEnd {
		var best *domain.PlanFacts
		for i, p := range b.Plans {
			if p.Paid() && p.PriceMinor < b.Plan.PriceMinor && p.Fits(b.ActiveIdeas) && (best == nil || p.PriceMinor > best.PriceMinor) {
				best = &b.Plans[i]
			}
		}
		if best != nil {
			d := ask("M2_offer_cheaper_plan", "retention.offer_plan", map[string]string{
				"plan": b.Plan.Name, "price": money(b.Plan.PriceMinor), "ideas": strconv.Itoa(b.ActiveIdeas),
				"offer": best.Name, "offerPrice": money(best.PriceMinor), "limit": ideaLimit(best.ActiveIdeas),
				"saving": money(b.Plan.PriceMinor - best.PriceMinor), "until": day(b.PeriodEnd),
			})
			d.Suggested = pricing("Перейти на «" + best.Name + "»")
			d.NextState = inRetention(1)
			return d, true
		}
	}
	var dearest *domain.SponsorshipFacts
	for i, s := range b.Sponsorships {
		if !s.CancelAtPeriodEnd && (dearest == nil || s.PriceMinor > dearest.PriceMinor) {
			dearest = &b.Sponsorships[i]
		}
	}
	if dearest == nil {
		return domain.Decision{}, false
	}
	var lower *domain.TierFacts
	for i, t := range b.Tiers {
		if t.PriceMinor < dearest.PriceMinor && (lower == nil || t.PriceMinor > lower.PriceMinor) {
			lower = &b.Tiers[i]
		}
	}
	if lower == nil {
		return domain.Decision{}, false
	}
	d := ask("M2_offer_lower_tier", "retention.offer_tier", map[string]string{
		"idea": dearest.IdeaTitle, "tier": dearest.TierName, "price": money(dearest.PriceMinor),
		"offer": lower.Name, "offerPrice": money(lower.PriceMinor),
		"saving": money(dearest.PriceMinor - lower.PriceMinor), "until": day(dearest.PeriodEnd),
	})
	if dearest.IdeaSlug != "" {
		d.Suggested = []domain.SuggestedAction{{Label: "Змінити рівень", To: "/ideas/" + dearest.IdeaSlug}}
	}
	d.NextState = inRetention(1)
	return d, true
}

// goodwill is the second offer: free days of the paid plan, given once per person.
// It is the one billing change the bot makes itself, so it runs automatically.
func goodwill(b *domain.BillingFacts, cfg Config) (domain.Decision, bool) {
	if !b.Plan.Paid() || b.CancelAtPeriodEnd || b.GoodwillUsed || cfg.GoodwillDays <= 0 || b.Handle == "" {
		return domain.Decision{}, false
	}
	days := strconv.Itoa(cfg.GoodwillDays)
	d := ask("M3_goodwill_days", "retention.goodwill", map[string]string{
		"days": days, "plan": b.Plan.Name, "until": day(b.PeriodEnd.AddDate(0, 0, cfg.GoodwillDays)),
	})
	d.Actions = []domain.Action{{Type: domain.ActExtendPlan, Queue: domain.QueueSupport,
		Note:   fmt.Sprintf("подарунок %s днів плану «%s», щоб утримати клієнта", days, b.Plan.Name),
		Params: map[string]string{"days": days, "handle": b.Handle}}}
	d.NextState = inRetention(2)
	return d, true
}

// retentionHandoff gives a person who insists to a support manager, with
// everything the manager needs so nobody asks twice.
func retentionHandoff(rule string, f domain.Facts, cfg Config) domain.Decision {
	return supportHandoff(rule, "retention.handoff", handoffNote(f, cfg), 2, nil)
}

func supportHandoff(rule, template, note string, priority int, slots map[string]string) domain.Decision {
	return domain.Decision{
		Autonomy: domain.AutonomyEscalate, Rule: rule, TemplateID: template, Slots: slots, Status: domain.StatusEscalated,
		Escalation: &domain.Escalation{Reason: "retention_handoff", Queue: domain.QueueSupport, Priority: priority, Note: note},
	}
}

func handoffNote(f domain.Facts, cfg Config) string {
	b := f.Billing
	if b == nil {
		return "клієнт хоче скасувати підписку або повернути кошти; білінг недоступний"
	}
	var parts []string
	if b.Plan.Paid() {
		state := "продовжується"
		if b.CancelAtPeriodEnd {
			state = "не продовжуватиметься"
		}
		parts = append(parts, fmt.Sprintf("план «%s» %s/міс, оплачено до %s, %s; активних ідей %d", b.Plan.Name, money(b.Plan.PriceMinor), day(b.PeriodEnd), state, b.ActiveIdeas))
	} else {
		parts = append(parts, "безкоштовний план")
	}
	for _, s := range b.Sponsorships {
		parts = append(parts, fmt.Sprintf("спонсорство «%s», рівень «%s» %s/міс", s.IdeaTitle, s.TierName, money(s.PriceMinor)))
	}
	var offered []string
	step := retentionStep(f)
	if step >= 1 {
		if d, ok := cheaperOffer(b); ok {
			offered = append(offered, d.Slots["offer"]+" (−"+d.Slots["saving"]+"/міс)")
		}
	}
	if step >= 2 {
		offered = append(offered, strconv.Itoa(cfg.GoodwillDays)+" днів безкоштовно")
	} else if b.GoodwillUsed {
		offered = append(offered, "безкоштовні дні вже дарували раніше")
	}
	if len(offered) > 0 {
		parts = append(parts, "бот уже запропонував: "+strings.Join(offered, "; "))
	}
	var refundable []string
	for _, p := range b.Payments {
		if p.Refundable(f.Now) {
			refundable = append(refundable, fmt.Sprintf("%s від %s «%s» (%s)", money(p.AmountMinor), day(p.CreatedAt), p.Description, p.ID))
		}
	}
	if len(refundable) > 0 {
		parts = append(parts, "можна повернути (до 14 днів): "+strings.Join(refundable, "; "))
	} else {
		parts = append(parts, "платежів, які ще можна повернути, немає")
	}
	return "клієнт наполягає на скасуванні або поверненні коштів. " + strings.Join(parts, ". ")
}

// duplicateCharge finds two refundable charges for the same thing close together:
// a mistake on our side, so nobody tries to talk the person out of a refund.
func duplicateCharge(b *domain.BillingFacts, now time.Time, cfg Config) (domain.PaymentFacts, bool) {
	for i, a := range b.Payments {
		if !a.Refundable(now) {
			continue
		}
		for _, o := range b.Payments[i+1:] {
			gap := a.CreatedAt.Sub(o.CreatedAt).Abs()
			if o.Refundable(now) && o.Description == a.Description && o.AmountMinor == a.AmountMinor && gap <= cfg.DuplicateWindow {
				return a, true
			}
		}
	}
	return domain.PaymentFacts{}, false
}

func billingQuestion(f domain.Facts) domain.Decision {
	b := f.Billing
	if b == nil || len(b.Plans) == 0 {
		d := answer("M8_plans_general", "billing.plans_general", nil)
		d.Suggested = pricing("Відкрити тарифи")
		return d
	}
	slots := map[string]string{
		"plan": b.Plan.Name, "price": price(b.Plan.PriceMinor), "limit": ideaLimit(b.Plan.ActiveIdeas),
		"ideas": strconv.Itoa(b.ActiveIdeas), "catalog": catalog(b.Plans),
	}
	template, rule := "billing.plans", "M8_plans"
	if d, ok := cheaperOffer(b); ok && d.Rule == "M2_offer_cheaper_plan" {
		template, rule = "billing.plans_cheaper", "M8_plans_cheaper_fits"
		slots["offer"], slots["saving"] = d.Slots["offer"], d.Slots["saving"]
	}
	d := answer(rule, template, slots)
	d.Suggested = pricing("Відкрити тарифи")
	return d
}

func pricing(label string) []domain.SuggestedAction {
	return []domain.SuggestedAction{{Label: label, To: "/pricing"}}
}

func catalog(plans []domain.PlanFacts) string {
	parts := make([]string, len(plans))
	for i, p := range plans {
		parts[i] = fmt.Sprintf("«%s» — %s, %s", p.Name, price(p.PriceMinor), ideaLimit(p.ActiveIdeas))
	}
	return strings.Join(parts, "; ")
}

func ideaLimit(n int) string {
	if n <= 0 {
		return "без ліміту активних ідей"
	}
	return "до " + strconv.Itoa(n) + " активних ідей"
}

func price(minor int) string {
	if minor <= 0 {
		return "безкоштовно"
	}
	return money(minor) + " на місяць"
}

// money formats kopiyky as hryvnias: 14900 is "149 ₴", 14950 is "149,50 ₴".
func money(minor int) string {
	if minor%100 == 0 {
		return strconv.Itoa(minor/100) + " ₴"
	}
	return fmt.Sprintf("%d,%02d ₴", minor/100, minor%100)
}

var monthsGenitive = [...]string{"січня", "лютого", "березня", "квітня", "травня", "червня", "липня", "серпня", "вересня", "жовтня", "листопада", "грудня"}

// Day is a Kyiv date the way a person says it: "12 листопада".
func Day(t time.Time) string { return day(t) }

func day(t time.Time) string {
	if t.IsZero() {
		return "кінця оплаченого періоду"
	}
	k := t.In(kyiv)
	return strconv.Itoa(k.Day()) + " " + monthsGenitive[k.Month()-1]
}
