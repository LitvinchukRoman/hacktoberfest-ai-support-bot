package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/handziurdmytro/skelar/internal/support/domain"
	"github.com/handziurdmytro/skelar/internal/support/ports"
)

// inboxCursor is the opaque pagination token for ListInbox: the keyset
// position (priority, updated_at, id) of the last row already returned.
type inboxCursor struct {
	Priority  int       `json:"p"`
	UpdatedAt time.Time `json:"u"`
	ID        string    `json:"id"`
}

func encodeInboxCursor(c inboxCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeInboxCursor(s string) (*inboxCursor, error) {
	if s == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decode cursor: %w", err)
	}
	var c inboxCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("decode cursor: %w", err)
	}
	return &c, nil
}

const ticketColumns = `id, user_id, anon_key_hash, contact_email, status, autonomy, intent, emotion,
	language, priority, queue, escalation_reason, idea_id, page, flow, flow_step, last_seq,
	first_reply_at, solved_at, created_at, updated_at`

func scanTicket(row pgx.Row) (ports.Ticket, error) {
	var (
		t                                     ports.Ticket
		status, intent, emotion, language     string
		flow                                  string
		autonomy, queue, contactEmail, reason *string
		priority, flowStep                    int16
	)
	if err := row.Scan(
		&t.ID, &t.UserID, &t.AnonKeyHash, &contactEmail, &status, &autonomy, &intent, &emotion,
		&language, &priority, &queue, &reason, &t.IdeaID, &t.Page, &flow, &flowStep, &t.LastSeq,
		&t.FirstReplyAt, &t.SolvedAt, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return ports.Ticket{}, err
	}
	t.Status = domain.TicketStatus(status)
	t.Intent = domain.Intent(intent)
	t.Emotion = domain.Emotion(emotion)
	t.Language = language
	t.Priority = int(priority)
	t.State = domain.ConversationState{Flow: domain.Flow(flow), Step: int(flowStep)}
	if autonomy != nil {
		a := domain.Autonomy(*autonomy)
		t.Autonomy = &a
	}
	if queue != nil {
		q := domain.Queue(*queue)
		t.Queue = &q
	}
	if contactEmail != nil {
		t.ContactEmail = *contactEmail
	}
	if reason != nil {
		t.EscalationReason = *reason
	}
	return t, nil
}

func (s *Store) Create(ctx context.Context, in ports.NewTicketInput) (ports.Ticket, error) {
	row := s.q(ctx).QueryRow(ctx, `
		INSERT INTO support_tickets (id, user_id, anon_key_hash, contact_email, page)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+ticketColumns,
		in.ID, in.UserID, in.AnonKeyHash, nullIfEmpty(in.ContactEmail), in.Page,
	)
	return scanTicket(row)
}

func (s *Store) Get(ctx context.Context, id string) (ports.Ticket, bool, error) {
	row := s.q(ctx).QueryRow(ctx, `SELECT `+ticketColumns+` FROM support_tickets WHERE id = $1`, id)
	t, err := scanTicket(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.Ticket{}, false, nil
	}
	if err != nil {
		return ports.Ticket{}, false, err
	}
	return t, true, nil
}

// UpdateAfterDecision writes the orchestrator's outcome for one processing
// pass. first_reply_at is set once and never overwritten (COALESCE); pass
// nil to leave it untouched, or a timestamp the first time a reply is sent.
func (s *Store) UpdateAfterDecision(ctx context.Context, t ports.Ticket) error {
	var autonomy, queue *string
	if t.Autonomy != nil {
		v := string(*t.Autonomy)
		autonomy = &v
	}
	if t.Queue != nil {
		v := string(*t.Queue)
		queue = &v
	}
	_, err := s.q(ctx).Exec(ctx, `
		UPDATE support_tickets SET
			status = $2, autonomy = $3, intent = $4, emotion = $5, priority = $6, queue = $7,
			escalation_reason = $8, idea_id = $9, flow = $10, flow_step = $11,
			first_reply_at = COALESCE(first_reply_at, $12),
			solved_at = CASE WHEN $2 = 'solved' THEN now() ELSE solved_at END,
			updated_at = now()
		WHERE id = $1`,
		t.ID, string(t.Status), autonomy, string(t.Intent), string(t.Emotion), t.Priority, queue,
		nullIfEmpty(t.EscalationReason), t.IdeaID, string(t.State.Flow), t.State.Step,
		t.FirstReplyAt,
	)
	return err
}

// ListInbox is keyset-paginated on (priority, updated_at, id) ascending —
// most urgent and longest-waiting tickets first. cursor is the opaque
// token ListInbox itself returned as nextCursor; "" starts from the top.
func (s *Store) ListInbox(ctx context.Context, status string, cursor string, limit int) ([]ports.Ticket, string, error) {
	after, err := decodeInboxCursor(cursor)
	if err != nil {
		return nil, "", err
	}

	var (
		where []string
		args  []any
	)
	if status != "" && status != "all" {
		args = append(args, status)
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	if after != nil {
		args = append(args, after.Priority, after.UpdatedAt, after.ID)
		n := len(args)
		where = append(where, fmt.Sprintf("(priority, updated_at, id) > ($%d, $%d, $%d)", n-2, n-1, n))
	}
	whereSQL := "TRUE"
	if len(where) > 0 {
		whereSQL = strings.Join(where, " AND ")
	}

	args = append(args, limit+1)
	q := `SELECT ` + ticketColumns + ` FROM support_tickets WHERE ` + whereSQL + `
		ORDER BY priority ASC, updated_at ASC, id ASC LIMIT $` + strconv.Itoa(len(args))

	rows, err := s.q(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var tickets []ports.Ticket
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, "", err
		}
		tickets = append(tickets, t)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	next := ""
	if len(tickets) > limit {
		last := tickets[limit-1]
		tickets = tickets[:limit]
		next = encodeInboxCursor(inboxCursor{Priority: last.Priority, UpdatedAt: last.UpdatedAt, ID: last.ID})
	}
	return tickets, next, nil
}
