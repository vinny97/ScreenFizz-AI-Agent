package leadengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const searchAreasResponseLimit = 16 << 20

type searchArea struct {
	County string `json:"county"`
}

// LoadEnabledSearchAreas returns the counties enabled in the ScreenFizz-owned
// Supabase table.
func LoadEnabledSearchAreas(ctx context.Context, cfg Config) ([]string, error) {
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.SearchAreasTable)
	query := url.Values{
		"select":  {"county"},
		"enabled": {"eq.true"},
		"order":   {"county.asc"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create ScreenFizz search areas request: %w", err)
	}
	req.Header.Set("apikey", cfg.SupabaseServiceRoleKey)
	req.Header.Set("Authorization", "Bearer "+cfg.SupabaseServiceRoleKey)

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("list ScreenFizz search areas: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, searchAreasResponseLimit))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read ScreenFizz search areas response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("list ScreenFizz search areas: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var areas []searchArea
	if err := json.Unmarshal(body, &areas); err != nil {
		return nil, fmt.Errorf("decode ScreenFizz search areas response: %w", err)
	}
	counties := make([]string, 0, len(areas))
	for _, area := range areas {
		if county := strings.TrimSpace(area.County); county != "" {
			counties = append(counties, county)
		}
	}
	if len(counties) == 0 {
		return nil, errors.New("no enabled ScreenFizz search areas")
	}
	return counties, nil
}
