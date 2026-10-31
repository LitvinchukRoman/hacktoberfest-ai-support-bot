package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/handziurdmytro/skelar/internal/support"
	"github.com/handziurdmytro/skelar/internal/support/adapters/httpapi"
	"github.com/handziurdmytro/skelar/internal/support/adapters/postgres"
	"github.com/handziurdmytro/skelar/internal/support/brain"
	"github.com/handziurdmytro/skelar/internal/support/facts"
	"github.com/handziurdmytro/skelar/internal/support/guard"
	"github.com/handziurdmytro/skelar/internal/support/internal/dbtest"
	"github.com/handziurdmytro/skelar/internal/support/kb"
	"github.com/handziurdmytro/skelar/internal/support/llm"
)

// Each case owns a database schema; failures are kept visible, not skipped.
func TestOPS2Audit(t *testing.T) {
	for _, scenario := range []string{"validation", "rate_limit", "staff_access", "gdpr", "handoff_followup", "legal", "self_harm", "shadow", "approve_all"} {
		t.Run(scenario, func(t *testing.T) {
			pool := dbtest.Pool(t)
			store := postgres.New(pool)
			guides, err := kb.Default()
			if err != nil {
				t.Fatal(err)
			}
			b, err := brain.New(t.Context(), brain.Config{Fake: true}, facts.NewPostgres(pool, guides))
			if err != nil {
				t.Fatal(err)
			}
			svc := &support.Service{Store: store, Brain: b, Fallback: llm.Fake{}}
			intake := &httpapi.Intake{Store: store, Limiter: httpapi.NewLimiter()}
			admin := &httpapi.Admin{Store: store, Service: svc}
			mux := http.NewServeMux()
			mux.HandleFunc("POST /api/support/messages", intake.PostMessage)
			mux.HandleFunc("GET /api/support/tickets/{id}", intake.GetTicket)
			mux.HandleFunc("GET /api/support/admin/tickets", admin.ListTickets)
			var cookies []*http.Cookie
			call := func(method, path, body string, owner bool, viewer *httpapi.Viewer) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				if owner {
					for _, c := range cookies {
						r.AddCookie(c)
					}
				}
				if viewer != nil {
					r = r.WithContext(httpapi.WithViewer(r.Context(), *viewer))
				}
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				t.Logf("%s %s INPUT=%s => HTTP %d %s", method, path, body, w.Code, w.Body.String())
				return w
			}
			post := func(text, id string) string {
				t.Helper()
				payload := map[string]string{"text": text, "page": "/ideas"}
				if id != "" {
					payload["ticketId"] = id
				}
				body, _ := json.Marshal(payload)
				w := call("POST", "/api/support/messages", string(body), true, nil)
				if w.Code != 202 {
					t.Fatalf("want 202, got %d", w.Code)
				}
				if len(w.Result().Cookies()) > 0 {
					cookies = w.Result().Cookies()
				}
				var receipt struct{ TicketID string }
				if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil {
					t.Fatal(err)
				}
				return receipt.TicketID
			}
			process := func(id, status string) {
				t.Helper()
				if err := svc.ProcessDue(t.Context()); err != nil {
					t.Fatal(err)
				}
				w := call("GET", "/api/support/tickets/"+id, "", true, nil)
				var snapshot struct{ Status string }
				if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
					t.Fatal(err)
				}
				if w.Code != 200 || snapshot.Status != status {
					t.Errorf("want status=%s; got HTTP %d status=%s", status, w.Code, snapshot.Status)
				}
			}
			switch scenario {
			case "validation":
				for _, tc := range []struct{ name, body string }{
					{"empty", `{"text":""}`}, {"malformed", `{`},
					{"email", `{"text":"hello","email":"bad"}`},
					{"long_text", `{"text":"` + strings.Repeat("я", 2001) + `"}`},
					{"long_page", `{"text":"hello","page":"` + strings.Repeat("x", 301) + `"}`},
				} {
					t.Run(tc.name, func(t *testing.T) {
						if w := call("POST", "/api/support/messages", tc.body, false, nil); w.Code != 400 {
							t.Errorf("want 400, got %d", w.Code)
						}
					})
				}
			case "rate_limit":
				for i := 1; i <= 7; i++ {
					w := call("POST", "/api/support/messages", `{"text":"Дякую"}`, false, nil)
					want := 202
					if i == 7 {
						want = 429
					}
					if w.Code != want {
						t.Errorf("request %d: want %d got %d", i, want, w.Code)
					}
				}
			case "staff_access":
				for _, tc := range []struct {
					role string
					want int
				}{{"", 401}, {"USER", 403}, {"MODERATOR", 200}, {"ADMIN", 200}} {
					t.Run(tc.role, func(t *testing.T) {
						var v *httpapi.Viewer
						if tc.role != "" {
							v = &httpapi.Viewer{ID: "test-viewer", Role: tc.role}
						}
						if w := call("GET", "/api/support/admin/tickets", "", false, v); w.Code != tc.want {
							t.Errorf("want %d got %d", tc.want, w.Code)
						}
					})
				}
			case "gdpr":
				id := post("Видаліть мої персональні дані та мій акаунт.", "")
				process(id, "awaiting_approval")
				var actions, requests int
				if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM support_actions WHERE ticket_id=$1 AND type='data_request'), (SELECT count(*) FROM support_data_requests WHERE ticket_id=$1)`, id).Scan(&actions, &requests); err != nil {
					t.Fatal(err)
				}
				t.Logf("DB: data_request actions=%d; registered requests=%d", actions, requests)
				if actions != 1 || requests != 1 {
					t.Errorf("GDPR request must be registered: actions=%d requests=%d", actions, requests)
				}
			case "handoff_followup":
				id := post("Хочу поговорити з живою людиною.", "")
				process(id, "escalated")
				post("Дякую", id)
				process(id, "escalated")
			case "legal", "self_harm":
				input := "Мій адвокат подасть позов до суду."
				if scenario == "self_harm" {
					input = "Я не хочу жити"
				}
				id := post(input, "")
				process(id, "escalated")
				ticket, ok, err := store.Get(t.Context(), id)
				if err != nil || !ok {
					t.Fatalf("load ticket: %v", err)
				}
				t.Logf("Routing: priority=%d queue=%v reason=%s", ticket.Priority, ticket.Queue, ticket.EscalationReason)
				if ticket.Priority != 1 || ticket.Queue == nil || string(*ticket.Queue) != "admins" {
					t.Error("must route P1 to admins")
				}
			case "shadow", "approve_all":
				mode := guard.ModeShadow
				status := "processing"
				if scenario == "approve_all" {
					mode = guard.ModeApproveAll
					status = "awaiting_approval"
				}
				svc.Mode = func(context.Context) guard.Mode { return mode }
				id := post("Дякую, все працює!", "")
				process(id, status)
				var replies, actions, decisions int
				if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM support_messages WHERE ticket_id=$1 AND author='bot'),(SELECT count(*) FROM support_actions WHERE ticket_id=$1),(SELECT count(*) FROM support_decisions WHERE ticket_id=$1)`, id).Scan(&replies, &actions, &decisions); err != nil {
					t.Fatal(err)
				}
				t.Logf("DB: bot replies=%d actions=%d decisions=%d", replies, actions, decisions)
				if decisions != 1 {
					t.Error("decision must be recorded")
				}
				if mode == guard.ModeShadow && (replies != 0 || actions != 0) {
					t.Error("shadow must not send or execute")
				}
			}
		})
	}
}
