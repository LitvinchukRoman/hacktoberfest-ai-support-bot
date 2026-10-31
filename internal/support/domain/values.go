package domain

import "slices"

// Valid reports whether the intent is one the classifier may return.
func (i Intent) Valid() bool { return slices.Contains(Intents, i) }

// Emotions lists every emotion the classifier may return.
var Emotions = []Emotion{EmotionNeutral, EmotionFrustrated, EmotionAngry, EmotionConfused, EmotionSad, EmotionSatisfied}

func (e Emotion) Valid() bool { return slices.Contains(Emotions, e) }

// SignalPromptInjection is the classifier's own note that a message tries to
// change the bot's rules. guard.Tighten escalates on it.
const SignalPromptInjection = "prompt_injection_attempt"

// Values of fantasm columns that policy reads. They mirror the database checks.
const (
	IdeaDraft = "DRAFT" // ideas.status

	ModerationPending  = "PENDING" // ideas.moderation_state
	ModerationApproved = "APPROVED"
	ModerationHidden   = "HIDDEN"
	ModerationRejected = "REJECTED"

	VisibilityPublic       = "PUBLIC" // ideas.visibility
	VisibilityMembers      = "MEMBERS_ONLY"
	VisibilityOrganization = "ORGANIZATION_ONLY"

	CasePremoderation = "PREMODERATION" // moderation_cases.source
	CaseReports       = "REPORTS"

	ProviderGoogle = "google"
	ProviderEntra  = "entra"
)
