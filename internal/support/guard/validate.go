package guard

import (
	"regexp"
	"unicode/utf8"

	"github.com/handziurdmytro/skelar/internal/support/domain"
)

// Check is one named pass/fail result of Validate, surfaced in the trace so
// staff can see exactly why a reply was or wasn't sent.
type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}

var (
	templateLeftoverRe = regexp.MustCompile(`\{\{`)
	externalLinkRe     = regexp.MustCompile(`(?i)https?://|www\.`)
	// Internal terms that must never leak into a customer-facing reply: rule
	// ids (G_..., A2_..., V6_...), and words only meaningful to staff.
	internalTermRe = regexp.MustCompile(`(?i)\b[A-Z][0-9]*_[A-Za-z]+|confidence|classifier|system prompt|промпт|policy|правило\s*[A-Z][0-9]`)
	// Phrases that claim an action already happened — only acceptable when
	// the decision is auto (the action really did run synchronously).
	overpromiseRe = regexp.MustCompile(`(?i)відновив|відновлено|видалено|змінив роль|змінено роль`)
)

// Validate is the last filter before a reply reaches a customer. Any failed
// check means the reply must NOT be sent — the ticket escalates with the
// name of the failed check instead.
func Validate(reply string, d domain.Decision) (bool, []Check) {
	length := utf8.RuneCountInString(reply)
	checks := []Check{
		{"non_empty", length >= 1 && length <= 1500},
		{"no_personal_data", len(Redact(reply).Found) == 0},
		{"no_internal_terms", !internalTermRe.MatchString(reply)},
		{"no_template_leftovers", !templateLeftoverRe.MatchString(reply)},
		{"no_external_links", !externalLinkRe.MatchString(reply)},
		{"no_overpromise", !overpromiseRe.MatchString(reply) || d.Autonomy == domain.AutonomyAuto},
	}

	ok := true
	for _, c := range checks {
		if !c.Passed {
			ok = false
		}
	}
	return ok, checks
}

// FailedCheck returns the name of the first failed check, or "" if all
// passed. Used to name the escalation reason (L6_veto:<reason> style).
func FailedCheck(checks []Check) string {
	for _, c := range checks {
		if !c.Passed {
			return c.Name
		}
	}
	return ""
}
