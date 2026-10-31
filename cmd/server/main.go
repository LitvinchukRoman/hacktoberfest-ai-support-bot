// Command server wires the Backend-2 (Ops) pipeline end to end: intake,
// Postgres job queue, orchestrator, approvals, SSE stream and admin API.
//
// The domain.Brain comes from internal/support/brain: Gemma 4 when GEMINI_API_KEY
// is set, otherwise the offline keyword classifier and templates. Whenever the
// model fails, the orchestrator answers with the keyword classifier instead.
package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/handziurdmytro/skelar/internal/moderation"
	"github.com/handziurdmytro/skelar/internal/platform/settings"
	"github.com/handziurdmytro/skelar/internal/support"
	"github.com/handziurdmytro/skelar/internal/support/adapters/httpapi"
	"github.com/handziurdmytro/skelar/internal/support/adapters/postgres"
	"github.com/handziurdmytro/skelar/internal/support/billing"
	"github.com/handziurdmytro/skelar/internal/support/brain"
	"github.com/handziurdmytro/skelar/internal/support/domain"
	"github.com/handziurdmytro/skelar/internal/support/facts"
	"github.com/handziurdmytro/skelar/internal/support/identity"
	"github.com/handziurdmytro/skelar/internal/support/kb"
	"github.com/handziurdmytro/skelar/internal/support/llm"
	"github.com/handziurdmytro/skelar/internal/support/stream"
	"github.com/handziurdmytro/skelar/migrations"
)

func main() {
	databaseURL := requireEnv("DATABASE_URL")
	// ADDR (host:port) wins over PORT: on the box the API listens on loopback only, behind Caddy.
	addr := envOr("ADDR", ":"+envOr("PORT", "8080"))
	autoMigrate := envOr("SUPPORT_AUTO_MIGRATE", "false") == "true"
	devTools := envOr("SUPPORT_DEV_TOOLS", "false") == "true"
	retentionDays := envInt("SUPPORT_RETENTION_DAYS", 90)
	avgHandleMin := envFloat("SUPPORT_AVG_HANDLE_MIN", 6)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatalf("connect postgres: %v", err)
	}
	defer pool.Close()

	if autoMigrate {
		n, err := migrations.Up(ctx, pool)
		if err != nil {
			log.Fatalf("migrate: %v", err)
		}
		slog.Info("support: migrations applied", "count", n)
	}

	store := postgres.New(pool)
	broker := stream.NewBroker()
	settingsStore := settings.New(store)
	moderationSvc := &moderation.Service{DB: store}

	guides, err := kb.Default()
	if err != nil {
		log.Fatalf("knowledge base: %v", err)
	}
	// Billing is fantasm's support API. Without it the bot still answers about
	// plans in general and hands every cancellation or refund to a manager.
	factsSource := facts.NewPostgres(pool, guides)
	var billingActions domain.BillingActions
	if fantasmURL, apiToken := os.Getenv("FANTASM_API_URL"), os.Getenv("SUPPORT_API_TOKEN"); fantasmURL != "" && apiToken != "" {
		client := billing.New(fantasmURL, apiToken)
		factsSource = factsSource.WithBilling(client)
		billingActions = client
	} else {
		slog.Warn("support: FANTASM_API_URL or SUPPORT_API_TOKEN is not set, billing is off")
	}
	identities := identity.NewStore()

	brainCfg := brain.FromEnv()
	aiBrain, err := brain.New(ctx, brainCfg, factsSource)
	if err != nil {
		log.Fatalf("brain: %v", err)
	}

	svc := &support.Service{
		Store:         store,
		Broker:        broker,
		Brain:         aiBrain,
		Fallback:      llm.Fake{},
		Moderation:    moderationSvc,
		Mode:          support.ModeFromSettings(settingsStore),
		RetentionDays: retentionDays,
		Identities:    identities,
		Billing:       billingActions,
	}

	intake := &httpapi.Intake{Store: store, Broker: broker, Limiter: httpapi.NewLimiter(), Identities: identities}
	admin := &httpapi.Admin{
		Service: svc, Store: store, Settings: settingsStore, Broker: broker,
		AvgHandleMin: avgHandleMin, DevToolsEnabled: devTools,
	}
	streamHandler := &stream.Handler{Broker: broker, Store: store, Authorize: authorize(store)}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	mux.HandleFunc("POST /api/support/messages", intake.PostMessage)
	mux.HandleFunc("GET /api/support/tickets/{id}", intake.GetTicket)
	mux.Handle("GET /api/support/tickets/{id}/events", streamHandler)

	mux.HandleFunc("GET /api/support/admin/tickets", admin.ListTickets)
	mux.HandleFunc("GET /api/support/admin/tickets/{id}", admin.GetTicket)
	mux.HandleFunc("POST /api/support/admin/tickets/{id}/messages", admin.PostTicketMessage)
	mux.HandleFunc("POST /api/support/admin/approvals/{actionId}/approve", admin.Approve)
	mux.HandleFunc("POST /api/support/admin/approvals/{actionId}/reject", admin.Reject)
	mux.HandleFunc("GET /api/support/admin/metrics", admin.Metrics)
	mux.HandleFunc("GET /api/support/admin/settings", admin.GetSettings)
	mux.HandleFunc("PUT /api/support/admin/settings", admin.PutSettings)
	mux.HandleFunc("GET /api/support/admin/data-requests", admin.ListDataRequests)
	mux.HandleFunc("POST /api/support/admin/data-requests/{id}/complete", admin.CompleteDataRequest)
	if devTools {
		mux.HandleFunc("POST /api/support/dev/reset", admin.DevReset)
	}

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	go runEvery(ctx, time.Second, 60*time.Second, svc.ProcessDue)
	go runEvery(ctx, 24*time.Hour, time.Minute, svc.PurgeExpired)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	slog.Info("support server listening", "addr", addr, "devTools", devTools, "model", map[bool]string{true: "offline keyword bot", false: brainCfg.LLM.ClassifierModel}[brainCfg.Fake])
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}

func runEvery(ctx context.Context, interval, timeout time.Duration, fn func(context.Context) error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tickCtx, cancel := context.WithTimeout(ctx, timeout)
			if err := fn(tickCtx); err != nil {
				slog.Error("support: background job failed", "err", err)
			}
			cancel()
		}
	}
}

func authorize(store *postgres.Store) func(r *http.Request, ticketID string) (staff, ok bool) {
	return func(r *http.Request, ticketID string) (bool, bool) {
		if v, has := httpapi.ViewerFromContext(r.Context()); has && v.IsStaff() {
			return true, true
		}
		ticket, found, err := store.Get(r.Context(), ticketID)
		if err != nil || !found {
			return false, false
		}
		userID, anonHash := httpapi.ResolveOwnerReadOnly(r)
		if ticket.OwnedBy(userID, anonHash) {
			return false, true
		}
		return false, false
	}
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("missing required env var %s", key)
	}
	return v
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}
