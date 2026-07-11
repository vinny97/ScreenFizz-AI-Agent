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
)

const apifyResponseLimit = 256 << 20

var defaultSearchStrings = []string{
	"restaurant",
	"cafe",
	"pub",
	"bar",
	"hotel",
	"gym",
	"estate agent",
	"dentist",
	"hair salon",
	"beauty salon",
	"takeaway",
	"car dealership",
}

type ApifyInput struct {
	SearchStringsArray        []string `json:"searchStringsArray"`
	LocationQuery             string   `json:"locationQuery"`
	Language                  string   `json:"language"`
	MaxCrawledPlacesPerSearch int      `json:"maxCrawledPlacesPerSearch"`
	SkipClosedPlaces          bool     `json:"skipClosedPlaces"`
	ScrapeContacts            bool     `json:"scrapeContacts"`
	ScrapePlaceDetailPage     bool     `json:"scrapePlaceDetailPage"`
}

// ApifyCampaign describes the ScreenFizz-specific actor configuration.
// Actor execution will be implemented in a later step.
type ApifyCampaign struct {
	Name        string
	ApifyAPIURL string
	ApifyInput  json.RawMessage
}

func DefaultApifyInput(county string) ApifyInput {
	searchStrings := append([]string(nil), defaultSearchStrings...)
	return ApifyInput{
		SearchStringsArray:        searchStrings,
		LocationQuery:             strings.TrimSpace(county) + ", England",
		Language:                  "en",
		MaxCrawledPlacesPerSearch: 100,
		SkipClosedPlaces:          true,
		ScrapeContacts:            true,
		ScrapePlaceDetailPage:     false,
	}
}

func DefaultApifyCampaign(cfg Config, county string) (ApifyCampaign, error) {
	input, err := json.Marshal(DefaultApifyInput(county))
	if err != nil {
		return ApifyCampaign{}, err
	}
	return ApifyCampaign{
		Name:        "ScreenFizz " + strings.TrimSpace(county) + " Businesses",
		ApifyAPIURL: cfg.ApifyAPIURL,
		ApifyInput:  input,
	}, nil
}

// BoundedApifyCampaign runs the standard ScreenFizz categories for one county
// with a caller-selected cap per category.
func BoundedApifyCampaign(cfg Config, county string, maxPerSearch int) (ApifyCampaign, error) {
	if maxPerSearch <= 0 {
		return ApifyCampaign{}, errors.New("max results per category must be positive")
	}
	input := DefaultApifyInput(county)
	input.MaxCrawledPlacesPerSearch = maxPerSearch
	encoded, err := json.Marshal(input)
	if err != nil {
		return ApifyCampaign{}, err
	}
	return ApifyCampaign{
		Name:        "ScreenFizz " + strings.TrimSpace(county) + " Bounded Businesses",
		ApifyAPIURL: cfg.ApifyAPIURL,
		ApifyInput:  encoded,
	}, nil
}

// TestApifyCampaign is a small, fixed actor run for verifying the ScreenFizz
// Apify-to-Supabase path without using the full production category set.
func TestApifyCampaign(cfg Config, county string) (ApifyCampaign, error) {
	input, err := json.Marshal(ApifyInput{
		SearchStringsArray:        []string{"restaurant"},
		LocationQuery:             strings.TrimSpace(county) + ", England",
		Language:                  "en",
		MaxCrawledPlacesPerSearch: 5,
		SkipClosedPlaces:          true,
		ScrapeContacts:            true,
		ScrapePlaceDetailPage:     false,
	})
	if err != nil {
		return ApifyCampaign{}, err
	}
	return ApifyCampaign{
		Name:        "ScreenFizz " + strings.TrimSpace(county) + " Restaurant Test",
		ApifyAPIURL: cfg.ApifyAPIURL,
		ApifyInput:  input,
	}, nil
}

type ApifyClient struct {
	token        string
	httpClient   *http.Client
	pollInterval time.Duration
}

func NewApifyClient(cfg Config) (*ApifyClient, error) {
	token := strings.TrimSpace(cfg.ApifyAPIToken)
	if token == "" {
		return nil, errors.New("APIFY_API_TOKEN is required")
	}
	return &ApifyClient{
		token:        token,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		pollInterval: 10 * time.Second,
	}, nil
}

func (c *ApifyClient) Run(ctx context.Context, campaign ApifyCampaign) (json.RawMessage, error) {
	startURL, err := withToken(campaign.ApifyAPIURL, c.token)
	if err != nil {
		return nil, fmt.Errorf("invalid Apify actor URL: %w", err)
	}

	var started apifyResponse
	if err := c.doJSON(ctx, http.MethodPost, startURL, campaign.ApifyInput, &started); err != nil {
		return nil, fmt.Errorf("start Apify actor: %w", err)
	}
	if started.Data.ID == "" {
		return nil, errors.New("start Apify actor: response has no run ID")
	}
	slog.Info("screenfizz.leadengine.actor_started", "run_id", started.Data.ID)

	base, err := url.Parse(startURL)
	if err != nil {
		return nil, fmt.Errorf("parse Apify actor URL: %w", err)
	}
	runURL, err := withToken(base.Scheme+"://"+base.Host+"/v2/actor-runs/"+url.PathEscape(started.Data.ID), c.token)
	if err != nil {
		return nil, err
	}

	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}

		var run apifyResponse
		if err := c.doJSON(ctx, http.MethodGet, runURL, nil, &run); err != nil {
			return nil, fmt.Errorf("poll Apify actor: %w", err)
		}
		switch run.Data.Status {
		case "SUCCEEDED":
			slog.Info("screenfizz.leadengine.actor_finished", "run_id", started.Data.ID, "status", run.Data.Status)
			return c.DownloadRunDataset(ctx, base.Scheme+"://"+base.Host, started.Data.ID)
		case "FAILED", "TIMED-OUT", "ABORTED":
			return nil, fmt.Errorf("Apify actor ended with status %s", run.Data.Status)
		}
	}
}

// DownloadRunDataset downloads the default dataset for a completed actor run.
// It permits an interrupted command to resume the import without starting a
// second actor run.
func (c *ApifyClient) DownloadRunDataset(ctx context.Context, apiBaseURL, runID string) (json.RawMessage, error) {
	base, err := url.Parse(strings.TrimSpace(apiBaseURL))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, errors.New("Apify API base URL must be absolute")
	}
	if strings.TrimSpace(runID) == "" {
		return nil, errors.New("Apify run ID is required")
	}
	datasetURL, err := withToken(base.Scheme+"://"+base.Host+"/v2/actor-runs/"+url.PathEscape(runID)+"/dataset/items", c.token)
	if err != nil {
		return nil, err
	}
	return c.downloadJSON(ctx, datasetURL)
}

type apifyResponse struct {
	Data struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"data"`
}

func withToken(rawURL string, token string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("URL must be absolute")
	}
	query := parsed.Query()
	query.Set("token", token)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (c *ApifyClient) doJSON(ctx context.Context, method string, requestURL string, body []byte, target any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, apifyResponseLimit))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Apify returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}
	if err := json.Unmarshal(responseBody, target); err != nil {
		return fmt.Errorf("decode Apify response: %w", err)
	}
	return nil
}

func (c *ApifyClient) downloadJSON(ctx context.Context, datasetURL string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, datasetURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download Apify dataset: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, apifyResponseLimit))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read Apify dataset: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download Apify dataset: Apify returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if !json.Valid(body) {
		return nil, errors.New("download Apify dataset: response is not valid JSON")
	}
	return json.RawMessage(body), nil
}
