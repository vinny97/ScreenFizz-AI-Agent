package leadengine

import (
	"context"
	"log/slog"
	"time"
)

// StartDailyScheduler runs job every day at 12:00 UTC until ctx is canceled.
func StartDailyScheduler(ctx context.Context, job func(context.Context) error) {
	go func() {
		for {
			now := time.Now().UTC()
			next := nextDailyRun(now)
			slog.Info("leadengine.scheduler.waiting", "next_run", next.Format(time.RFC3339))
			timer := time.NewTimer(time.Until(next))
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				slog.Info("leadengine.scheduler.stopped")
				return
			case <-timer.C:
			}
			slog.Info("leadengine.scheduler.triggered")
			if err := job(ctx); err != nil {
				slog.Error("leadengine.scheduler.job_failed", "error", err)
			}
		}
	}()
}

func nextDailyRun(now time.Time) time.Time {
	now = now.UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)
	if !now.Before(next) {
		next = next.Add(24 * time.Hour)
	}
	return next
}
