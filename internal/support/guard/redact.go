// Package guard is the deterministic safety net Backend 2 wraps around the
// Brain: Redact strips PII before anything reaches an LLM or the database,
// Detect flags raw-text signals, Tighten can only escalate a decision
// towards a human, and Validate vetoes a reply before it is sent.
package guard

import (
	"regexp"
	"strings"
)

var (
	cardRe  = regexp.MustCompile(`\b(?:\d[ -]?){13,19}\b`)
	ibanRe  = regexp.MustCompile(`(?i)\bUA\d{2}(?:\s?[0-9A-Z]){25}\b`)
	phoneRe = regexp.MustCompile(`(?:\+?38[\s-]?)?\(?0[\s-]?\d{2}\)?[\s-]?\d{3}[\s-]?\d{2}[\s-]?\d{2}`)
	emailRe = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
)

// Redaction is the result of Redact: the text with PII replaced by
// placeholders, and the distinct types found (stored as support_messages.pii_found).
type Redaction struct {
	Text  string
	Found []string
}

// Redact replaces card numbers, IBANs, phone numbers and emails with
// placeholders. The raw text must never be stored or sent to an LLM —
// only the output of this function is. Card-looking digit runs that fail
// the Luhn check are left untouched: they are not real card numbers.
func Redact(text string) Redaction {
	found := map[string]bool{}

	text = cardRe.ReplaceAllStringFunc(text, func(m string) string {
		// The group's separator is optional per-digit, so a greedy match can
		// swallow a trailing space/dash that belongs to the surrounding
		// sentence, not the number. Trim it back off before validating.
		core := strings.TrimRight(m, " -")
		suffix := m[len(core):]
		digits := stripCardSeparators(core)
		if len(digits) < 13 || len(digits) > 19 || !luhnValid(digits) {
			return m
		}
		found["card"] = true
		return "[CARD]" + suffix
	})

	text = ibanRe.ReplaceAllStringFunc(text, func(string) string {
		found["iban"] = true
		return "[IBAN]"
	})

	text = phoneRe.ReplaceAllStringFunc(text, func(m string) string {
		found["phone"] = true
		return "[PHONE]"
	})

	text = emailRe.ReplaceAllStringFunc(text, func(string) string {
		found["email"] = true
		return "[EMAIL]"
	})

	types := make([]string, 0, len(found))
	for _, t := range []string{"card", "iban", "phone", "email"} {
		if found[t] {
			types = append(types, t)
		}
	}
	return Redaction{Text: text, Found: types}
}

var cardSepReplacer = strings.NewReplacer(" ", "", "-", "")

func stripCardSeparators(s string) string {
	return cardSepReplacer.Replace(s)
}

func luhnValid(digits string) bool {
	if digits == "" {
		return false
	}
	sum, alt := 0, false
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if d < 0 || d > 9 {
			return false
		}
		if alt {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return sum%10 == 0
}
