package guard

import (
	"regexp"
	"slices"
)

// Signal is a safety-relevant pattern found in the raw (pre-redaction) text.
// Detect runs before the classifier and its output can only ever push a
// decision towards a human (see Tighten) — it never decides anything itself.
type Signal string

const (
	SignalSelfHarm    Signal = "self_harm"
	SignalLegal       Signal = "legal"
	SignalDataRequest Signal = "data_request"
	SignalInjection   Signal = "prompt_injection"
	SignalHuman       Signal = "human_request"
)

// Go's \b is ASCII-only, so for Cyrillic we match literal phrases instead of
// relying on word boundaries.
var detectors = []struct {
	signal Signal
	re     *regexp.Regexp
}{
	{SignalSelfHarm, regexp.MustCompile(`(?i)не хочу жити|покінчити з собою|суїцид|вбити себе|kill myself|suicide`)},
	{SignalLegal, regexp.MustCompile(`(?i)адвокат|юрист|до суду|в суд|позов|прокуратур|поліці|омбудсмен|lawyer|lawsuit`)},
	{SignalDataRequest, regexp.MustCompile(`(?i)видал\p{L}*\s*(мій|мою|мої)?\s*(акаунт|профіль|дані)|які дані\s.*зберіга|копі\p{L}*\s*даних|право на забуття|персональн\p{L}*\s*дан|gdpr`)},
	{SignalInjection, regexp.MustCompile(`(?i)ignore\s*(previous\s*)?instructions|ігноруй\s*(попередні\s*)?інструкції|забудь правила|system prompt|системн\w*\s*промпт|ти\s*(тепер\s*)?адмін|you are\s*(now\s*)?admin|developer mode|jailbreak`)},
	{SignalHuman, regexp.MustCompile(`(?i)з\s*(живою\s*)?людиною|жив\w*\s*оператор|покличте людину|оператора|talk to a human`)},
}

// Detect scans the raw, pre-redaction text for safety signals. It never
// stores or returns the text itself — only the set of signals found, which
// get written into the trace/job payload.
func Detect(text string) []Signal {
	var found []Signal
	for _, d := range detectors {
		if d.re.MatchString(text) && !slices.Contains(found, d.signal) {
			found = append(found, d.signal)
		}
	}
	return found
}
