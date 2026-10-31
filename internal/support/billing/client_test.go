package billing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	apiToken = "support-api-token"
	userTok  = "user-token-0123456789"
)

const plans = `{"plans":[
 {"code":"PRO","name":"Про","priceMinor":39900,"activeIdeas":null},
 {"code":"FREE","name":"Старт","priceMinor":0,"activeIdeas":3},
 {"code":"PLUS","name":"Плюс","priceMinor":14900,"activeIdeas":15}],
 "tiers":[{"code":"PATRON","name":"Партнер","priceMinor":50000},{"code":"SUPPORTER","name":"Друг ідеї","priceMinor":5000}]}`

const snapshot = `{"user":{"handle":"olena"},
 "plan":{"code":"PRO","name":"Про","priceMinor":39900,"activeIdeas":null,"currentPeriodEnd":"2026-11-12T10:00:00Z","cancelAtPeriodEnd":false},
 "usage":{"activeIdeas":4},
 "sponsorships":[{"id":"sp-1","idea":{"slug":"sad","title":"Сад"},"tier":"BACKER","tierName":"Меценат","priceMinor":20000,"currentPeriodEnd":"2026-11-20T10:00:00Z","cancelAtPeriodEnd":true}],
 "payments":[{"id":"pay-1","kind":"PLAN","amountMinor":39900,"status":"SUCCEEDED","description":"План «Про»","createdAt":"2026-10-12T10:00:00Z"}]}`

// fakeFantasm answers like fantasm's support API and records what the bot sent.
type fakeFantasm struct {
	catalogCalls atomic.Int32
	lastAction   map[string]any
	replayed     bool
}

func (f *fakeFantasm) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/billing/plans" {
		if r.Header.Get("Authorization") != "" || r.Header.Get(UserHeader) != "" {
			http.Error(w, "the public price list must not get credentials", http.StatusBadRequest)
			return
		}
		f.catalogCalls.Add(1)
		_, _ = w.Write([]byte(plans))
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+apiToken || r.Header.Get(UserHeader) != userTok {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized"}}`))
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/integrations/support/billing":
		_, _ = w.Write([]byte(snapshot))
	case r.Method == http.MethodPost && r.URL.Path == "/api/integrations/support/billing/actions":
		_ = json.NewDecoder(r.Body).Decode(&f.lastAction)
		out := strings.Replace(snapshot, "2026-11-12", "2026-11-19", 1)
		out = strings.TrimSuffix(out, "}") + `,"replayed":` + map[bool]string{true: "true", false: "false"}[f.replayed] + `}`
		_, _ = w.Write([]byte(out))
	default:
		http.NotFound(w, r)
	}
}

func newClient(t *testing.T) (*Client, *fakeFantasm) {
	t.Helper()
	f := &fakeFantasm{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return New(srv.URL+"/", apiToken), f
}

func TestBillingReadsWhatThePersonPays(t *testing.T) {
	c, _ := newClient(t)
	b, err := c.Billing(context.Background(), userTok)
	if err != nil {
		t.Fatal(err)
	}
	if b.Handle != "olena" || b.Plan.Code != "PRO" || b.Plan.ActiveIdeas != 0 || b.ActiveIdeas != 4 {
		t.Fatalf("billing = %+v", b)
	}
	if !b.PeriodEnd.Equal(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("period end = %v", b.PeriodEnd)
	}
	if b.Plans[0].Code != "FREE" || b.Plans[2].Code != "PRO" || b.Tiers[0].Code != "SUPPORTER" {
		t.Fatalf("price list must be cheapest first: %+v / %+v", b.Plans, b.Tiers)
	}
	if len(b.Sponsorships) != 1 || !b.Sponsorships[0].CancelAtPeriodEnd || b.Sponsorships[0].IdeaSlug != "sad" {
		t.Fatalf("sponsorships = %+v", b.Sponsorships)
	}
	if len(b.Payments) != 1 || b.Payments[0].ID != "pay-1" {
		t.Fatalf("payments = %+v", b.Payments)
	}
}

func TestPriceListIsCached(t *testing.T) {
	c, f := newClient(t)
	for range 3 {
		if _, err := c.Billing(context.Background(), userTok); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.catalogCalls.Load(); n != 1 {
		t.Fatalf("price list fetched %d times, want once", n)
	}
}

func TestVisitorsAreNotLookedUp(t *testing.T) {
	c, f := newClient(t)
	b, err := c.Billing(context.Background(), "")
	if b != nil || err != nil || f.catalogCalls.Load() != 0 {
		t.Fatalf("b=%v err=%v calls=%d", b, err, f.catalogCalls.Load())
	}
	if _, err := c.ExtendPlan(context.Background(), "", "support-goodwill:olena", 7); !errors.Is(err, ErrRejected) {
		t.Fatalf("extend without identity: %v", err)
	}
}

func TestRejectedIdentity(t *testing.T) {
	c, _ := newClient(t)
	if _, err := c.Billing(context.Background(), "someone-elses-token-000"); !errors.Is(err, ErrRejected) {
		t.Fatalf("err = %v, want ErrRejected", err)
	}
}

func TestExtendPlanSendsOneIdempotentAction(t *testing.T) {
	c, f := newClient(t)
	ext, err := c.ExtendPlan(context.Background(), userTok, GoodwillActionID("olena"), 7)
	if err != nil {
		t.Fatal(err)
	}
	if f.lastAction["action"] != "EXTEND_PLAN" || f.lastAction["days"] != float64(7) || f.lastAction["actionId"] != "support-goodwill:olena" {
		t.Fatalf("sent %v", f.lastAction)
	}
	if ext.Replayed || !ext.PeriodEnd.Equal(time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("extension = %+v", ext)
	}
	f.replayed = true
	if ext, err := c.ExtendPlan(context.Background(), userTok, GoodwillActionID("olena"), 7); err != nil || !ext.Replayed {
		t.Fatalf("replay = %+v, %v", ext, err)
	}
}

func TestGoodwillActionIDFitsFantasmPattern(t *testing.T) {
	for _, handle := range []string{"olena", "Оле на/../x", strings.Repeat("a", 200)} {
		id := GoodwillActionID(handle)
		if len(id) < 8 || len(id) > 100 || unsafeActionID.MatchString(id) {
			t.Errorf("%q -> %q breaks ^[A-Za-z0-9_.:-]{8,100}$", handle, id)
		}
	}
}
