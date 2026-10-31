package support

import (
	"context"
	"time"

	"github.com/handziurdmytro/skelar/internal/support/domain"
	"github.com/handziurdmytro/skelar/internal/support/ports"
)

const dataRequestWindow = 30 * 24 * time.Hour

var validDataRequestKinds = map[ports.DataRequestKind]bool{
	ports.DataRequestAccess:        true,
	ports.DataRequestErasure:       true,
	ports.DataRequestRectification: true,
	ports.DataRequestObjection:     true,
}

func dataRequestKind(a domain.Action) ports.DataRequestKind {
	if k := ports.DataRequestKind(a.Params["kind"]); validDataRequestKinds[k] {
		return k
	}
	return ports.DataRequestAccess
}

// createDataRequest is called from recordAction for every ActDataRequest
// action. The bot only ever records the request here — completion (and any
// actual data export/erasure) is always a manual admin action via
// CompleteDataRequest.
func (s *Service) createDataRequest(ctx context.Context, t ports.Ticket, a domain.Action) error {
	_, err := s.Store.CreateDataRequest(ctx, ports.NewDataRequestInput{
		ID:       newID(),
		TicketID: idPtr(t.ID),
		UserID:   t.UserID,
		Kind:     dataRequestKind(a),
		DueAt:    s.now().Add(dataRequestWindow),
	})
	return err
}

func (s *Service) ListOpenDataRequests(ctx context.Context) ([]ports.DataRequest, error) {
	return s.Store.ListOpen(ctx)
}

// CompleteDataRequest marks a request completed. ok=false means it was
// already completed (or never existed) — not an error, just nothing to do.
func (s *Service) CompleteDataRequest(ctx context.Context, id, decidedBy, note string) (bool, error) {
	return s.Store.Complete(ctx, id, decidedBy, note)
}
