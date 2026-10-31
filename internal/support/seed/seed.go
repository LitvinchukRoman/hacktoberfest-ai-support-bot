// Package seed loads the support demo: the people and ideas behind scenarios S1–S7.
// Ids are fixed and times are relative to now, so every reset gives the same state
// ("pending for 3 hours" stays true whenever the demo runs). The operations side's
// dev reset calls Apply after clearing the support tables.
package seed

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/handziurdmytro/skelar/internal/support/facts"
)

// People of the demo.
const (
	Olena     = "5eed0000-0000-4000-8000-000000000001" // S1: Google, first idea waiting in premoderation for 3 h
	Iryna     = "5eed0000-0000-4000-8000-000000000002" // S2: idea hidden by five coordinated reports
	Taras     = "5eed0000-0000-4000-8000-000000000003" // S4: Google with gmail, no seal; S7: push stuck
	Moderator = "5eed0000-0000-4000-8000-000000000004" // approves S2; Microsoft sign-in with the seal
	Admin     = "5eed0000-0000-4000-8000-000000000005"
)

// Ideas of the demo.
const (
	OlenaIdea = "5eed0000-0000-4000-8000-0000000000a1"
	IrynaIdea = "5eed0000-0000-4000-8000-0000000000a2"

	OlenaIdeaSlug = "kavovyi-avtomat-u-biblioteci"
	IrynaIdeaSlug = "khakaton-dlia-pershokursnykiv"
)

type person struct {
	id, handle, name, email, role string
	approved                      int
	age                           time.Duration // how long ago the account was created
	entra                         bool          // Microsoft sign-in from the university tenant, verified university email
}

type report struct {
	reporter person
	after    time.Duration // after the first report
	reason   string
}

const (
	day        = 24 * time.Hour
	googleIss  = "https://accounts.google.com"
	entraIssFm = "https://login.microsoftonline.com/%s/v2.0"
)

func people() []person {
	return []person{
		{Olena, "olena-demo", "Олена Демченко", "olena.demo@gmail.com", "USER", 0, 30 * day, false},
		{Iryna, "iryna-demo", "Ірина Коваль", "iryna.demo@gmail.com", "USER", 1, 200 * day, false},
		{Taras, "taras-demo", "Тарас Мельник", "taras.demo@gmail.com", "USER", 0, 60 * day, false},
		{Moderator, "moderator-demo", "Марія Модераторка", "moderator.demo@ukma.edu.ua", "MODERATOR", 5, 400 * day, true},
		{Admin, "admin-demo", "Андрій Адмін", "admin.demo@ukma.edu.ua", "ADMIN", 2, 500 * day, true},
	}
}

// brigade is the S2 reports: four accounts created hours before reporting and one
// old account, all within 12 minutes, none with an approved idea.
func brigade() []report {
	reporter := func(n int, age time.Duration) person {
		return person{fmt.Sprintf("5eed0000-0000-4000-8000-0000000000%02d", 10+n), fmt.Sprintf("reporter-%d-demo", n),
			fmt.Sprintf("Скаржник %d", n), fmt.Sprintf("reporter%d.demo@gmail.com", n), "USER", 0, age, false}
	}
	return []report{
		{reporter(1, 2*time.Hour), 0, "Спам"},
		{reporter(2, 3*time.Hour), 3 * time.Minute, "Спам"},
		{reporter(3, 5*time.Hour), 7 * time.Minute, "Неприйнятний вміст"},
		{reporter(4, time.Hour), 10 * time.Minute, "Спам"},
		{reporter(5, 400*day), 12 * time.Minute, "Не подобається"},
	}
}

// Apply resets the demo people and their ideas to the scenario state. Users are
// updated in place rather than deleted, so sessions and decisions that reference
// them survive a reset; their ideas, sign-ins, push devices and notifications are replaced.
func Apply(ctx context.Context, db *pgxpool.Pool, now time.Time) error {
	now = now.UTC()
	reports := brigade()
	everyone := people()
	for _, r := range reports {
		// A reporter's account age is counted from the moment of the report.
		r.reporter.age = 2*time.Hour - r.after + r.reporter.age
		everyone = append(everyone, r.reporter)
	}
	ids := make([]string, len(everyone))
	for i, p := range everyone {
		ids[i] = p.id
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("seed: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := apply(ctx, tx, now, everyone, reports, ids); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// execer is what the seed writes through: a transaction.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func apply(ctx context.Context, q execer, now time.Time, everyone []person, reports []report, ids []string) error {
	for _, stmt := range []string{
		`DELETE FROM ideas WHERE author_id = ANY($1)`,
		`DELETE FROM external_identities WHERE user_id = ANY($1)`,
		`DELETE FROM push_subscriptions WHERE user_id = ANY($1)`,
		`DELETE FROM notifications WHERE user_id = ANY($1)`,
	} {
		if _, err := q.Exec(ctx, stmt, ids); err != nil {
			return fmt.Errorf("seed: clear: %w", err)
		}
	}
	for _, p := range everyone {
		if _, err := q.Exec(ctx, `
				INSERT INTO users (id, handle, name, email, role, approved_ideas, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
				ON CONFLICT (id) DO UPDATE SET handle = EXCLUDED.handle, name = EXCLUDED.name, email = EXCLUDED.email,
				    role = EXCLUDED.role, approved_ideas = EXCLUDED.approved_ideas, created_at = EXCLUDED.created_at`,
			p.id, p.handle, p.name, p.email, p.role, p.approved, now.Add(-p.age)); err != nil {
			return fmt.Errorf("seed: user %s: %w", p.handle, err)
		}
		provider, issuer, tenant := "google", googleIss, ""
		if p.entra {
			provider, issuer, tenant = "entra", fmt.Sprintf(entraIssFm, facts.DefaultSeal.Tenant), facts.DefaultSeal.Tenant
		}
		if _, err := q.Exec(ctx, `
				INSERT INTO external_identities (provider, issuer, subject, user_id, tenant_id, email, email_verified)
				VALUES ($1, $2, $3, $4, $5, $6, true)`, provider, issuer, "seed-"+p.handle, p.id, tenant, p.email); err != nil {
			return fmt.Errorf("seed: identity %s: %w", p.handle, err)
		}
	}

	// S1: Olena's first idea, waiting in premoderation for 3 hours.
	olenaAt := now.Add(-3 * time.Hour)
	if err := insertIdea(ctx, q, OlenaIdea, Olena, OlenaIdeaSlug, "Кавовий автомат у бібліотеці",
		"Поставити кавовий автомат на першому поверсі бібліотеки.", "PENDING", olenaAt, nil); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `INSERT INTO moderation_cases (id, idea_id, state, source, created_at) VALUES ($1, $2, 'OPEN', 'PREMODERATION', $3)`,
		"5eed0000-0000-4000-8000-0000000000c1", OlenaIdea, olenaAt); err != nil {
		return fmt.Errorf("seed: S1 case: %w", err)
	}

	// S2: Iryna's published idea, hidden automatically by the fifth report.
	irynaAt, published := now.Add(-10*day), now.Add(-10*day+time.Hour)
	if err := insertIdea(ctx, q, IrynaIdea, Iryna, IrynaIdeaSlug, "Хакатон для першокурсників",
		"Дводенний хакатон, де першокурсники збирають команди й будують перший проєкт.", "HIDDEN", irynaAt, &published); err != nil {
		return err
	}
	firstReport := now.Add(-2 * time.Hour)
	for _, r := range reports {
		if _, err := q.Exec(ctx, `INSERT INTO reports (idea_id, user_id, reason, status, created_at) VALUES ($1, $2, $3, 'OPEN', $4)`,
			IrynaIdea, r.reporter.id, r.reason, firstReport.Add(r.after)); err != nil {
			return fmt.Errorf("seed: S2 report: %w", err)
		}
	}
	if _, err := q.Exec(ctx, `INSERT INTO moderation_cases (id, idea_id, state, source, created_at) VALUES ($1, $2, 'OPEN', 'REPORTS', $3)`,
		"5eed0000-0000-4000-8000-0000000000c2", IrynaIdea, firstReport.Add(12*time.Minute)); err != nil {
		return fmt.Errorf("seed: S2 case: %w", err)
	}

	// S7: Taras has a phone subscribed, but two notifications have waited for a push for over 10 minutes.
	if _, err := q.Exec(ctx, `INSERT INTO push_subscriptions (id, user_id, endpoint, p256dh, auth, created_at) VALUES ($1, $2, $3, 'seed-p256dh', 'seed-auth', $4)`,
		"5eed0000-0000-4000-8000-0000000000d1", Taras, "https://push.example.invalid/seed-taras", now.Add(-7*day)); err != nil {
		return fmt.Errorf("seed: S7 subscription: %w", err)
	}
	for i, ago := range []time.Duration{30 * time.Minute, 20 * time.Minute} {
		if _, err := q.Exec(ctx, `INSERT INTO notifications (id, user_id, type, payload, created_at) VALUES ($1, $2, 'SYSTEM', $3, $4)`,
			fmt.Sprintf("5eed0000-0000-4000-8000-0000000000e%d", i+1), Taras, map[string]any{"message": "Нова відповідь у твоєму обговоренні"}, now.Add(-ago)); err != nil {
			return fmt.Errorf("seed: S7 notification: %w", err)
		}
	}
	return nil
}

func insertIdea(ctx context.Context, q execer, id, author, slug, title, summary, moderation string, at time.Time, published *time.Time) error {
	_, err := q.Exec(ctx, `
		INSERT INTO ideas (id, author_id, slug, title, summary, category, status, visibility, moderation_state, published_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'PROJECT', 'OPEN', 'PUBLIC', $6, $7, $8, $8)`,
		id, author, slug, title, summary, moderation, published, at)
	if err != nil {
		return fmt.Errorf("seed: idea %s: %w", slug, err)
	}
	return nil
}
