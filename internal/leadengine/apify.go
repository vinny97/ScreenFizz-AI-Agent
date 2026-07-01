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
	"os"
	"strings"
	"time"
)

const apifyResponseLimit = 64 << 20

// ActiveCampaign contains the fields needed to start an Apify run.
type ActiveCampaign struct {
	Name        string
	ApifyAPIURL string
	ApifyInput  json.RawMessage
}

// GetActiveCampaignName returns the name of the single campaign marked active.
func (c *Client) GetActiveCampaignName(ctx context.Context) (string, error) {
	campaigns, err := c.ListCampaigns(ctx)
	if err != nil {
		return "", err
	}
	var name string
	for _, campaign := range campaigns {
		if !campaignIsActive(campaign) {
			continue
		}
		if name != "" {
			return "", errors.New("multiple active campaigns found")
		}
		campaignName, ok := campaign["name"].(string)
		if !ok || strings.TrimSpace(campaignName) == "" {
			return "", errors.New("active campaign has no name")
		}
		name = campaignName
	}
	if name == "" {
		return "", errors.New("no active campaign found")
	}
	return name, nil
}

// GetActiveCampaign returns the single campaign marked active.
func (c *Client) GetActiveCampaign(ctx context.Context) (*ActiveCampaign, error) {
	campaigns, err := c.ListCampaigns(ctx)
	if err != nil {
		return nil, err
	}

	var active Campaign
	for _, campaign := range campaigns {
		if !campaignIsActive(campaign) {
			continue
		}
		if active != nil {
			return nil, errors.New("multiple active campaigns found")
		}
		active = campaign
	}
	if active == nil {
		return nil, errors.New("no active campaign found")
	}

	name, ok := active["name"].(string)
	if !ok || strings.TrimSpace(name) == "" {
		return nil, errors.New("active campaign has no name")
	}
	apiURL, ok := active["apify_api_url"].(string)
	if !ok || strings.TrimSpace(apiURL) == "" {
		return nil, errors.New("active campaign has no apify_api_url")
	}
	input, err := campaignInput(active["apify_input"])
	if err != nil {
		return nil, err
	}
	return &ActiveCampaign{Name: name, ApifyAPIURL: apiURL, ApifyInput: input}, nil
}

func campaignIsActive(campaign Campaign) bool {
	if active, ok := campaign["active"].(bool); ok {
		return active
	}
	if active, ok := campaign["is_active"].(bool); ok {
		return active
	}
	status, _ := campaign["status"].(string)
	return strings.EqualFold(strings.TrimSpace(status), "active")
}

func campaignInput(value any) (json.RawMessage, error) {
	if value == nil {
		return nil, errors.New("active campaign has no apify_input")
	}
	if text, ok := value.(string); ok {
		if !json.Valid([]byte(text)) {
			return nil, errors.New("active campaign apify_input is not valid JSON")
		}
		return json.RawMessage(text), nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode active campaign apify_input: %w", err)
	}
	return encoded, nil
}

// ApifyClient starts Actor runs and downloads their default dataset.
type ApifyClient struct {
	token        string
	httpClient   *http.Client
	pollInterval time.Duration
}

// NewApifyClientFromEnv creates a client using APIFY_API_TOKEN.
func NewApifyClientFromEnv() (*ApifyClient, error) {
	token := strings.TrimSpace(os.Getenv("APIFY_API_TOKEN"))
	if token == "" {
		return nil, errors.New("APIFY_API_TOKEN is required")
	}
	return &ApifyClient{
		token:        token,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		pollInterval: 10 * time.Second,
	}, nil
}

// Run starts the campaign's Actor, waits for success, and returns its dataset
// items as JSON.
func (c *ApifyClient) Run(ctx context.Context, campaign *ActiveCampaign) (json.RawMessage, error) {
	if campaign == nil {
		return nil, errors.New("active campaign is required")
	}
	startURL, err := withToken(campaign.ApifyAPIURL, c.token)
	if err != nil {
		return nil, fmt.Errorf("invalid apify_api_url: %w", err)
	}

	var started apifyResponse
	if err := c.doJSON(ctx, http.MethodPost, startURL, campaign.ApifyInput, &started); err != nil {
		return nil, fmt.Errorf("start Apify run: %w", err)
	}
	if started.Data.ID == "" {
		return nil, errors.New("start Apify run: response has no run ID")
	}

	base, err := url.Parse(startURL)
	if err != nil {
		return nil, fmt.Errorf("parse Apify URL: %w", err)
	}
	runURL := base.Scheme + "://" + base.Host + "/v2/actor-runs/" + url.PathEscape(started.Data.ID)
	runURL, err = withToken(runURL, c.token)
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
			return nil, fmt.Errorf("poll Apify run: %w", err)
		}
		switch run.Data.Status {
		case "SUCCEEDED":
			datasetURL := base.Scheme + "://" + base.Host + "/v2/actor-runs/" + url.PathEscape(started.Data.ID) + "/dataset/items"
			datasetURL, err = withToken(datasetURL, c.token)
			if err != nil {
				return nil, err
			}
			return c.downloadJSON(ctx, datasetURL)
		case "FAILED", "TIMED-OUT", "ABORTED":
			return nil, fmt.Errorf("Apify run ended with status %s", run.Data.Status)
		}
	}
}

type apifyResponse struct {
	Data struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"data"`
}

func withToken(rawURL, token string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("URL must be absolute")
	}
	query := parsed.Query()
	query.Set("token", token)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (c *ApifyClient) doJSON(ctx context.Context, method, requestURL string, body []byte, target any) error {
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
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, apifyResponseLimit))
	if err != nil {
		return errors.Join(err, resp.Body.Close())
	}
	if err := resp.Body.Close(); err != nil {
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, apifyResponseLimit))
	if err != nil {
		return nil, fmt.Errorf("read Apify dataset: %w", errors.Join(err, resp.Body.Close()))
	}
	if err := resp.Body.Close(); err != nil {
		return nil, fmt.Errorf("close Apify dataset response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download Apify dataset: Apify returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if !json.Valid(body) {
		return nil, errors.New("download Apify dataset: response is not valid JSON")
	}
	return json.RawMessage(body), nil
}
