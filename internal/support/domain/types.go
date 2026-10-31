// Package domain holds the contract shared between Backend 1 (AI core) and
// Backend 2 (Ops): classification, decisions, facts and the Brain interfaces.
// Backend 2 depends on these types but does not decide product logic itself —
// it only tightens and guards what domain.Brain produces.
package domain

import (
	"context"
	"time"
)

type Intent string

const (
	IntentIdeaVisibility     Intent = "idea_visibility"
	IntentModerationAppeal   Intent = "moderation_appeal"
	IntentVerificationSeal   Intent = "verification_seal"
	IntentLoginProblem       Intent = "login_problem"
	IntentNotificationsIssue Intent = "notifications_issue"
	IntentHowTo              Intent = "how_to"
	IntentRoleRequest        Intent = "role_request"
	IntentReportContent      Intent = "report_content"
	IntentBugReport          Intent = "bug_report"
	IntentThanksResolved     Intent = "thanks_resolved"
	IntentAccountData        Intent = "account_data"
	IntentHumanRequest       Intent = "human_request"
	IntentLegal              Intent = "legal"
	IntentOffensive          Intent = "offensive"
	IntentUnknown            Intent = "unknown"
)

// Intents enumerates every known intent, used to validate classifier output
// (an intent outside this set must be treated as IntentUnknown).
var Intents = []Intent{
	IntentIdeaVisibility, IntentModerationAppeal, IntentVerificationSeal, IntentLoginProblem,
	IntentNotificationsIssue, IntentHowTo, IntentRoleRequest, IntentReportContent, IntentBugReport,
	IntentThanksResolved, IntentAccountData, IntentHumanRequest, IntentLegal, IntentOffensive, IntentUnknown,
}

type Emotion string

const (
	EmotionNeutral    Emotion = "neutral"
	EmotionFrustrated Emotion = "frustrated"
	EmotionAngry      Emotion = "angry"
	EmotionConfused   Emotion = "confused"
	EmotionSad        Emotion = "sad"
	EmotionSatisfied  Emotion = "satisfied"
)

type Classification struct {
	Intent        Intent   `json:"intent"`
	Emotion       Emotion  `json:"emotion"`
	Language      string   `json:"language"`
	Confidence    float64  `json:"confidence"`
	IdeaHint      string   `json:"ideaHint"`
	ErrorCode     string   `json:"errorCode"`
	UserQuestions []string `json:"userQuestions"`
	Signals       []string `json:"signals"`
	Reasoning     string   `json:"reasoning"`
}

type Autonomy string

const (
	AutonomyAuto     Autonomy = "auto"
	AutonomyApprove  Autonomy = "approve"
	AutonomyEscalate Autonomy = "escalate"
)

// autonomyRank orders autonomy levels from least to most restrictive, so that
// guard.Tighten can tell whether a candidate decision moves towards or away
// from a human.
var autonomyRank = map[Autonomy]int{
	AutonomyAuto:     0,
	AutonomyApprove:  1,
	AutonomyEscalate: 2,
}

// Stricter returns whichever of a, b sits closer to a human in the chain
// auto -> approve -> escalate. Unknown values are treated as the strictest.
func Stricter(a, b Autonomy) Autonomy {
	ra, okA := autonomyRank[a]
	rb, okB := autonomyRank[b]
	if !okA {
		return a
	}
	if !okB {
		return b
	}
	if ra >= rb {
		return a
	}
	return b
}

type TicketStatus string

const (
	StatusProcessing       TicketStatus = "processing"
	StatusAwaitingCustomer TicketStatus = "awaiting_customer"
	StatusAwaitingApproval TicketStatus = "awaiting_approval"
	StatusEscalated        TicketStatus = "escalated"
	StatusSolved           TicketStatus = "solved"
)

type ActionType string

const (
	ActRestoreIdea ActionType = "restore_idea"
	ActNotifyStaff ActionType = "notify_staff"
	ActBugReport   ActionType = "bug_report"
	ActDataRequest ActionType = "data_request"
)

type Queue string

const (
	QueueModerators Queue = "moderators"
	QueueAdmins     Queue = "admins"
)

type Action struct {
	Type   ActionType        `json:"type"`
	IdeaID string            `json:"ideaId,omitempty"`
	Queue  Queue             `json:"queue,omitempty"`
	Note   string            `json:"note,omitempty"`
	Params map[string]string `json:"params,omitempty"`
}

type VerdictKind string

const (
	VerdictNotABug        VerdictKind = "not_a_bug"
	VerdictBug            VerdictKind = "bug"
	VerdictSuspectedAbuse VerdictKind = "suspected_abuse"
	VerdictUserSide       VerdictKind = "user_side"
	VerdictUnclear        VerdictKind = "unclear"
)

type Verdict struct {
	Kind        VerdictKind `json:"kind"`
	Evidence    []string    `json:"evidence"`
	Explanation string      `json:"explanation"`
}

type Escalation struct {
	Reason   string `json:"reason"`
	Queue    Queue  `json:"queue"`
	Priority int    `json:"priority"` // 1 urgent ... 3 routine
	Note     string `json:"note,omitempty"`
}

type Flow string

const (
	FlowNone   Flow = ""
	FlowAppeal Flow = "appeal"
)

type ConversationState struct {
	Flow Flow `json:"flow"`
	Step int  `json:"step"`
}

// SuggestedAction is a button shown to the customer; To must be an internal
// site path only (never an external URL).
type SuggestedAction struct {
	Label string `json:"label"`
	To    string `json:"to"`
}

type Decision struct {
	Autonomy   Autonomy          `json:"autonomy"`
	Rule       string            `json:"rule"`
	Verdict    *Verdict          `json:"verdict,omitempty"`
	Actions    []Action          `json:"actions,omitempty"`
	TemplateID string            `json:"templateId,omitempty"`
	Slots      map[string]string `json:"slots,omitempty"`
	Suggested  []SuggestedAction `json:"suggested,omitempty"`
	Status     TicketStatus      `json:"status"`
	NextState  ConversationState `json:"nextState"`
	Escalation *Escalation       `json:"escalation,omitempty"`
}

type UserFacts struct {
	ID            string
	Handle        string
	Role          string
	Provider      string // entra | google
	TenantMatches bool
	UKMAEmail     bool
	Verified      bool
	ApprovedIdeas int
	IdeasToday    int
	CreatedAt     time.Time
}

type IdeaFacts struct {
	ID             string
	Slug           string
	Title          string
	Status         string // DRAFT | OPEN | ... (ideas.status); a draft is not waiting for anyone
	Moderation     string // PENDING | APPROVED | HIDDEN | REJECTED
	Visibility     string // PUBLIC | MEMBERS_ONLY | ORGANIZATION_ONLY
	CreatedAt      time.Time
	CaseSource     string
	CaseOpenedAt   time.Time
	DecidedByHuman bool
	DecisionNote   string
	OpenReports    int
}

// ReportFacts deliberately omits the reporter's identity (GDPR data
// minimisation) — only age of account, trust signal and timing survive.
type ReportFacts struct {
	ReporterAge      time.Duration
	ReporterApproved int
	CreatedAt        time.Time
}

type PushFacts struct {
	Subscriptions int
	Stuck         int
}

type KBHit struct {
	Path    string
	Title   string
	Snippet string
	Score   float64
}

type Facts struct {
	Now           time.Time
	User          *UserFacts
	Idea          *IdeaFacts
	Reports       []ReportFacts
	Push          *PushFacts
	Knowledge     []KBHit
	State         ConversationState
	RecentTickets int
}

type Usage struct {
	Model            string
	PromptTokens     int
	CompletionTokens int
	LatencyMS        int
}

type Turn struct {
	Author string // customer | bot | agent
	Text   string
}

type ClassifyInput struct {
	Text    string
	History []Turn
	State   ConversationState
	Page    string
}

type FactsInput struct {
	UserID   string
	IdeaSlug string
	IdeaHint string
	Intent   Intent
	Question string
	State    ConversationState
	Now      time.Time
}

type RenderInput struct {
	TemplateID string
	Slots      map[string]string
	Language   string
	Emotion    Emotion
	Question   string
	Knowledge  []KBHit
}

type Classifier interface {
	Classify(ctx context.Context, in ClassifyInput) (Classification, Usage, error)
}

type FactsSource interface {
	Facts(ctx context.Context, in FactsInput) (Facts, error)
}

type Renderer interface {
	Render(ctx context.Context, in RenderInput) (string, Usage, error)
}

// Brain is everything Backend 1 hands to the Backend 2 orchestrator.
// Decide is a pure function: no I/O, no model, no system clock.
type Brain struct {
	Classifier Classifier
	Facts      FactsSource
	Decide     func(Classification, Facts) Decision
	Renderer   Renderer
}
