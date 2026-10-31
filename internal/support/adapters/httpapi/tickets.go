package httpapi

import (
	"encoding/json"
	"net/http"
)

type traceStep struct {
	Stage   string `json:"stage"`
	Summary string `json:"summary"`
}

type messageDTO struct {
	ID        string `json:"id"`
	Author    string `json:"author"`
	Text      string `json:"text"`
	CreatedAt string `json:"createdAt"`
}

type ticketSnapshot struct {
	ID       string       `json:"id"`
	Status   string       `json:"status"`
	Autonomy *string      `json:"autonomy,omitempty"`
	Messages []messageDTO `json:"messages"`
	Trace    []traceStep  `json:"trace"`
	LastSeq  int64        `json:"lastSeq"`
}

func (h *Intake) GetTicket(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	userID, anonKeyHash, _ := h.resolveOwner(r)
	ticket, ok, err := h.Store.Get(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load ticket")
		return
	}
	if !ok || !ticket.OwnedBy(userID, anonKeyHash) {
		writeError(w, http.StatusNotFound, "not_found", "ticket not found")
		return
	}

	msgs, err := h.Store.ForTicket(ctx, id, false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load messages")
		return
	}
	events, err := h.Store.After(ctx, id, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load trace")
		return
	}

	snapshot := ticketSnapshot{ID: ticket.ID, Status: string(ticket.Status), LastSeq: ticket.LastSeq}
	if ticket.Autonomy != nil {
		a := string(*ticket.Autonomy)
		snapshot.Autonomy = &a
	}
	for _, m := range msgs {
		snapshot.Messages = append(snapshot.Messages, messageDTO{
			ID: m.ID, Author: m.Author, Text: m.Body, CreatedAt: m.CreatedAt.Format(timeFormat),
		})
	}
	for _, e := range events {
		if e.Type != "trace" || e.Public == nil {
			continue
		}
		var step traceStep
		if err := json.Unmarshal(e.Public, &step); err != nil {
			continue
		}
		snapshot.Trace = append(snapshot.Trace, step)
	}

	writeJSON(w, http.StatusOK, snapshot)
}

const timeFormat = "2006-01-02T15:04:05.000Z07:00"
