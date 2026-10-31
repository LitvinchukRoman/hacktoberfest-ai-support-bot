// Package investigate checks the facts behind a complaint and returns a verdict
// with evidence a moderator can verify. It is deterministic and never calls a model.
package investigate

import (
	"fmt"
	"time"

	"github.com/handziurdmytro/skelar/internal/support/domain"
)

// ReportRules say when reports against an idea look coordinated rather than genuine.
type ReportRules struct {
	NewAccount    time.Duration // a reporter younger than this is a new account
	Window        time.Duration // all reports inside this window count as a burst
	MinSuspicious int           // how many new accounts, or burst reports, make it suspicious
}

var DefaultReportRules = ReportRules{NewAccount: 24 * time.Hour, Window: 30 * time.Minute, MinSuspicious: 3}

// Reports looks at the open reports on one idea. They are suspected abuse when at
// least MinSuspicious come from new accounts, or when at least MinSuspicious all
// arrived within Window from people with no approved idea. Anything else needs a person.
func Reports(reports []domain.ReportFacts, r ReportRules) domain.Verdict {
	n := len(reports)
	if n == 0 {
		return domain.Verdict{Kind: domain.VerdictUnclear, Evidence: []string{}, Explanation: "Відкритих скарг немає."}
	}
	young, withApproved := 0, 0
	first, last := reports[0].CreatedAt, reports[0].CreatedAt
	for _, rep := range reports {
		if rep.ReporterAge < r.NewAccount {
			young++
		}
		if rep.ReporterApproved > 0 {
			withApproved++
		}
		if rep.CreatedAt.Before(first) {
			first = rep.CreatedAt
		}
		if rep.CreatedAt.After(last) {
			last = rep.CreatedAt
		}
	}
	span := last.Sub(first)
	burst := n >= r.MinSuspicious && span <= r.Window && withApproved == 0

	evidence := []string{}
	if young > 0 {
		evidence = append(evidence, fmt.Sprintf("%d з %d %s — акаунти, молодші за %s", young, n, plural(n, "скарги", "скарг", "скарг"), hours(r.NewAccount)))
	}
	if span <= r.Window {
		evidence = append(evidence, fmt.Sprintf("усі %d %s подано за %d хв", n, plural(n, "скарга", "скарги", "скарг"), int(span.Minutes())))
	} else {
		evidence = append(evidence, fmt.Sprintf("скарги подано протягом %s", hours(span)))
	}
	if withApproved == 0 {
		evidence = append(evidence, "у скаржників 0 схвалених ідей")
	} else {
		evidence = append(evidence, fmt.Sprintf("%d з %d скаржників мають схвалені ідеї", withApproved, n))
	}

	if young >= r.MinSuspicious || burst {
		return domain.Verdict{Kind: domain.VerdictSuspectedAbuse, Evidence: evidence,
			Explanation: "Скарги схожі на скоординовані: нові акаунти або сплеск за короткий час."}
	}
	return domain.Verdict{Kind: domain.VerdictUnclear, Evidence: evidence,
		Explanation: "Ознак скоординованих скарг немає, рішення за модератором."}
}

// plural picks the Ukrainian form for n: one (1, 21), few (2–4, 22–24) or many.
func plural(n int, one, few, many string) string {
	switch n10, n100 := n%10, n%100; {
	case n10 == 1 && n100 != 11:
		return one
	case n10 >= 2 && n10 <= 4 && (n100 < 12 || n100 > 14):
		return few
	default:
		return many
	}
}

func hours(d time.Duration) string {
	if d == 24*time.Hour {
		return "добу"
	}
	h := int(d.Round(time.Hour).Hours())
	if h < 1 {
		return fmt.Sprintf("%d хв", int(d.Minutes()))
	}
	return fmt.Sprintf("%d год", h)
}
