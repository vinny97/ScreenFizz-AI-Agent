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
	"strconv"
	"strings"
	"time"
)

const (
	brevoLeadsListName = "ScreenFizz Leads"
	brevoSyncBatchSize = 100
)

type brevoProspect struct {
	ID                  string `json:"id"`
	PersonalisationLine string `json:"personalisation_line"`
	RecommendedUseCase  string `json:"recommended_use_case"`
	Business            struct {
		Email        string `json:"email"`
		BusinessName string `json:"business_name"`
		Category     string `json:"category"`
		Town         string `json:"town"`
		Website      string `json:"website"`
	} `json:"screenfizz_businesses"`
}

type brevoClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func newBrevoClient(cfg Config) (*brevoClient, error) {
	if strings.TrimSpace(cfg.BrevoAPIKey) == "" {
		return nil, errors.New("SCREENFIZZ_BREVO_API_KEY is required")
	}
	if strings.TrimSpace(cfg.BrevoAPIURL) == "" {
		return nil, errors.New("SCREENFIZZ_BREVO_API_URL is required")
	}
	return &brevoClient{
		apiKey:     cfg.BrevoAPIKey,
		baseURL:    strings.TrimRight(cfg.BrevoAPIURL, "/"),
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// SyncBrevoContacts upserts pending ScreenFizz prospects in Brevo and assigns
// them to the ScreenFizz Leads list. It does not send email.
func SyncBrevoContacts(ctx context.Context, cfg Config) error {
	client, err := newBrevoClient(cfg)
	if err != nil {
		return err
	}
	listID, err := client.listIDByName(ctx, brevoLeadsListName)
	if err != nil {
		return err
	}
	for {
		prospects, err := nextBrevoProspects(ctx, cfg)
		if err != nil {
			return err
		}
		if len(prospects) == 0 {
			return nil
		}
		failed := 0
		for _, prospect := range prospects {
			email := strings.TrimSpace(prospect.Business.Email)
			if email == "" {
				slog.Warn("Skipping Brevo sync for prospect without email", "prospect_id", prospect.ID)
				failed++
				continue
			}
			contactID, err := client.upsertContact(ctx, prospect, listID)
			if err != nil {
				slog.Error("Failed to sync Brevo contact", "prospect_id", prospect.ID, "email", email, "error", err)
				failed++
				continue
			}
			if err := saveBrevoContactSync(ctx, cfg, prospect.ID, contactID); err != nil {
				slog.Error("Failed to save Brevo contact sync", "prospect_id", prospect.ID, "error", err)
				failed++
			}
		}
		if failed > 0 {
			return fmt.Errorf("failed to sync %d ScreenFizz prospects to Brevo", failed)
		}
	}
}

func nextBrevoProspects(ctx context.Context, cfg Config) ([]brevoProspect, error) {
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable)
	query := url.Values{
		"select":                      {"id,personalisation_line,recommended_use_case,screenfizz_businesses!inner(email,business_name,category,town,website)"},
		"brevo_contact_id":            {"is.null"},
		"screenfizz_businesses.email": {"not.is.null"},
		"order":                       {"created_at.asc"},
		"limit":                       {fmt.Sprintf("%d", brevoSyncBatchSize)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create ScreenFizz Brevo prospect request: %w", err)
	}
	setSupabaseHeaders(req, cfg)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("list Brevo-pending ScreenFizz prospects: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read Brevo-pending ScreenFizz prospects response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("list Brevo-pending ScreenFizz prospects: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var prospects []brevoProspect
	if err := json.Unmarshal(body, &prospects); err != nil {
		return nil, fmt.Errorf("decode Brevo-pending ScreenFizz prospects response: %w", err)
	}
	return prospects, nil
}

func (c *brevoClient) listIDByName(ctx context.Context, name string) (int64, error) {
	requestURL := c.baseURL + "/v3/contacts/lists?limit=500&offset=0"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return 0, fmt.Errorf("create Brevo list request: %w", err)
	}
	req.Header.Set("api-key", c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("list Brevo contact lists: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return 0, fmt.Errorf("read Brevo contact lists response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return 0, fmt.Errorf("list Brevo contact lists: Brevo returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var response struct {
		Lists []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"lists"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return 0, fmt.Errorf("decode Brevo contact lists response: %w", err)
	}
	for _, list := range response.Lists {
		if strings.EqualFold(strings.TrimSpace(list.Name), name) {
			return list.ID, nil
		}
	}
	return 0, fmt.Errorf("Brevo list %q was not found", name)
}

func (c *brevoClient) upsertContact(ctx context.Context, prospect brevoProspect, listID int64) (int64, error) {
	body, err := json.Marshal(map[string]any{
		"email": prospect.Business.Email,
		"attributes": map[string]string{
			"BUSINESS_NAME":        prospect.Business.BusinessName,
			"CATEGORY":             prospect.Business.Category,
			"TOWN":                 prospect.Business.Town,
			"WEBSITE":              prospect.Business.Website,
			"PERSONALISATION_LINE": prospect.PersonalisationLine,
			"RECOMMENDED_USE_CASE": prospect.RecommendedUseCase,
		},
		"listIds":       []int64{listID},
		"updateEnabled": true,
		"getId":         true,
	})
	if err != nil {
		return 0, fmt.Errorf("encode Brevo contact: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v3/contacts", bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create Brevo contact request: %w", err)
	}
	req.Header.Set("api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("upsert Brevo contact: %w", err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return 0, fmt.Errorf("read Brevo contact response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return 0, fmt.Errorf("upsert Brevo contact: Brevo returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}
	var response struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return 0, fmt.Errorf("decode Brevo contact response: %w", err)
	}
	contactID, err := strconv.ParseInt(strings.Trim(string(response.ID), `"`), 10, 64)
	if err != nil || contactID == 0 {
		return 0, errors.New("Brevo contact response has no valid ID")
	}
	return contactID, nil
}

func saveBrevoContactSync(ctx context.Context, cfg Config, prospectID string, contactID int64) error {
	body, err := json.Marshal(map[string]any{
		"brevo_contact_id": contactID,
		"brevo_synced_at":  time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("encode Brevo contact sync: %w", err)
	}
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable) + "?id=eq." + url.QueryEscape(prospectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ScreenFizz Brevo sync update request: %w", err)
	}
	setSupabaseHeaders(req, cfg)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "return=minimal")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("save ScreenFizz Brevo sync: %w", err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return fmt.Errorf("read ScreenFizz Brevo sync response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("save ScreenFizz Brevo sync: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}
	return nil
}
