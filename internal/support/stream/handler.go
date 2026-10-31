package stream

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/handziurdmytro/skelar/internal/support/ports"
)

type EventSource interface {
	After(ctx context.Context, ticketID string, seq int64) ([]ports.Event, error)
}

// Handler serves GET /api/support/tickets/{id}/events. Authorize is
// injected so this package never has to know how ownership/staff-ness is
// determined (session, cookie, whatever adapters/httpapi uses).
type Handler struct {
	Broker    *Broker
	Store     EventSource
	Authorize func(r *http.Request, ticketID string) (staff bool, ok bool)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ticketID := r.PathValue("id")
	staff, ok := h.Authorize(r, ticketID)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	live, unsubscribe := h.Broker.Subscribe(ticketID)
	defer unsubscribe()

	after, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := io.WriteString(w, ": connected\n\n"); err != nil || rc.Flush() != nil {
		return
	}

	if backlog, err := h.Store.After(r.Context(), ticketID, after); err == nil {
		for _, e := range backlog {
			if writeEvent(w, e, staff) != nil {
				return
			}
			after = e.Seq
		}
	}
	if rc.Flush() != nil {
		return
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		case e := <-live:
			if e.Seq <= after {
				continue
			}
			if writeEvent(w, e, staff) != nil || rc.Flush() != nil {
				return
			}
			after = e.Seq
		}
	}
}

func writeEvent(w http.ResponseWriter, e ports.Event, staff bool) error {
	payload := e.Staff
	if !staff {
		if e.Public == nil {
			return nil
		}
		payload = e.Public
	}
	_, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Type, payload)
	return err
}
