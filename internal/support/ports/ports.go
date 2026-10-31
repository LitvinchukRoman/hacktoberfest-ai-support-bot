// Package ports declares what the orchestrator, executor and HTTP handlers
// need from storage, independent of any driver. internal/support/adapters/postgres
// implements these against Postgres.
package ports

import (
	"context"
	"encoding/json"
	"time"

	"github.com/handziurdmytro/skelar/internal/support/domain"
)

// Ticket mirrors the support_tickets row.
type Ticket struct {
	ID               string
	UserID           *string
	AnonKeyHash      []byte
	ContactEmail     string
	Status           domain.TicketStatus
	Autonomy         *domain.Autonomy
	Intent           domain.Intent
	Emotion          domain.Emotion
	Language         string
	Priority         int
	Queue            *domain.Queue
	EscalationReason string
	IdeaID           *string
	Page             string
	State            domain.ConversationState
	LastSeq          int64
	FirstReplyAt     *time.Time
	SolvedAt         *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Owner reports whether userID/anonKeyHash owns this ticket. A ticket always
// has exactly one owner kind (support_tickets_owner check constraint).
func (t Ticket) OwnedBy(userID string, anonKeyHash []byte) bool {
	if t.UserID != nil && userID != "" {
		return *t.UserID == userID
	}
	if len(t.AnonKeyHash) > 0 && len(anonKeyHash) > 0 {
		return string(t.AnonKeyHash) == string(anonKeyHash)
	}
	return false
}

// Message mirrors the support_messages row. Body is always post-redaction —
// no caller of this package should ever construct one from raw text.
type Message struct {
	ID          string
	TicketID    string
	Author      string // customer | bot | agent
	AgentID     *string
	Visibility  string // public | internal
	Body        string
	Actions     json.RawMessage // []domain.SuggestedAction, as stored
	PIIFound    []string
	ProcessedAt *time.Time
	CreatedAt   time.Time
}

// Event mirrors the support_events row: Staff is the full payload for
// personnel, Public is the trace-without-data (or nil) version the customer
// receives.
type Event struct {
	TicketID  string
	Seq       int64
	Type      string // trace | message | status
	Staff     json.RawMessage
	Public    json.RawMessage // nil => staff-only event
	CreatedAt time.Time
}

// NewTicketInput is what intake needs to create a ticket.
type NewTicketInput struct {
	ID           string
	UserID       *string
	AnonKeyHash  []byte
	ContactEmail string
	Page         string
}

// NewMessageInput is what intake needs to append a customer message to an
// existing or brand-new ticket, inside a single transaction with the
// enqueue and the trace event.
type NewMessageInput struct {
	ID       string
	TicketID string
	Author   string
	Body     string // already redacted
	PIIFound []string
}

type TicketRepo interface {
	Create(ctx context.Context, in NewTicketInput) (Ticket, error)
	Get(ctx context.Context, id string) (Ticket, bool, error)
	// ListInbox returns staff-facing tickets, newest-priority first, cursor-paginated.
	ListInbox(ctx context.Context, status string, cursor string, limit int) ([]Ticket, string, error)
	UpdateAfterDecision(ctx context.Context, t Ticket) error
}

type MessageRepo interface {
	Insert(ctx context.Context, in NewMessageInput) (Message, error)
	// Unprocessed returns customer messages not yet picked up by the
	// worker, oldest first, so a burst of 3 quick messages is handled together.
	Unprocessed(ctx context.Context, ticketID string) ([]Message, error)
	MarkProcessed(ctx context.Context, ids []string) error
	ForTicket(ctx context.Context, ticketID string, includeInternal bool) ([]Message, error)
}

type EventRepo interface {
	// Append assigns the next seq for the ticket (via support_tickets.last_seq)
	// and inserts the event in the same transaction as the caller's other writes.
	Append(ctx context.Context, ticketID string, typ string, staff, public json.RawMessage) (Event, error)
	After(ctx context.Context, ticketID string, seq int64) ([]Event, error)
}

// Job mirrors support_jobs, surfaced only for tests/metrics — service.go
// talks to JobQueue, not this struct, during normal operation.
type Job struct {
	TicketID    string
	State       string
	Dirty       bool
	Attempts    int
	RunAfter    time.Time
	LockedUntil *time.Time
	LastError   string
}

type JobQueue interface {
	// Enqueue marks the ticket's job queued, or dirty if it's already running.
	Enqueue(ctx context.Context, ticketID string) error
	// Claim picks up to n due jobs with FOR UPDATE SKIP LOCKED.
	Claim(ctx context.Context, n int) ([]Job, error)
	// Finish deletes the job unless it went dirty while running, in which
	// case it's requeued immediately for one more pass.
	Finish(ctx context.Context, ticketID string) error
	// Fail requeues the job after backoff with a short error code.
	Fail(ctx context.Context, ticketID string, backoff time.Duration, errCode string) error
}

// TxManager runs fn inside a single database transaction. Repos pick the
// active transaction up from ctx, so WithinTx composes freely: a handler
// can call Tickets().Create, Messages().Insert and Events().Append inside
// one WithinTx and get one atomic commit.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}
