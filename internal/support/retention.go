package support

import (
	"context"
	"time"
)

const decisionRetention = 30 * 24 * time.Hour

// PurgeExpired deletes solved tickets older than RetentionDays and
// support_decisions older than 30 days — decisions never hold the
// customer's text, only rule/autonomy/latency, but still age out.
func (s *Service) PurgeExpired(ctx context.Context) error {
	days := s.RetentionDays
	if days <= 0 {
		days = 90
	}
	if _, err := s.Store.PurgeSolvedBefore(ctx, s.now().Add(-time.Duration(days)*24*time.Hour)); err != nil {
		return err
	}
	_, err := s.Store.PurgeBefore(ctx, s.now().Add(-decisionRetention))
	return err
}
