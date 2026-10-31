package postgres

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/handziurdmytro/skelar/internal/support/domain"
	"github.com/handziurdmytro/skelar/internal/support/ports"
)

// testStore connects to TEST_DATABASE_URL (migrations already applied by
// the caller, e.g. via `migrate ... up`) and truncates every support_* and
// fixture table so each test starts from a clean slate.
func testStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set, skipping Postgres integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `TRUNCATE support_feedback, support_data_requests, support_actions,
		support_decisions, support_jobs, support_events, support_messages, support_tickets,
		ideas, users CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return New(pool), ctx
}

func seedUser(t *testing.T, ctx context.Context, s *Store) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO users (id, handle) VALUES ($1, $2)`, id, "u-"+id[:8]); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func TestCreateAndGetTicket(t *testing.T) {
	s, ctx := testStore(t)
	userID := seedUser(t, ctx, s)

	in := ports.NewTicketInput{ID: uuid.NewString(), UserID: &userID, Page: "/ideas/x"}
	created, err := s.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Status != "processing" || created.Priority != 3 || created.Page != "/ideas/x" {
		t.Fatalf("unexpected defaults: %+v", created)
	}
	if created.Autonomy != nil {
		t.Fatalf("expected nil autonomy, got %v", *created.Autonomy)
	}

	got, ok, err := s.Get(ctx, in.ID)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.ID != created.ID || *got.UserID != userID {
		t.Fatalf("Get mismatch: %+v", got)
	}

	_, ok, err = s.Get(ctx, uuid.NewString())
	if err != nil || ok {
		t.Fatalf("Get for missing ticket: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

func TestCreateTicketWithoutOwnerFails(t *testing.T) {
	s, ctx := testStore(t)
	_, err := s.Create(ctx, ports.NewTicketInput{ID: uuid.NewString()})
	if err == nil {
		t.Fatal("expected an error: a ticket needs either user_id or anon_key_hash")
	}
}

func TestUpdateAfterDecision(t *testing.T) {
	s, ctx := testStore(t)
	userID := seedUser(t, ctx, s)
	ticket, err := s.Create(ctx, ports.NewTicketInput{ID: uuid.NewString(), UserID: &userID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	auto := ports.Ticket{
		ID:       ticket.ID,
		Status:   "awaiting_approval",
		Intent:   "moderation_appeal",
		Emotion:  "angry",
		Priority: 2,
	}
	queue := domain.QueueModerators
	auto.Queue = &queue
	now := time.Now().UTC().Truncate(time.Microsecond)
	auto.FirstReplyAt = &now

	if err := s.UpdateAfterDecision(ctx, auto); err != nil {
		t.Fatalf("UpdateAfterDecision: %v", err)
	}

	got, _, err := s.Get(ctx, ticket.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "awaiting_approval" || got.Priority != 2 || got.Queue == nil || *got.Queue != queue {
		t.Fatalf("unexpected ticket after update: %+v", got)
	}
	if got.FirstReplyAt == nil || !got.FirstReplyAt.Equal(now) {
		t.Fatalf("first_reply_at not set: %+v", got.FirstReplyAt)
	}

	// A second update must not move first_reply_at even if a new, later
	// timestamp is passed: it's set-once.
	later := now.Add(time.Hour)
	auto.FirstReplyAt = &later
	auto.Status = "solved"
	if err := s.UpdateAfterDecision(ctx, auto); err != nil {
		t.Fatalf("UpdateAfterDecision (2nd): %v", err)
	}
	got2, _, _ := s.Get(ctx, ticket.ID)
	if !got2.FirstReplyAt.Equal(now) {
		t.Fatalf("first_reply_at moved: got %v, want %v", got2.FirstReplyAt, now)
	}
	if got2.SolvedAt == nil {
		t.Fatalf("solved_at not set when status became solved")
	}
}

func TestMessagesUnprocessedAndVisibility(t *testing.T) {
	s, ctx := testStore(t)
	userID := seedUser(t, ctx, s)
	ticket, err := s.Create(ctx, ports.NewTicketInput{ID: uuid.NewString(), UserID: &userID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	m1, err := s.Insert(ctx, ports.NewMessageInput{ID: uuid.NewString(), TicketID: ticket.ID, Author: "customer", Body: "[EMAIL] привіт", PIIFound: []string{"email"}})
	if err != nil {
		t.Fatalf("Insert m1: %v", err)
	}
	m2, err := s.Insert(ctx, ports.NewMessageInput{ID: uuid.NewString(), TicketID: ticket.ID, Author: "customer", Body: "ще питання"})
	if err != nil {
		t.Fatalf("Insert m2: %v", err)
	}

	unprocessed, err := s.Unprocessed(ctx, ticket.ID)
	if err != nil {
		t.Fatalf("Unprocessed: %v", err)
	}
	if len(unprocessed) != 2 || unprocessed[0].ID != m1.ID || unprocessed[1].ID != m2.ID {
		t.Fatalf("unexpected unprocessed order: %+v", unprocessed)
	}
	if len(unprocessed[0].PIIFound) != 1 || unprocessed[0].PIIFound[0] != "email" {
		t.Fatalf("pii_found not round-tripped: %+v", unprocessed[0])
	}

	if err := s.MarkProcessed(ctx, []string{m1.ID, m2.ID}); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}
	unprocessed, err = s.Unprocessed(ctx, ticket.ID)
	if err != nil || len(unprocessed) != 0 {
		t.Fatalf("expected no unprocessed messages left, got %v err=%v", unprocessed, err)
	}

	// internal note from an agent is hidden from the customer's view.
	agentID := seedUser(t, ctx, s)
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO support_messages (id, ticket_id, author, agent_id, visibility, body)
		VALUES ($1, $2, 'agent', $3, 'internal', 'нотатка для персоналу')`,
		uuid.NewString(), ticket.ID, agentID); err != nil {
		t.Fatalf("seed internal note: %v", err)
	}

	customerView, err := s.ForTicket(ctx, ticket.ID, false)
	if err != nil {
		t.Fatalf("ForTicket(customer): %v", err)
	}
	if len(customerView) != 2 {
		t.Fatalf("customer must not see internal notes, got %d messages", len(customerView))
	}

	staffView, err := s.ForTicket(ctx, ticket.ID, true)
	if err != nil {
		t.Fatalf("ForTicket(staff): %v", err)
	}
	if len(staffView) != 3 {
		t.Fatalf("staff must see internal notes too, got %d messages", len(staffView))
	}
}

func TestEventAppendAssignsMonotonicSeq(t *testing.T) {
	s, ctx := testStore(t)
	userID := seedUser(t, ctx, s)
	ticket, err := s.Create(ctx, ports.NewTicketInput{ID: uuid.NewString(), UserID: &userID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	staffPayload, _ := json.Marshal(map[string]any{"stage": "classify", "data": map[string]any{"intent": "how_to"}})
	e1, err := s.Append(ctx, ticket.ID, "trace", staffPayload, json.RawMessage(`{"stage":"classify"}`))
	if err != nil {
		t.Fatalf("Append e1: %v", err)
	}
	e2, err := s.Append(ctx, ticket.ID, "status", staffPayload, nil)
	if err != nil {
		t.Fatalf("Append e2: %v", err)
	}
	if e1.Seq != 1 || e2.Seq != 2 {
		t.Fatalf("expected seq 1 then 2, got %d then %d", e1.Seq, e2.Seq)
	}
	if e2.Public != nil {
		t.Fatalf("staff-only event leaked a Public payload: %s", e2.Public)
	}

	got, _, err := s.Get(ctx, ticket.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.LastSeq != 2 {
		t.Fatalf("ticket.last_seq = %d, want 2", got.LastSeq)
	}

	after, err := s.After(ctx, ticket.ID, 1)
	if err != nil {
		t.Fatalf("After: %v", err)
	}
	if len(after) != 1 || after[0].Seq != 2 {
		t.Fatalf("After(seq=1) = %+v, want just seq 2", after)
	}
}

func TestJobQueueLifecycle(t *testing.T) {
	s, ctx := testStore(t)
	userID := seedUser(t, ctx, s)
	ticket, err := s.Create(ctx, ports.NewTicketInput{ID: uuid.NewString(), UserID: &userID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.Enqueue(ctx, ticket.ID); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	claimed, err := s.Claim(ctx, 10)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].TicketID != ticket.ID || claimed[0].Attempts != 1 {
		t.Fatalf("unexpected claim: %+v", claimed)
	}

	// Still locked (running, locked_until in the future) -> nothing to claim.
	again, err := s.Claim(ctx, 10)
	if err != nil {
		t.Fatalf("Claim (2nd): %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("expected no claimable jobs while running, got %+v", again)
	}

	// A message arriving mid-processing marks the job dirty.
	if err := s.Enqueue(ctx, ticket.ID); err != nil {
		t.Fatalf("Enqueue while running: %v", err)
	}

	if err := s.Finish(ctx, ticket.ID); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	// Dirty -> requeued immediately, claimable right away.
	requeued, err := s.Claim(ctx, 10)
	if err != nil {
		t.Fatalf("Claim after dirty finish: %v", err)
	}
	if len(requeued) != 1 {
		t.Fatalf("expected the dirty job to be requeued and claimable, got %+v", requeued)
	}

	// Clean finish deletes the job row entirely.
	if err := s.Finish(ctx, ticket.ID); err != nil {
		t.Fatalf("Finish (clean): %v", err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM support_jobs WHERE ticket_id = $1`, ticket.ID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected job row deleted after clean finish, found %d", count)
	}
}

func TestJobFailBacksOff(t *testing.T) {
	s, ctx := testStore(t)
	userID := seedUser(t, ctx, s)
	ticket, err := s.Create(ctx, ports.NewTicketInput{ID: uuid.NewString(), UserID: &userID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Enqueue(ctx, ticket.ID); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := s.Claim(ctx, 10); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	before := time.Now().UTC()
	if err := s.Fail(ctx, ticket.ID, 2*time.Second, "llm_timeout"); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	var state string
	var runAfter time.Time
	var lastError string
	if err := s.pool.QueryRow(ctx, `SELECT state, run_after, last_error FROM support_jobs WHERE ticket_id = $1`, ticket.ID).
		Scan(&state, &runAfter, &lastError); err != nil {
		t.Fatalf("query job: %v", err)
	}
	if state != "queued" {
		t.Fatalf("state = %q, want queued", state)
	}
	if lastError != "llm_timeout" {
		t.Fatalf("last_error = %q, want llm_timeout", lastError)
	}
	if !runAfter.After(before.Add(time.Second)) {
		t.Fatalf("run_after = %v, want at least 2s after %v", runAfter, before)
	}
}

func TestWithinTxRollsBackOnError(t *testing.T) {
	s, ctx := testStore(t)
	userID := seedUser(t, ctx, s)
	ticketID := uuid.NewString()

	err := s.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := s.Create(ctx, ports.NewTicketInput{ID: ticketID, UserID: &userID}); err != nil {
			return err
		}
		if _, err := s.Insert(ctx, ports.NewMessageInput{ID: uuid.NewString(), TicketID: ticketID, Author: "customer", Body: "привіт"}); err != nil {
			return err
		}
		return errAbort
	})
	if err != errAbort {
		t.Fatalf("WithinTx returned %v, want errAbort", err)
	}

	_, ok, err := s.Get(ctx, ticketID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Fatalf("ticket should not exist after rollback")
	}
}

func TestListInboxPaginatesByPriority(t *testing.T) {
	s, ctx := testStore(t)
	userID := seedUser(t, ctx, s)

	for i := range 5 {
		tk, err := s.Create(ctx, ports.NewTicketInput{ID: uuid.NewString(), UserID: &userID})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		pr := 3 - (i % 3)
		q := domain.QueueModerators
		if err := s.UpdateAfterDecision(ctx, ports.Ticket{ID: tk.ID, Status: "escalated", Priority: pr, Queue: &q}); err != nil {
			t.Fatalf("UpdateAfterDecision: %v", err)
		}
	}

	page1, cursor1, err := s.ListInbox(ctx, "escalated", "", 2)
	if err != nil || len(page1) != 2 || cursor1 == "" {
		t.Fatalf("page1: %v len=%d cursor=%q", err, len(page1), cursor1)
	}
	page2, cursor2, err := s.ListInbox(ctx, "escalated", cursor1, 2)
	if err != nil || len(page2) != 2 || cursor2 == "" {
		t.Fatalf("page2: %v len=%d cursor=%q", err, len(page2), cursor2)
	}
	page3, cursor3, err := s.ListInbox(ctx, "escalated", cursor2, 2)
	if err != nil || len(page3) != 1 || cursor3 != "" {
		t.Fatalf("page3: %v len=%d cursor=%q", err, len(page3), cursor3)
	}

	seen := map[string]bool{}
	for _, tk := range append(append(page1, page2...), page3...) {
		if seen[tk.ID] {
			t.Fatalf("duplicate ticket across pages: %s", tk.ID)
		}
		seen[tk.ID] = true
	}
	if page1[0].Priority > page1[1].Priority {
		t.Fatalf("page1 not ascending by priority: %+v", page1)
	}
}

func TestWithinTxNestsIntoOneCommit(t *testing.T) {
	s, ctx := testStore(t)
	userID := seedUser(t, ctx, s)
	ticketID := uuid.NewString()

	err := s.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := s.Create(ctx, ports.NewTicketInput{ID: ticketID, UserID: &userID}); err != nil {
			return err
		}
		// Nested WithinTx must join the same transaction, not open a second one.
		return s.WithinTx(ctx, func(ctx context.Context) error {
			_, err := s.Insert(ctx, ports.NewMessageInput{ID: uuid.NewString(), TicketID: ticketID, Author: "customer", Body: "привіт"})
			return err
		})
	})
	if err != nil {
		t.Fatalf("WithinTx: %v", err)
	}

	msgs, err := s.ForTicket(ctx, ticketID, true)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("ForTicket: %v, got %d messages", err, len(msgs))
	}
}

// errAbort is a sentinel used only to force WithinTx's rollback path.
var errAbort = &abortError{}

type abortError struct{}

func (*abortError) Error() string { return "abort for test" }
