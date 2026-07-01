package leadengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ScheduledCampaign struct {
	ID                 string
	Name               string
	ApifyAPIURL        string
	ApifyInput         json.RawMessage
	EmailSubject       string
	EmailTemplateHTML  string
	EmailTemplateText  string
	EmailSignatureHTML string
	EmailSignatureText string
	SenderName         string
	SenderEmail        string
	CTAText            string
	CTAURL             string
	Enabled            bool
	DailyLimit         int
	ScheduleTime       string
	Timezone           string
	LastRunAt          *time.Time
}

type CampaignRunCounts struct {
	ImportCount    int
	QualifiedCount int
	QueuedCount    int
	GeneratedCount int
	SentCount      int
}

type CampaignScheduler struct {
	Supabase  *Client
	Apify     *ApifyClient
	Sender    *TestSender
	OutputDir string
	Now       func() time.Time

	mu      sync.Mutex
	running map[string]struct{}
}

func NewCampaignSchedulerFromEnv(outputDir string) (*CampaignScheduler, error) {
	supabase, err := NewFromEnv()
	if err != nil {
		return nil, err
	}
	apify, err := NewApifyClientFromEnv()
	if err != nil {
		return nil, err
	}
	sender, err := NewTestSenderFromEnv()
	if err != nil {
		return nil, err
	}
	return &CampaignScheduler{
		Supabase:  supabase,
		Apify:     apify,
		Sender:    sender,
		OutputDir: outputDir,
		Now:       time.Now,
		running:   make(map[string]struct{}),
	}, nil
}

func (s *CampaignScheduler) Start(ctx context.Context) {
	go func() {
		s.runOnce(ctx)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				slog.Info("leadengine.scheduler.stopped")
				return
			case <-ticker.C:
				s.runOnce(ctx)
			}
		}
	}()
}

func (s *CampaignScheduler) runOnce(ctx context.Context) {
	campaigns, err := s.Supabase.ListScheduledCampaigns(ctx)
	if err != nil {
		slog.Error("leadengine.scheduler.list_campaigns_failed", "error", err)
		return
	}
	now := s.now()
	for _, campaign := range campaigns {
		if !campaignDue(now, campaign) {
			continue
		}
		if !s.markRunning(campaign.ID) {
			continue
		}
		go func(c ScheduledCampaign) {
			defer s.unmarkRunning(c.ID)
			if err := s.Supabase.UpdateCampaignLastRunAt(ctx, c.ID, s.now().UTC()); err != nil {
				slog.Error("leadengine.scheduler.claim_failed", "campaign", c.Name, "error", err)
				return
			}
			start := time.Now()
			slog.Info("leadengine.scheduler.campaign_started", "campaign", c.Name)
			counts, err := s.runCampaign(ctx, c)
			duration := time.Since(start)
			if err != nil {
				slog.Error("leadengine.scheduler.campaign_failed",
					"campaign", c.Name,
					"import_count", counts.ImportCount,
					"qualified_count", counts.QualifiedCount,
					"queued_count", counts.QueuedCount,
					"generated_count", counts.GeneratedCount,
					"sent_count", counts.SentCount,
					"duration", duration.String(),
					"error", err)
				return
			}
			slog.Info("leadengine.scheduler.campaign_completed",
				"campaign", c.Name,
				"import_count", counts.ImportCount,
				"qualified_count", counts.QualifiedCount,
				"queued_count", counts.QueuedCount,
				"generated_count", counts.GeneratedCount,
				"sent_count", counts.SentCount,
				"duration", duration.String())
		}(campaign)
	}
}

func (s *CampaignScheduler) runCampaign(ctx context.Context, campaign ScheduledCampaign) (CampaignRunCounts, error) {
	counts := CampaignRunCounts{}

	dataset, err := s.Apify.Run(ctx, &ActiveCampaign{
		Name:        campaign.Name,
		ApifyAPIURL: campaign.ApifyAPIURL,
		ApifyInput:  campaign.ApifyInput,
	})
	if err != nil {
		return counts, err
	}
	job := &Job{OutputDir: s.OutputDir, Now: s.now}
	if _, err := job.saveDataset(campaign.Name, dataset); err != nil {
		return counts, err
	}

	importResult, err := s.Supabase.ImportLeads(ctx, campaign.Name, dataset)
	if err != nil {
		return counts, err
	}
	counts.ImportCount = importResult.Imported

	qualifyResult, err := s.Supabase.QualifyNewLeadsForCampaign(ctx, campaign.Name)
	if err != nil {
		return counts, err
	}
	counts.QualifiedCount = qualifyResult.Qualified

	limit := campaign.SendLimit()
	queueResult, err := s.Supabase.QueueReadyLeadsForCampaign(ctx, campaign.Name, limit)
	if err != nil {
		return counts, err
	}
	counts.QueuedCount = queueResult.Queued

	generateResult, err := s.Supabase.GenerateQueuedEmailsForCampaign(ctx, campaign)
	if err != nil {
		return counts, err
	}
	counts.GeneratedCount = generateResult.Generated

	sendResult, err := s.Supabase.SendReadyLeadsForCampaign(ctx, s.Sender, campaign, limit)
	if err != nil {
		return counts, err
	}
	counts.SentCount = sendResult.Sent

	return counts, nil
}

func (s *CampaignScheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *CampaignScheduler) markRunning(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, found := s.running[id]; found {
		return false
	}
	s.running[id] = struct{}{}
	return true
}

func (s *CampaignScheduler) unmarkRunning(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.running, id)
}

func (c ScheduledCampaign) SendLimit() int {
	if c.DailyLimit <= 0 {
		return 100
	}
	if c.DailyLimit > 100 {
		return 100
	}
	return c.DailyLimit
}

func (c *Client) ListScheduledCampaigns(ctx context.Context) ([]ScheduledCampaign, error) {
	rows, err := c.ListCampaigns(ctx)
	if err != nil {
		return nil, err
	}
	campaigns := make([]ScheduledCampaign, 0)
	for _, row := range rows {
		campaign, err := parseScheduledCampaign(row)
		if err != nil {
			return nil, err
		}
		if !campaign.Enabled || !campaignIsActive(row) {
			continue
		}
		campaigns = append(campaigns, campaign)
	}
	return campaigns, nil
}

func parseScheduledCampaign(row Campaign) (ScheduledCampaign, error) {
	id, err := campaignID(row["id"])
	if err != nil {
		return ScheduledCampaign{}, err
	}
	name, _ := row["name"].(string)
	if strings.TrimSpace(name) == "" {
		return ScheduledCampaign{}, errors.New("active campaign has no name")
	}
	apiURL, _ := row["apify_api_url"].(string)
	if strings.TrimSpace(apiURL) == "" {
		return ScheduledCampaign{}, errors.New("active campaign has no apify_api_url")
	}
	input, err := campaignInput(row["apify_input"])
	if err != nil {
		return ScheduledCampaign{}, err
	}
	lastRunAt, err := campaignLastRunAt(row["last_run_at"])
	if err != nil {
		return ScheduledCampaign{}, err
	}
	return ScheduledCampaign{
		ID:                 id,
		Name:               name,
		ApifyAPIURL:        apiURL,
		ApifyInput:         input,
		EmailSubject:       stringValue(row["email_subject"]),
		EmailTemplateHTML:  stringValue(row["email_template_html"]),
		EmailTemplateText:  stringValue(row["email_template_text"]),
		EmailSignatureHTML: stringValue(row["email_signature_html"]),
		EmailSignatureText: stringValue(row["email_signature_text"]),
		SenderName:         stringValue(row["sender_name"]),
		SenderEmail:        stringValue(row["sender_email"]),
		CTAText:            stringValue(row["cta_text"]),
		CTAURL:             stringValue(row["cta_url"]),
		Enabled:            boolValue(row["enabled"]),
		DailyLimit:         intValue(row["daily_limit"]),
		ScheduleTime:       stringValue(row["schedule_time"]),
		Timezone:           stringValue(row["timezone"]),
		LastRunAt:          lastRunAt,
	}, nil
}

func campaignDue(now time.Time, campaign ScheduledCampaign) bool {
	location, err := time.LoadLocation(strings.TrimSpace(campaign.Timezone))
	if err != nil || strings.TrimSpace(campaign.ScheduleTime) == "" {
		return false
	}
	hour, minute, second, ok := parseScheduleTime(campaign.ScheduleTime)
	if !ok {
		return false
	}
	localNow := now.In(location)
	scheduled := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, second, 0, location)
	if localNow.Before(scheduled) {
		return false
	}
	if campaign.LastRunAt == nil {
		return true
	}
	lastLocal := campaign.LastRunAt.In(location)
	return lastLocal.Year() != localNow.Year() || lastLocal.YearDay() != localNow.YearDay()
}

func parseScheduleTime(value string) (int, int, int, bool) {
	for _, layout := range []string{"15:04:05", "15:04"} {
		t, err := time.Parse(layout, strings.TrimSpace(value))
		if err == nil {
			return t.Hour(), t.Minute(), t.Second(), true
		}
	}
	return 0, 0, 0, false
}

func campaignLastRunAt(value any) (*time.Time, error) {
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		parsed, err := time.Parse(layout, text)
		if err == nil {
			return &parsed, nil
		}
	}
	return nil, fmt.Errorf("invalid campaign last_run_at %q", text)
}

func campaignID(value any) (string, error) {
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			return v, nil
		}
	case json.Number:
		if v.String() != "" {
			return v.String(), nil
		}
	case float64:
		return strconv.FormatInt(int64(v), 10), nil
	}
	return "", errors.New("campaign has no valid id")
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func boolValue(value any) bool {
	flag, _ := value.(bool)
	return flag
}

func intValue(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case int:
		return v
	}
	return 0
}

func (c *Client) UpdateCampaignLastRunAt(ctx context.Context, id string, ts time.Time) error {
	body, err := json.Marshal(map[string]string{"last_run_at": ts.Format(time.RFC3339)})
	if err != nil {
		return fmt.Errorf("encode campaign last_run_at: %w", err)
	}
	query := url.Values{}
	query.Set("id", "eq."+id)
	requestURL := c.baseURL + "/rest/v1/campaigns?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, requestURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create campaign last_run_at request: %w", err)
	}
	c.setSupabaseHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "return=minimal")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("update campaign last_run_at: %w", err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return fmt.Errorf("read campaign last_run_at response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("update campaign last_run_at: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func schedulerOutputDir() string {
	return filepath.Join("data", "leads")
}
