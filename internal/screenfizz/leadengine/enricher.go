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
	"strings"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/security"
)

const (
	enrichmentBatchSize = 25
	websiteHTMLLimit    = 10 << 20
)

type enrichmentProspect struct {
	ID       string `json:"id"`
	Business struct {
		BusinessName string `json:"business_name"`
		Website      string `json:"website"`
	} `json:"screenfizz_businesses"`
}

// EnrichProspects downloads and stores homepage HTML for the next 25
// unenriched ScreenFizz prospects. Individual download failures remain
// unenriched so a future command run can retry them.
func EnrichProspects(ctx context.Context, cfg Config) error {
	_, _, err := enrichProspectBatch(ctx, cfg)
	return err
}

// EnrichAllProspects processes every currently queued prospect. A failed
// homepage download stops this run so it can be retried by the next schedule
// rather than silently marking the prospect complete.
func EnrichAllProspects(ctx context.Context, cfg Config) error {
	for {
		processed, failed, err := enrichProspectBatch(ctx, cfg)
		if err != nil {
			return err
		}
		if processed == 0 {
			return nil
		}
		if failed > 0 {
			return fmt.Errorf("failed to download or save %d ScreenFizz prospect websites", failed)
		}
	}
}

func enrichProspectBatch(ctx context.Context, cfg Config) (int, int, error) {
	prospects, err := nextUnenrichedProspects(ctx, cfg)
	if err != nil {
		return 0, 0, err
	}
	websiteClient := security.NewRedirectFollowingSafeClient(30*time.Second, 5)
	failed := 0
	for _, prospect := range prospects {
		businessName := strings.TrimSpace(prospect.Business.BusinessName)
		slog.Info("Processing: " + businessName)

		html, err := downloadHomepage(ctx, websiteClient, prospect.Business.Website)
		if err != nil {
			slog.Error("Failed to download website", "business_name", businessName, "error", err)
			failed++
			continue
		}
		slog.Info("Downloaded website", "business_name", businessName)

		if err := saveWebsiteHTML(ctx, cfg, prospect.ID, html); err != nil {
			slog.Error("Failed to save HTML", "business_name", businessName, "error", err)
			failed++
			continue
		}
		slog.Info("Saved HTML", "business_name", businessName)
	}
	return len(prospects), failed, nil
}

func nextUnenrichedProspects(ctx context.Context, cfg Config) ([]enrichmentProspect, error) {
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable)
	query := url.Values{
		"select":   {"id,screenfizz_businesses(business_name,website)"},
		"enriched": {"eq.false"},
		"order":    {"created_at.asc"},
		"limit":    {fmt.Sprintf("%d", enrichmentBatchSize)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create ScreenFizz prospects enrichment request: %w", err)
	}
	setSupabaseHeaders(req, cfg)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("list unenriched ScreenFizz prospects: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read unenriched ScreenFizz prospects response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("list unenriched ScreenFizz prospects: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var prospects []enrichmentProspect
	if err := json.Unmarshal(body, &prospects); err != nil {
		return nil, fmt.Errorf("decode unenriched ScreenFizz prospects response: %w", err)
	}
	return prospects, nil
}

func downloadHomepage(ctx context.Context, client *http.Client, website string) ([]byte, error) {
	website = strings.TrimSpace(website)
	if website == "" {
		return nil, errors.New("business has no website")
	}
	if !strings.HasPrefix(website, "http://") && !strings.HasPrefix(website, "https://") {
		website = "https://" + website
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, website, nil)
	if err != nil {
		return nil, fmt.Errorf("create website request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ScreenFizzLeadEngine/1.0)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download website: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, websiteHTMLLimit+1))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read website response: %w", err)
	}
	if len(body) > websiteHTMLLimit {
		return nil, fmt.Errorf("website HTML exceeds %d-byte limit", websiteHTMLLimit)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("website returned %s", resp.Status)
	}
	return body, nil
}

func saveWebsiteHTML(ctx context.Context, cfg Config, prospectID string, html []byte) error {
	body, err := json.Marshal(map[string]any{
		"website_html": string(html),
		"enriched":     true,
	})
	if err != nil {
		return fmt.Errorf("encode website HTML: %w", err)
	}
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable) + "?id=eq." + url.QueryEscape(prospectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ScreenFizz prospect HTML update request: %w", err)
	}
	setSupabaseHeaders(req, cfg)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "return=minimal")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("save ScreenFizz prospect HTML: %w", err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return fmt.Errorf("read ScreenFizz prospect HTML update response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("save ScreenFizz prospect HTML: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}
	return nil
}
