// Package facts reads what a support decision needs from fantasm's database: the
// person, their idea, the reports on it, push delivery and the knowledge base.
// It only reads, and only what the intent needs. An idea is loaded only when its
// author is the person asking. Reporters stay anonymous: the age of the account,
// its approved ideas and the time of the report, never who filed it.
//
// The seal ("Verified") is worked out from the sign-ins by the same rule fantasm's
// organization policy applies: one identity that is Microsoft, from the university
// tenant, with a verified university address.
package facts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/handziurdmytro/skelar/internal/support/domain"
)

// Seal is the sign-in a seal needs. Policy reads the pieces (Microsoft, university
// tenant, university address) to explain what is missing, and Verified to know
// whether the seal is held.
type Seal struct {
	Tenant string // Microsoft tenant id
	Domain string // verified email domain
}

// DefaultSeal mirrors the NaUKMA rule in config/organizations.json.
var DefaultSeal = Seal{Tenant: "b8cbfe43-c90c-4bea-84ae-be5d6d8a5f52", Domain: "ukma.edu.ua"}

// DB is the part of a pgx pool facts reads through.
type DB interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Searcher finds knowledge-base passages for a question; kb.Index implements it.
type Searcher interface {
	Search(query string, limit int) []domain.KBHit
}

const knowledgeHits = 3

type Postgres struct {
	db   DB
	kb   Searcher
	seal Seal
}

func NewPostgres(db DB, kb Searcher) *Postgres {
	return &Postgres{db: db, kb: kb, seal: DefaultSeal}
}

func (p *Postgres) WithSeal(s Seal) *Postgres { p.seal = s; return p }

var errNoUser = errors.New("user not found")

func (p *Postgres) Facts(ctx context.Context, in domain.FactsInput) (domain.Facts, error) {
	f := domain.Facts{Now: in.Now, State: in.State}
	if in.Intent == domain.IntentHowTo && p.kb != nil {
		f.Knowledge = p.kb.Search(in.Question, knowledgeHits)
	}
	if in.UserID == "" {
		return f, nil
	}
	user, recent, err := p.user(ctx, in.UserID, in.Now)
	if errors.Is(err, errNoUser) {
		// The account is gone: answer as to a visitor.
		return f, nil
	}
	if err != nil {
		return domain.Facts{}, err
	}
	f.User, f.RecentTickets = &user, recent

	if in.Intent == domain.IntentIdeaVisibility || in.Intent == domain.IntentModerationAppeal || in.State.Flow == domain.FlowAppeal {
		idea, found, err := p.idea(ctx, in)
		if err != nil {
			return domain.Facts{}, err
		}
		if found {
			f.Idea = &idea
			if idea.Moderation == domain.ModerationHidden {
				if f.Reports, err = p.reports(ctx, idea.ID); err != nil {
					return domain.Facts{}, err
				}
			}
		}
	}
	if in.Intent == domain.IntentNotificationsIssue {
		push, err := p.push(ctx, in.UserID, in.Now)
		if err != nil {
			return domain.Facts{}, err
		}
		f.Push = &push
	}
	return f, nil
}

func (p *Postgres) user(ctx context.Context, userID string, now time.Time) (domain.UserFacts, int, error) {
	u := domain.UserFacts{ID: userID}
	var hasEntra bool
	var recent int
	err := p.db.QueryRow(ctx, `
		SELECT u.handle, u.role, u.approved_ideas, u.created_at,
		       COALESCE(bool_or(ei.provider = 'entra'), false),
		       COALESCE(bool_or(ei.provider = 'entra' AND ei.tenant_id = $2), false),
		       COALESCE(bool_or(ei.email_verified AND lower(ei.email) LIKE '%@' || $3), false),
		       COALESCE(bool_or(ei.provider = 'entra' AND ei.tenant_id = $2 AND ei.email_verified AND lower(ei.email) LIKE '%@' || $3), false),
		       (SELECT count(*) FROM ideas i WHERE i.author_id = u.id AND i.created_at > $4::timestamptz - interval '24 hours'),
		       (SELECT count(*) FROM support_tickets t WHERE t.user_id = u.id AND t.created_at > $4::timestamptz - interval '24 hours')
		FROM users u LEFT JOIN external_identities ei ON ei.user_id = u.id
		WHERE u.id = $1
		GROUP BY u.id`, userID, p.seal.Tenant, strings.ToLower(p.seal.Domain), now).
		Scan(&u.Handle, &u.Role, &u.ApprovedIdeas, &u.CreatedAt, &hasEntra, &u.TenantMatches, &u.UKMAEmail, &u.Verified, &u.IdeasToday, &recent)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.UserFacts{}, 0, errNoUser
	}
	if err != nil {
		return domain.UserFacts{}, 0, fmt.Errorf("load user facts: %w", err)
	}
	u.Provider = domain.ProviderGoogle
	if hasEntra {
		u.Provider = domain.ProviderEntra
	}
	return u, recent, nil
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// idea finds the person's idea the message is about: the one on the page the chat was
// opened on, then one named in the text, and otherwise their latest idea that is
// not out in the feed (a draft, pending, hidden or rejected).
func (p *Postgres) idea(ctx context.Context, in domain.FactsInput) (domain.IdeaFacts, bool, error) {
	hint := strings.TrimSpace(in.IdeaHint)
	var (
		i            domain.IdeaFacts
		caseSource   *string
		caseOpenedAt *time.Time
		lastDecision *string
		lastNote     *string
	)
	err := p.db.QueryRow(ctx, `
		SELECT i.id, i.slug, i.title, i.status, i.moderation_state, i.visibility, i.created_at,
		       oc.source, oc.created_at, lc.decision, lc.note,
		       (SELECT count(*) FROM reports r WHERE r.idea_id = i.id AND r.status = 'OPEN')
		FROM ideas i
		LEFT JOIN moderation_cases oc ON oc.idea_id = i.id AND oc.state = 'OPEN'
		LEFT JOIN LATERAL (
		    SELECT mc.decision, mc.note FROM moderation_cases mc
		    WHERE mc.idea_id = i.id AND mc.state = 'CLOSED'
		    ORDER BY mc.decided_at DESC LIMIT 1
		) lc ON true
		WHERE i.author_id = $1 AND i.deleted_at IS NULL
		  AND (i.slug = $2
		       OR ($3 <> '' AND (i.slug = lower($3) OR i.title ILIKE '%' || $4 || '%'))
		       OR i.status = 'DRAFT' OR i.moderation_state IN ('PENDING', 'HIDDEN', 'REJECTED'))
		ORDER BY i.slug = $2 DESC,
		         ($3 <> '' AND (i.slug = lower($3) OR i.title ILIKE '%' || $4 || '%')) DESC,
		         i.updated_at DESC
		LIMIT 1`, in.UserID, in.IdeaSlug, hint, likeEscaper.Replace(hint)).
		Scan(&i.ID, &i.Slug, &i.Title, &i.Status, &i.Moderation, &i.Visibility, &i.CreatedAt,
			&caseSource, &caseOpenedAt, &lastDecision, &lastNote, &i.OpenReports)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.IdeaFacts{}, false, nil
	}
	if err != nil {
		return domain.IdeaFacts{}, false, fmt.Errorf("load idea facts: %w", err)
	}
	if caseSource != nil {
		i.CaseSource = *caseSource
	}
	if caseOpenedAt != nil {
		i.CaseOpenedAt = *caseOpenedAt
	}
	// A moderator's closed case put the idea where it is now: the bot will not argue with it.
	if lastDecision != nil && *lastDecision == i.Moderation {
		i.DecidedByHuman = true
		if lastNote != nil {
			i.DecisionNote = *lastNote
		}
	}
	return i, true, nil
}

func (p *Postgres) reports(ctx context.Context, ideaID string) ([]domain.ReportFacts, error) {
	rows, err := p.db.Query(ctx, `
		SELECT extract(epoch FROM r.created_at - u.created_at)::float8, u.approved_ideas, r.created_at
		FROM reports r JOIN users u ON u.id = r.user_id
		WHERE r.idea_id = $1 AND r.status = 'OPEN'
		ORDER BY r.created_at`, ideaID)
	if err != nil {
		return nil, fmt.Errorf("load reports: %w", err)
	}
	defer rows.Close()
	out := []domain.ReportFacts{}
	for rows.Next() {
		var ageSeconds float64
		var r domain.ReportFacts
		if err := rows.Scan(&ageSeconds, &r.ReporterApproved, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan report: %w", err)
		}
		r.ReporterAge = time.Duration(ageSeconds * float64(time.Second))
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read reports: %w", err)
	}
	return out, nil
}

func (p *Postgres) push(ctx context.Context, userID string, now time.Time) (domain.PushFacts, error) {
	var f domain.PushFacts
	err := p.db.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM push_subscriptions WHERE user_id = $1),
		       (SELECT count(*) FROM notifications WHERE user_id = $1 AND push_sent_at IS NULL
		          AND created_at < $2::timestamptz - interval '10 minutes' AND created_at > $2::timestamptz - interval '1 day')`,
		userID, now).Scan(&f.Subscriptions, &f.Stuck)
	if err != nil {
		return domain.PushFacts{}, fmt.Errorf("load push facts: %w", err)
	}
	return f, nil
}
