package leadengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	screenFizzSendStartHour = 10
	screenFizzSendEndHour   = 16
	screenFizzSendInterval  = 108 * time.Second // 200 sends across six hours.
	screenFizzSendTick      = 30 * time.Second
)

// SendScheduler spaces approved ScreenFizz emails throughout the configured
// daytime window. The most recent sent_at value is read from Supabase, so a
// service restart cannot create a burst of sends.
type SendScheduler struct {
	Config Config
	Now    func() time.Time
}

func NewSendSchedulerFromEnv() (*SendScheduler, error) {
	cfg, err := ConfigFromEnv()
	if err != nil {
		return nil, err
	}
	return &SendScheduler{Config: cfg, Now: time.Now}, nil
}

func (s *SendScheduler) Start(ctx context.Context) {
	slog.Info("screenfizz.send_scheduler.started", "window", "10:00-16:00 Europe/London", "daily_limit", s.Config.DailySendLimit, "spacing", screenFizzSendInterval)
	go func() {
		s.tick(ctx)
		ticker := time.NewTicker(screenFizzSendTick)
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

func (s *SendScheduler) tick(ctx context.Context) {
	if !s.Config.AutoApprove {
		return
	}
	// The main pipeline approves newly created drafts at the end of a successful
	// run. This catch-up keeps the queue moving if a previous run stopped after
	// generation, or if a draft was saved outside that run. Approval never sends
	// immediately; sending still waits for the daytime window below.
	approved, err := ApproveReadyToSendProspects(ctx, s.Config)
	if err != nil {
		slog.Error("screenfizz.send_scheduler.auto_approve_failed", "error", err)
		return
	}
	if approved > 0 {
		slog.Info("screenfizz.send_scheduler.auto_approved", "count", approved)
	}
	now := s.now().In(londonLocation)
	if now.Hour() < screenFizzSendStartHour || now.Hour() >= screenFizzSendEndHour {
		return
	}
	limit := s.Config.DailySendLimit
	if limit <= 0 {
		limit = defaultDailySendLimit
	}
	sentToday, err := countScreenFizzEmailsSentToday(ctx, s.Config, now.UTC())
	if err != nil {
		slog.Error("screenfizz.send_scheduler.count_failed", "error", err)
		return
	}
	if sentToday >= limit {
		return
	}
	lastSentAt, err := latestScreenFizzSendToday(ctx, s.Config, now)
	if err != nil {
		slog.Error("screenfizz.send_scheduler.last_send_lookup_failed", "error", err)
		return
	}
	if lastSentAt != nil && now.Sub(*lastSentAt) < screenFizzSendInterval {
		return
	}
	result, err := SendOneApprovedProspect(ctx, s.Config)
	if err != nil {
		slog.Error("screenfizz.send_scheduler.send_failed", "error", err)
		return
	}
	if result.Sent > 0 || result.Failed > 0 {
		slog.Info("screenfizz.send_scheduler.send_completed", "sent", result.Sent, "failed", result.Failed, "sent_today_before", sentToday)
	}
}

func (s *SendScheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func latestScreenFizzSendToday(ctx context.Context, cfg Config, now time.Time) (*time.Time, error) {
	localNow := now.In(londonLocation)
	dayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, londonLocation).UTC()
	dayEnd := dayStart.In(londonLocation).AddDate(0, 0, 1).UTC()
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable)
	query := url.Values{
		"select":  {"sent_at"},
		"status":  {"eq.sent"},
		"sent_at": {"gte." + dayStart.Format(time.RFC3339), "lt." + dayEnd.Format(time.RFC3339)},
		"order":   {"sent_at.desc"},
		"limit":   {"1"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create ScreenFizz latest send request: %w", err)
	}
	setSupabaseHeaders(req, cfg)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("list ScreenFizz latest send: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read ScreenFizz latest send: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("list ScreenFizz latest send: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var rows []struct {
		SentAt time.Time `json:"sent_at"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("decode ScreenFizz latest send: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0].SentAt, nil
}
