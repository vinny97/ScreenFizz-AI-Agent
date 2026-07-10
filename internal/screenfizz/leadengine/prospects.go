package leadengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const prospectPageSize = 1000

type ProspectResult struct {
	Added   int
	Skipped int
}

// SyncProspects adds each eligible, uncontacted master business to the
// prospect queue once. The prospect retains a foreign-key reference so the
// businesses table remains the source of truth.
func SyncProspects(ctx context.Context, cfg Config) (ProspectResult, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	result := ProspectResult{}
	for start := 0; ; {
		businesses, total, err := eligibleBusinesses(ctx, client, cfg, start)
		if err != nil {
			return ProspectResult{}, err
		}
		if len(businesses) == 0 {
			return result, nil
		}
		added, err := addProspects(ctx, client, cfg, businesses)
		if err != nil {
			return ProspectResult{}, err
		}
		result.Added += added
		result.Skipped += len(businesses) - added
		if start+len(businesses) >= total {
			return result, nil
		}
		start += len(businesses)
	}
}

func eligibleBusinesses(ctx context.Context, client *http.Client, cfg Config, start int) ([]Business, int, error) {
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.BusinessesTable)
	query := url.Values{
		"select":    {"id"},
		"contacted": {"eq.false"},
		"website":   {"not.is.null"},
		"email":     {"not.is.null"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("create eligible ScreenFizz businesses request: %w", err)
	}
	setSupabaseHeaders(req, cfg)
	req.Header.Set("Range", fmt.Sprintf("%d-%d", start, start+prospectPageSize-1))
	req.Header.Set("Prefer", "count=exact")

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("list eligible ScreenFizz businesses: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, 0, fmt.Errorf("read eligible ScreenFizz businesses response: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, 0, fmt.Errorf("list eligible ScreenFizz businesses: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var businesses []Business
	if err := json.Unmarshal(body, &businesses); err != nil {
		return nil, 0, fmt.Errorf("decode eligible ScreenFizz businesses response: %w", err)
	}
	total, ok := contentRangeTotal(resp.Header.Get("Content-Range"))
	if !ok {
		total = start + len(businesses)
	}
	return businesses, total, nil
}

func addProspects(ctx context.Context, client *http.Client, cfg Config, businesses []Business) (int, error) {
	rows := make([]map[string]string, 0, len(businesses))
	for _, business := range businesses {
		if strings.TrimSpace(business.ID) != "" {
			rows = append(rows, map[string]string{"business_id": business.ID})
		}
	}
	if len(rows) == 0 {
		return 0, nil
	}
	body, err := json.Marshal(rows)
	if err != nil {
		return 0, fmt.Errorf("encode ScreenFizz prospects: %w", err)
	}
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable) + "?on_conflict=business_id"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create ScreenFizz prospects insert request: %w", err)
	}
	setSupabaseHeaders(req, cfg)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "resolution=ignore-duplicates,return=representation")
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("insert ScreenFizz prospects: %w", err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return 0, fmt.Errorf("read ScreenFizz prospects insert response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return 0, fmt.Errorf("insert ScreenFizz prospects: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}
	var inserted []struct {
		BusinessID string `json:"business_id"`
	}
	if err := json.Unmarshal(responseBody, &inserted); err != nil {
		return 0, fmt.Errorf("decode ScreenFizz prospects insert response: %w", err)
	}
	return len(inserted), nil
}

func setSupabaseHeaders(req *http.Request, cfg Config) {
	req.Header.Set("apikey", cfg.SupabaseServiceRoleKey)
	req.Header.Set("Authorization", "Bearer "+cfg.SupabaseServiceRoleKey)
}
