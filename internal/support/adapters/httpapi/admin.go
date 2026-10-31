package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/handziurdmytro/skelar/internal/platform/settings"
	"github.com/handziurdmytro/skelar/internal/support"
	"github.com/handziurdmytro/skelar/internal/support/ports"
	"github.com/handziurdmytro/skelar/internal/support/stream"
)

type AdminStore interface {
	ports.TicketRepo
	ports.MessageRepo
	ports.EventRepo
	ports.ActionRepo
	ports.MetricsRepo
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type Admin struct {
	Service         *support.Service
	Store           AdminStore
	Settings        *settings.Store
	Broker          *stream.Broker
	AvgHandleMin    float64
	DevToolsEnabled bool
	Now             func() time.Time
}

func (h *Admin) appendEvent(ctx context.Context, ticketID, typ string, staff, public json.RawMessage) error {
	e, err := h.Store.Append(ctx, ticketID, typ, staff, public)
	if err != nil {
		return err
	}
	if h.Broker != nil {
		ports.AfterCommit(ctx, func() { h.Broker.Publish(e) })
	}
	return nil
}

func (h *Admin) now() time.Time {
	if h.Now == nil {
		return time.Now().UTC()
	}
	return h.Now()
}

func requireStaff(w http.ResponseWriter, r *http.Request) (Viewer, bool) {
	v, ok := ViewerFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "sign in required")
		return Viewer{}, false
	}
	if !v.IsStaff() {
		writeError(w, http.StatusForbidden, "forbidden", "staff only")
		return Viewer{}, false
	}
	return v, true
}

func requireAdmin(w http.ResponseWriter, r *http.Request) (Viewer, bool) {
	v, ok := ViewerFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "sign in required")
		return Viewer{}, false
	}
	if !v.IsAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "admin only")
		return Viewer{}, false
	}
	return v, true
}

type ticketRow struct {
	ID        string  `json:"id"`
	Status    string  `json:"status"`
	Intent    string  `json:"intent"`
	Priority  int     `json:"priority"`
	Queue     *string `json:"queue,omitempty"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
}

func (h *Admin) ListTickets(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireStaff(w, r); !ok {
		return
	}
	status := r.URL.Query().Get("status")
	cursor := r.URL.Query().Get("cursor")
	limit := 50

	tickets, next, err := h.Store.ListInbox(r.Context(), status, cursor, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list tickets")
		return
	}
	rows := make([]ticketRow, len(tickets))
	for i, t := range tickets {
		row := ticketRow{
			ID: t.ID, Status: string(t.Status), Intent: string(t.Intent), Priority: t.Priority,
			CreatedAt: t.CreatedAt.Format(timeFormat), UpdatedAt: t.UpdatedAt.Format(timeFormat),
		}
		if t.Queue != nil {
			q := string(*t.Queue)
			row.Queue = &q
		}
		rows[i] = row
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows, "nextCursor": next})
}

type staffTicketDTO struct {
	ID       string            `json:"id"`
	Status   string            `json:"status"`
	Intent   string            `json:"intent"`
	Messages []messageDTO      `json:"messages"`
	Trace    []json.RawMessage `json:"trace"`
	Pending  []actionDTO       `json:"pending"`
}

type actionDTO struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Rule       string          `json:"rule"`
	DraftReply string          `json:"draftReply"`
	Evidence   json.RawMessage `json:"evidence"`
}

func (h *Admin) GetTicket(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireStaff(w, r); !ok {
		return
	}
	id := r.PathValue("id")
	ctx := r.Context()

	ticket, ok, err := h.Store.Get(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load ticket")
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "ticket not found")
		return
	}

	msgs, err := h.Store.ForTicket(ctx, id, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load messages")
		return
	}
	events, err := h.Store.After(ctx, id, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load trace")
		return
	}
	pending, err := h.Store.ListPendingForTicket(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load pending actions")
		return
	}

	dto := staffTicketDTO{ID: ticket.ID, Status: string(ticket.Status), Intent: string(ticket.Intent)}
	for _, m := range msgs {
		dto.Messages = append(dto.Messages, messageDTO{ID: m.ID, Author: m.Author, Text: m.Body, CreatedAt: m.CreatedAt.Format(timeFormat)})
	}
	for _, e := range events {
		if e.Type == "trace" {
			dto.Trace = append(dto.Trace, e.Staff)
		}
	}
	for _, a := range pending {
		dto.Pending = append(dto.Pending, actionDTO{ID: a.ID, Type: string(a.Type), Rule: a.Rule, DraftReply: a.DraftReply, Evidence: a.Evidence})
	}

	writeJSON(w, http.StatusOK, dto)
}

type staffMessageRequest struct {
	Text       string `json:"text"`
	Visibility string `json:"visibility"`
}

func (h *Admin) PostTicketMessage(w http.ResponseWriter, r *http.Request) {
	v, ok := requireStaff(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var req staffMessageRequest
	if err := decodeJSON(r, &req); err != nil || req.Text == "" || (req.Visibility != "public" && req.Visibility != "internal") {
		writeError(w, http.StatusBadRequest, "validation_failed", "text and visibility (public|internal) are required")
		return
	}

	ticket, ok2, err := h.Store.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load ticket")
		return
	}
	if !ok2 {
		writeError(w, http.StatusNotFound, "not_found", "ticket not found")
		return
	}

	agentID := v.ID
	_, err = h.Store.Insert(r.Context(), ports.NewMessageInput{
		ID: uuid.NewString(), TicketID: ticket.ID, Author: "agent", AgentID: &agentID, Visibility: req.Visibility, Body: req.Text,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to store message")
		return
	}
	payload, _ := json.Marshal(map[string]any{"author": "agent", "visibility": req.Visibility, "text": req.Text})
	var public json.RawMessage
	if req.Visibility == "public" {
		public = payload
	}
	if err := h.appendEvent(r.Context(), ticket.ID, "message", payload, public); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to publish message")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type approveRequest struct {
	EditedReply string `json:"editedReply"`
}

func (h *Admin) Approve(w http.ResponseWriter, r *http.Request) {
	v, ok := requireStaff(w, r)
	if !ok {
		return
	}
	var req approveRequest
	_ = decodeJSON(r, &req)

	err := h.Service.Approve(r.Context(), r.PathValue("actionId"), v.ID, req.EditedReply)
	writeApprovalResult(w, err)
}

type rejectRequest struct {
	Reason string `json:"reason"`
}

func (h *Admin) Reject(w http.ResponseWriter, r *http.Request) {
	v, ok := requireStaff(w, r)
	if !ok {
		return
	}
	var req rejectRequest
	if err := decodeJSON(r, &req); err != nil || req.Reason == "" {
		writeError(w, http.StatusBadRequest, "validation_failed", "reason is required")
		return
	}

	err := h.Service.Reject(r.Context(), r.PathValue("actionId"), v.ID, req.Reason)
	writeApprovalResult(w, err)
}

func writeApprovalResult(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, support.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "someone already decided")
	case errors.Is(err, support.ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_failed", "reason is required")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to decide")
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func (h *Admin) Metrics(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireStaff(w, r); !ok {
		return
	}
	rng := r.URL.Query().Get("range")
	days := 7
	if rng == "30d" {
		days = 30
	}
	since := h.now().Add(-time.Duration(days) * 24 * time.Hour)

	m, err := h.Store.Metrics(r.Context(), since, h.AvgHandleMin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to compute metrics")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (h *Admin) GetSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireStaff(w, r); !ok {
		return
	}
	mode, err := h.Settings.Get(r.Context(), support.SettingsKeySupportMode)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to read settings")
		return
	}
	if mode == "" {
		mode = "standard"
	}
	writeJSON(w, http.StatusOK, map[string]string{"mode": mode})
}

type putSettingsRequest struct {
	Mode string `json:"mode"`
}

var validModes = map[string]bool{"standard": true, "approve_all": true, "shadow": true}

func (h *Admin) PutSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	var req putSettingsRequest
	if err := decodeJSON(r, &req); err != nil || !validModes[req.Mode] {
		writeError(w, http.StatusBadRequest, "validation_failed", "mode must be standard|approve_all|shadow")
		return
	}
	if err := h.Settings.Set(r.Context(), support.SettingsKeySupportMode, req.Mode); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to save settings")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"mode": req.Mode})
}

func (h *Admin) ListDataRequests(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	items, err := h.Service.ListOpenDataRequests(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list data requests")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type completeDataRequest struct {
	Note string `json:"note"`
}

func (h *Admin) CompleteDataRequest(w http.ResponseWriter, r *http.Request) {
	v, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	var req completeDataRequest
	_ = decodeJSON(r, &req)

	done, err := h.Service.CompleteDataRequest(r.Context(), r.PathValue("id"), v.ID, req.Note)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to complete request")
		return
	}
	if !done {
		writeError(w, http.StatusConflict, "conflict", "already completed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "completed"})
}

func (h *Admin) DevReset(w http.ResponseWriter, r *http.Request) {
	if !h.DevToolsEnabled {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	_, err := h.Store.Exec(r.Context(), `TRUNCATE support_tickets, support_messages, support_events,
		support_jobs, support_decisions, support_actions, support_data_requests, support_feedback CASCADE`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to reset")
		return
	}
	if h.Broker != nil {
		h.Broker.Reset()
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
