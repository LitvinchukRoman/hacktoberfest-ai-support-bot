package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/handziurdmytro/skelar/internal/support/guard"
	"github.com/handziurdmytro/skelar/internal/support/ports"
)

type Store interface {
	ports.TicketRepo
	ports.MessageRepo
	ports.EventRepo
	ports.JobQueue
	ports.TxManager
}

type Intake struct {
	Store   Store
	Limiter *Limiter
	Now     func() time.Time
}

func (h *Intake) now() time.Time {
	if h.Now == nil {
		return time.Now().UTC()
	}
	return h.Now()
}

const anonCookieName = "fantasm_support"

var emailFormatRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

type messageRequest struct {
	TicketID *string `json:"ticketId"`
	Text     string  `json:"text"`
	Email    *string `json:"email,omitempty"`
	Page     string  `json:"page"`
	IdeaSlug *string `json:"ideaSlug,omitempty"`
}

func (h *Intake) PostMessage(w http.ResponseWriter, r *http.Request) {
	var req messageRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "invalid JSON body")
		return
	}

	textLen := utf8.RuneCountInString(req.Text)
	if textLen < 1 || textLen > 2000 {
		writeError(w, http.StatusBadRequest, "validation_failed", "text must be 1..2000 characters")
		return
	}
	if utf8.RuneCountInString(req.Page) > 300 {
		writeError(w, http.StatusBadRequest, "validation_failed", "page must be <=300 characters")
		return
	}
	email := ""
	if req.Email != nil && *req.Email != "" {
		if !emailFormatRe.MatchString(*req.Email) {
			writeError(w, http.StatusBadRequest, "validation_failed", "invalid email")
			return
		}
		email = *req.Email
	}

	ctx := r.Context()
	userID, anonKeyHash, setCookie := h.resolveOwner(r)

	identity := userID
	if identity == "" {
		identity = r.RemoteAddr
	}
	now := h.now()
	if !h.Limiter.Allow("msg:"+identity, time.Minute, 6, now) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many messages, slow down")
		return
	}

	var ticket ports.Ticket
	isNewTicket := req.TicketID == nil || *req.TicketID == ""
	if !isNewTicket {
		existing, ok, err := h.Store.Get(ctx, *req.TicketID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to load ticket")
			return
		}
		if !ok || !existing.OwnedBy(userID, anonKeyHash) {
			writeError(w, http.StatusNotFound, "not_found", "ticket not found")
			return
		}
		ticket = existing

		if !h.Limiter.Allow("ticket:"+ticket.ID, time.Hour, 30, now) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many messages on this ticket")
			return
		}
	}

	redaction := guard.Redact(req.Text)
	signals := guard.Detect(req.Text)
	signalStrs := make([]string, len(signals))
	for i, s := range signals {
		signalStrs[i] = string(s)
	}

	ideaSlug := ""
	if req.IdeaSlug != nil {
		ideaSlug = *req.IdeaSlug
	}

	var ticketID, messageID string
	err := h.Store.WithinTx(ctx, func(ctx context.Context) error {
		if isNewTicket {
			var userIDPtr *string
			if userID != "" {
				userIDPtr = &userID
			}
			created, err := h.Store.Create(ctx, ports.NewTicketInput{
				ID: uuid.NewString(), UserID: userIDPtr, AnonKeyHash: anonKeyHash,
				ContactEmail: email, Page: req.Page, IdeaSlug: ideaSlug,
			})
			if err != nil {
				return err
			}
			ticket = created
		}
		ticketID = ticket.ID

		msg, err := h.Store.Insert(ctx, ports.NewMessageInput{
			ID: uuid.NewString(), TicketID: ticket.ID, Author: "customer",
			Body: redaction.Text, PIIFound: redaction.Found, Signals: signalStrs,
		})
		if err != nil {
			return err
		}
		messageID = msg.ID

		payload, _ := json.Marshal(map[string]any{"stage": "received", "summary": "Отримав твоє повідомлення…"})
		if _, err := h.Store.Append(ctx, ticket.ID, "trace", payload, payload); err != nil {
			return err
		}

		return h.Store.Enqueue(ctx, ticket.ID)
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to store message")
		return
	}

	if setCookie != nil {
		http.SetCookie(w, setCookie)
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"ticketId": ticketID, "messageId": messageID})
}

// resolveOwner returns the caller's user id (if a Viewer is present in the
// request context) or an anonymous key hash derived from the fantasm_support
// cookie, minting a new one if absent. setCookie is non-nil only when a new
// anonymous cookie needs to be set on the response.
func (h *Intake) resolveOwner(r *http.Request) (userID string, anonKeyHash []byte, setCookie *http.Cookie) {
	if v, ok := ViewerFromContext(r.Context()); ok {
		return v.ID, nil, nil
	}

	if c, err := r.Cookie(anonCookieName); err == nil && c.Value != "" {
		hash := sha256.Sum256([]byte(c.Value))
		return "", hash[:], nil
	}

	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	value := hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(value))
	cookie := &http.Cookie{
		Name:     anonCookieName,
		Value:    value,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
		MaxAge:   30 * 24 * 60 * 60,
	}
	return "", hash[:], cookie
}
