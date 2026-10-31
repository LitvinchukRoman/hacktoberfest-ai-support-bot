package support

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/handziurdmytro/skelar/internal/support/domain"
	"github.com/handziurdmytro/skelar/internal/support/guard"
	"github.com/handziurdmytro/skelar/internal/support/identity"
	"github.com/handziurdmytro/skelar/internal/support/policy"
	"github.com/handziurdmytro/skelar/internal/support/ports"
	"github.com/handziurdmytro/skelar/internal/support/render"
	fx "github.com/handziurdmytro/skelar/internal/support/supporttest"
)

// billingFacts returns one person's billing, and only to the token fantasm gave them.
type billingFacts struct {
	token string
	b     *domain.BillingFacts
}

func (f billingFacts) Facts(_ context.Context, in domain.FactsInput) (domain.Facts, error) {
	out := domain.Facts{Now: in.Now, State: in.State}
	if in.Identity == f.token {
		out.Billing = f.b
	}
	return out, nil
}

type fakeBilling struct {
	calls []string
	ext   domain.Extension
	err   error
}

func (f *fakeBilling) ExtendPlan(_ context.Context, identity, actionID string, days int) (domain.Extension, error) {
	f.calls = append(f.calls, identity+"|"+actionID+"|"+strconv.Itoa(days))
	return f.ext, f.err
}

const personToken = "person-token-0123456789"

func retentionService(t *testing.T, b *fakeBilling, mode guard.Mode) (*Service, *fakeStore, string) {
	t.Helper()
	ctx := context.Background()
	store := newFakeStore()
	userID := "user-olena"
	ticket, err := store.Create(ctx, ports.NewTicketInput{ID: "ticket-r", UserID: &userID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Insert(ctx, ports.NewMessageInput{ID: "msg-r", TicketID: ticket.ID, Author: "customer", Body: "Хочу скасувати підписку"}); err != nil {
		t.Fatal(err)
	}
	ids := identity.NewStore()
	ids.Put(ticket.ID, personToken)
	svc := &Service{
		Store: store,
		Brain: domain.Brain{
			Classifier: constClassifier{domain.Classification{Intent: domain.IntentBillingCancel, Confidence: 0.9, Language: "uk"}},
			Facts:      billingFacts{token: personToken, b: fx.ProAuthor(20)},
			Decide: func(c domain.Classification, f domain.Facts) domain.Decision {
				return policy.Decide(c, f, policy.DefaultConfig)
			},
			Renderer: render.Templater{},
		},
		Fallback:   constClassifier{domain.Classification{Intent: domain.IntentUnknown}},
		Mode:       func(context.Context) guard.Mode { return mode },
		Now:        func() time.Time { return fx.Now },
		Identities: ids,
	}
	if b != nil {
		svc.Billing = b
	}
	return svc, store, ticket.ID
}

func botReplies(store *fakeStore, ticketID string) []string {
	var out []string
	for _, id := range store.byTicket[ticketID] {
		if m := store.messages[id]; m.Author == "bot" {
			out = append(out, m.Body)
		}
	}
	return out
}

func onlyAction(t *testing.T, store *fakeStore) ports.Action {
	t.Helper()
	if len(store.actions) != 1 {
		t.Fatalf("actions = %+v, want exactly one", store.actions)
	}
	for _, a := range store.actions {
		return a
	}
	return ports.Action{}
}

func TestFreeDaysAreGivenBeforeTheReplySaysSo(t *testing.T) {
	b := &fakeBilling{ext: domain.Extension{PeriodEnd: time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)}}
	svc, store, ticketID := retentionService(t, b, guard.ModeStandard)
	if err := svc.processTicket(context.Background(), ticketID); err != nil {
		t.Fatal(err)
	}
	if len(b.calls) != 1 || b.calls[0] != personToken+"|support-goodwill:olena|7" {
		t.Fatalf("fantasm calls = %v", b.calls)
	}
	replies := botReplies(store, ticketID)
	if len(replies) != 1 || !strings.Contains(replies[0], "додав тобі 7 днів «Про»") || !strings.Contains(replies[0], "25 листопада") {
		t.Fatalf("replies = %q, want the date fantasm returned", replies)
	}
	if a := onlyAction(t, store); a.Type != domain.ActExtendPlan || a.Status != "executed" || a.Params["handle"] != "olena" {
		t.Fatalf("action = %+v", a)
	}
	if got := store.tickets[ticketID].State; got != (domain.ConversationState{Flow: domain.FlowRetention, Step: 2}) {
		t.Fatalf("state = %+v", got)
	}
}

func TestRefusedFreeDaysGoToAManager(t *testing.T) {
	for name, b := range map[string]*fakeBilling{
		"fantasm refuses":   {err: errors.New("fantasm POST: status 409")},
		"billing not wired": nil,
	} {
		t.Run(name, func(t *testing.T) {
			svc, store, ticketID := retentionService(t, b, guard.ModeStandard)
			if err := svc.processTicket(context.Background(), ticketID); err != nil {
				t.Fatal(err)
			}
			tk := store.tickets[ticketID]
			if tk.Status != domain.StatusEscalated || tk.Queue == nil || *tk.Queue != domain.QueueSupport || tk.EscalationReason != "billing_action_failed" {
				t.Fatalf("ticket = %s / %v / %s", tk.Status, tk.Queue, tk.EscalationReason)
			}
			if len(store.actions) != 0 {
				t.Fatalf("no action may be recorded for days that were not given: %+v", store.actions)
			}
			if r := botReplies(store, ticketID); len(r) != 1 || strings.Contains(r[0], "додав") || !strings.Contains(r[0], "менеджер") {
				t.Fatalf("replies = %q", r)
			}
		})
	}
}

func TestShadowModeGivesNothing(t *testing.T) {
	b := &fakeBilling{}
	svc, _, ticketID := retentionService(t, b, guard.ModeShadow)
	if err := svc.processTicket(context.Background(), ticketID); err != nil {
		t.Fatal(err)
	}
	if len(b.calls) != 0 {
		t.Fatalf("shadow mode called fantasm: %v", b.calls)
	}
}

func TestApprovedFreeDays(t *testing.T) {
	b := &fakeBilling{}
	svc, store, ticketID := retentionService(t, b, guard.ModeApproveAll)
	if err := svc.processTicket(context.Background(), ticketID); err != nil {
		t.Fatal(err)
	}
	if len(b.calls) != 0 {
		t.Fatalf("nothing may be given before a colleague approves: %v", b.calls)
	}
	r := botReplies(store, ticketID)
	if len(r) != 1 || !strings.Contains(r[0], "попросив колегу") {
		t.Fatalf("the person must hear the days are being asked for: %q", r)
	}
	a := onlyAction(t, store)
	if a.Status != "pending" || !strings.Contains(a.DraftReply, "додав тобі 7 днів") {
		t.Fatalf("action = %+v", a)
	}

	if err := svc.Approve(context.Background(), a.ID, "staff-1", ""); err != nil {
		t.Fatal(err)
	}
	if len(b.calls) != 1 || !strings.HasPrefix(b.calls[0], personToken+"|support-goodwill:olena|") {
		t.Fatalf("approval must give the days with the person's token: %v", b.calls)
	}
	if r := botReplies(store, ticketID); len(r) != 2 || !strings.Contains(r[1], "додав тобі 7 днів") {
		t.Fatalf("replies after approval = %q", r)
	}
}

func TestApprovalFailsWhenThePersonsTokenIsGone(t *testing.T) {
	b := &fakeBilling{}
	svc, store, ticketID := retentionService(t, b, guard.ModeApproveAll)
	if err := svc.processTicket(context.Background(), ticketID); err != nil {
		t.Fatal(err)
	}
	b.err = errors.New("rejected")
	svc.Identities.Put(ticketID, "")
	a := onlyAction(t, store)
	if err := svc.Approve(context.Background(), a.ID, "staff-1", ""); !errors.Is(err, ErrBillingUnavailable) {
		t.Fatalf("err = %v, want ErrBillingUnavailable", err)
	}
	if r := botReplies(store, ticketID); len(r) != 1 {
		t.Fatalf("no reply may claim the days were given: %q", r)
	}
}
