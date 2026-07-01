// Package leadengine provides access to the Supabase-backed lead engine data.
package leadengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const pageSize = 1000

// Campaign is schema-agnostic so the client can return every column in the
// existing campaigns table without duplicating its Supabase schema here.
type Campaign map[string]any

// Client reads lead engine data from Supabase.
type Client struct {
	baseURL    string
	serviceKey string
	httpClient *http.Client
}

// New creates a Supabase lead engine client.
func New(baseURL, serviceKey string) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	serviceKey = strings.TrimSpace(serviceKey)
	if baseURL == "" {
		return nil, errors.New("SUPABASE_URL is required")
	}
	if serviceKey == "" {
		return nil, errors.New("SUPABASE_SERVICE_ROLE_KEY is required")
	}

	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid SUPABASE_URL %q", baseURL)
	}

	return &Client{
		baseURL:    baseURL,
		serviceKey: serviceKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// NewFromEnv creates a client from SUPABASE_URL and
// SUPABASE_SERVICE_ROLE_KEY.
func NewFromEnv() (*Client, error) {
	return New(os.Getenv("SUPABASE_URL"), os.Getenv("SUPABASE_SERVICE_ROLE_KEY"))
}

// ListCampaigns returns all rows and columns from the campaigns table.
func (c *Client) ListCampaigns(ctx context.Context) ([]Campaign, error) {
	campaigns := make([]Campaign, 0)

	for start := 0; ; {
		requestURL := c.baseURL + "/rest/v1/campaigns?select=*"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create campaigns request: %w", err)
		}
		c.setSupabaseHeaders(req)
		req.Header.Set("Prefer", "count=exact")
		req.Header.Set("Range", fmt.Sprintf("%d-%d", start, start+pageSize-1))

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("list campaigns: %w", err)
		}

		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		closeErr := resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read campaigns response: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close campaigns response: %w", closeErr)
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			return nil, fmt.Errorf("list campaigns: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}

		var page []Campaign
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.UseNumber()
		if err := decoder.Decode(&page); err != nil {
			return nil, fmt.Errorf("decode campaigns response: %w", err)
		}
		campaigns = append(campaigns, page...)

		total, hasTotal := contentRangeTotal(resp.Header.Get("Content-Range"))
		if len(page) == 0 || (hasTotal && len(campaigns) >= total) || (!hasTotal && len(page) < pageSize) {
			return campaigns, nil
		}
		start += len(page)
	}
}

func contentRangeTotal(value string) (int, bool) {
	_, rawTotal, ok := strings.Cut(value, "/")
	if !ok || rawTotal == "*" {
		return 0, false
	}
	total, err := strconv.Atoi(rawTotal)
	return total, err == nil
}
