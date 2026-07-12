package leadengine

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	defaultDailyLeadTarget = 100
	dailyRunHour           = 8
)

var londonLocation = time.FixedZone("Europe/London", 0)

func init() {
	location, err := time.LoadLocation("Europe/London")
	if err == nil {
		londonLocation = location
	}
}

// DailyScheduler imports approximately the requested number of raw businesses
// each day. The importer still applies its website, email, closed-place and
// duplicate checks, so the number stored may be lower than the raw target.
type DailyScheduler struct {
	Config Config
	Now    func() time.Time

	mu         sync.Mutex
	lastRunDay string
	running    bool
}

func NewDailySchedulerFromEnv() (*DailyScheduler, error) {
	cfg, err := ConfigFromEnv()
	if err != nil {
		return nil, err
	}
	return &DailyScheduler{Config: cfg, Now: time.Now}, nil
}

func (s *DailyScheduler) Start(ctx context.Context) {
	slog.Info("screenfizz.scheduler.started", "run_time", "08:00 Europe/London", "daily_lead_target", dailyLeadTarget(s.Config))
	go func() {
		s.tick(ctx)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.tick(ctx)
			}
		}
	}()
}

func (s *DailyScheduler) tick(ctx context.Context) {
	now := s.now().In(londonLocation)
	if !isDailyRunHour(now) {
		return
	}
	day := now.Format("2006-01-02")
	if !s.claim(day) {
		return
	}
	go func() {
		defer s.finish()
		if err := s.run(ctx, now); err != nil {
			slog.Error("screenfizz.scheduler.failed", "error", err)
			return
		}
		slog.Info("screenfizz.scheduler.completed", "date", day)
	}()
}

func (s *DailyScheduler) run(ctx context.Context, now time.Time) error {
	counties, err := LoadEnabledSearchAreas(ctx, s.Config)
	if err != nil {
		return err
	}
	county := counties[now.YearDay()%len(counties)]
	perCategory := (dailyLeadTarget(s.Config) + len(defaultSearchStrings) - 1) / len(defaultSearchStrings)
	campaign, err := BoundedApifyCampaign(s.Config, county, perCategory)
	if err != nil {
		return err
	}
	result, err := RunPipeline(ctx, s.Config, campaign)
	if err != nil {
		return err
	}
	slog.Info("screenfizz.scheduler.pipeline_completed", "county", county, "raw_target", perCategory*len(defaultSearchStrings), "found", result.Import.TotalReturned, "inserted", result.Import.Inserted, "duplicates_skipped", result.Import.DuplicatesSkipped, "auto_approved", result.AutoApproved)
	return nil
}

func dailyLeadTarget(cfg Config) int {
	if cfg.DailyLeadTarget > 0 {
		return cfg.DailyLeadTarget
	}
	return defaultDailyLeadTarget
}

func isDailyRunHour(now time.Time) bool {
	return now.Hour() == dailyRunHour
}

func (s *DailyScheduler) claim(day string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running || s.lastRunDay == day {
		return false
	}
	s.running = true
	s.lastRunDay = day
	return true
}

func (s *DailyScheduler) finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
}

func (s *DailyScheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
