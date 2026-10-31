# Backend 2 (Ops) — status, for Backend 1 (AI core)

Everything Backend 2 owns per `ddocs/BACKEND_2_OPS.md` is implemented, tested, and on `main`.
This file tells you exactly what's ready, the contract you implement against, and the couple of
things B2 still owes that have nothing to do with you.

## What's implemented

- `internal/support/domain` — the shared contract (`Intent`, `Classification`, `Decision`,
  `Facts`, the `Brain` interfaces). This is what you implement against.
- `internal/support/guard` — `Redact`, `Detect`, `Tighten` (monotonic escalation, property-tested
  across ~14k combinations), `Validate`. Fully unit-tested (`go test ./internal/support/guard/...`).
- `migrations/` — full schema: `support_tickets/messages/events/jobs/decisions/actions/data_requests`,
  plus minimal fixture tables (`users`, `ideas`, `moderation_cases`, `platform_settings`) since this
  is a standalone repo without the real fantasm database.
- `internal/support/adapters/postgres` — repository implementations, integration-tested against a
  real Postgres instance (`make test-integration`).
- `internal/support/service.go` + `executor.go` — the orchestrator: prefilter → classify → facts →
  decide → `guard.Tighten` → render → `guard.Validate` → route → execute. Fully wired to Postgres
  and the SSE broker.
- `internal/support/approvals.go` — Approve/Reject claim pattern (double-decide → conflict),
  `restore_idea` wired through a moderation stub.
- `internal/support/gdpr.go` — GDPR data-request lifecycle (`support_data_requests`, 30-day due
  date, bot never deletes anything itself).
- `internal/support/stream` — SSE broker + HTTP handler (backlog replay via `Last-Event-ID`, staff
  vs. public payload split).
- `internal/support/adapters/httpapi` — intake (`POST /api/support/messages`,
  `GET /api/support/tickets/{id}`), and admin endpoints (ticket list/detail/reply, approvals,
  metrics, settings/kill-switch, GDPR admin, dev reset).
- `cmd/server/main.go` — composition root. **This is where you plug in.**

## Where you plug in

`cmd/server/main.go` currently wires a placeholder `domain.Brain` (`placeholderClassifier` /
`placeholderFacts` / `placeholderDecide` / `placeholderRenderer`) that always escalates —
intentionally dumb, but safe: nothing auto-executes without a real policy. Replace the
`placeholderBrain()` call with your real implementation:

```go
svc := &support.Service{
    ...
    Brain: domain.Brain{
        Classifier: yourpkg.NewOpenAIClassifier(cfg),
        Facts:      yourpkg.NewPostgresFacts(pool),
        Decide:     yourpkg.Decide,
        Renderer:   yourpkg.NewRenderer(cfg),
    },
    Fallback: yourpkg.FakeClassifier{}, // used whenever Classifier errors, not just at startup
}
```

Everything downstream (guard, trace, execute, SSE, approvals) already works with whatever
`Decision`/`Classification`/`Facts` you return, as long as they satisfy `internal/support/domain`.

One hard constraint from the contract: every `TemplateID` your `Decide`/`Tighten` path can produce
must render to a real, non-empty reply — `guard.Validate` vetoes anything that doesn't, and the
ticket escalates with reason `fallback:<check>` instead of sending a broken message. Worth checking
early rather than discovering it live.

## Known gaps — mine, not blocked on you

Two B2 features from the brief aren't built yet. Not your job, just unfinished on my end:

1. **Handoff note** — an internal (`visibility: internal`) message summarizing what the bot found
   before an escalation (so staff don't have to re-derive it from trace). Right now `execute()`
   only ever sends the customer-facing reply.
2. **Staff notifications** (`SYSTEM` type, dedupe key) on `notify_staff`/`bug_report`/escalation —
   not implemented; this standalone repo has no notification system to hook into.

Also written-but-not-integration-tested: the `ActionRepo`/`DataRequestRepo` Postgres methods, and
the full Approve → `moderation.Decide` path, against a real database (core ticket/message/event/job
repos are covered, these two aren't yet).

## How to run / test

```
make test-all   # unit + Postgres integration tests; brings up Postgres itself
make run        # starts the server on :8080 with SUPPORT_DEV_TOOLS=true
```

No credentials are needed for anything in this repo. `OPENAI_API_KEY` and friends are your env
vars — nothing in B2's code reads them.
