package leadengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const leadInsertBatchSize = 500

// ImportResult summarizes a lead import.
type ImportResult struct {
	Imported int
	Skipped  int
}

type leadInsert struct {
	Campaign    string `json:"campaign"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Email       string `json:"email"`
	CompanyName string `json:"company"`
	CompanyURL  string `json:"website"`
	LinkedIn    string `json:"linkedin_url"`
	JobTitle    string `json:"job_title"`
	Industry    string `json:"industry"`
	CompanySize string `json:"company_size"`
	Country     string `json:"country"`
	Status      string `json:"status"`
}

// ImportLeads inserts new Apify dataset items into the Supabase leads table.
// Existing and repeated email addresses are skipped.
func (c *Client) ImportLeads(ctx context.Context, campaignName string, dataset json.RawMessage) (ImportResult, error) {
	if strings.TrimSpace(campaignName) == "" {
		return ImportResult{}, errors.New("campaign name is required")
	}
	var items []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(dataset))
	decoder.UseNumber()
	if err := decoder.Decode(&items); err != nil {
		return ImportResult{}, fmt.Errorf("decode Apify dataset: %w", err)
	}

	existing, err := c.listLeadEmails(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	suppressed, err := c.listSuppressedEmails(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	result := ImportResult{}
	newLeads := make([]leadInsert, 0, len(items))
	for _, item := range items {
		email := strings.TrimSpace(stringFieldAny(item, "email"))
		if email == "" {
			result.Skipped++
			continue
		}
		normalizedEmail := strings.ToLower(email)
		blocked := false
		for _, address := range splitLeadEmails(email) {
			if _, found := suppressed[strings.ToLower(address)]; found {
				blocked = true
				break
			}
		}
		if blocked {
			result.Skipped++
			continue
		}
		if _, found := existing[normalizedEmail]; found {
			result.Skipped++
			continue
		}
		existing[normalizedEmail] = struct{}{}
		newLeads = append(newLeads, leadInsert{
			Campaign:    campaignName,
			FirstName:   stringFieldAny(item, "first_name", "firstName"),
			LastName:    stringFieldAny(item, "last_name", "lastName"),
			Email:       email,
			CompanyName: stringFieldAny(item, "company_name", "organizationName"),
			CompanyURL:  stringFieldAny(item, "company_website", "organizationWebsite"),
			LinkedIn:    stringFieldAny(item, "linkedin", "linkedinUrl"),
			JobTitle:    stringFieldAny(item, "job_title", "position"),
			Industry:    stringFieldAny(item, "industry", "organizationIndustry"),
			CompanySize: stringFieldAny(item, "company_size", "organizationSize"),
			Country:     stringFieldAny(item, "country"),
			Status:      "NEW",
		})
	}

	for start := 0; start < len(newLeads); start += leadInsertBatchSize {
		end := min(start+leadInsertBatchSize, len(newLeads))
		if err := c.insertLeadBatch(ctx, newLeads[start:end]); err != nil {
			return ImportResult{}, err
		}
	}
	result.Imported = len(newLeads)
	return result, nil
}

func (c *Client) listSuppressedEmails(ctx context.Context) (map[string]struct{}, error) {
	result := make(map[string]struct{})
	var rows []struct {
		Email string `json:"email"`
	}
	if err := c.restJSON(ctx, http.MethodGet, "lead_email_suppressions", nil, nil, &rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		if email := strings.ToLower(strings.TrimSpace(row.Email)); email != "" {
			result[email] = struct{}{}
		}
	}
	return result, nil
}

func stringFieldAny(item map[string]any, keys ...string) string {
	for _, key := range keys {
		value, _ := item[key].(string)
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (c *Client) listLeadEmails(ctx context.Context) (map[string]struct{}, error) {
	emails := make(map[string]struct{})
	rowsRead := 0
	for start := 0; ; {
		requestURL := c.baseURL + "/rest/v1/leads?select=email"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create leads request: %w", err)
		}
		c.setSupabaseHeaders(req)
		req.Header.Set("Prefer", "count=exact")
		req.Header.Set("Range", fmt.Sprintf("%d-%d", start, start+pageSize-1))

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("list lead emails: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		closeErr := resp.Body.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return nil, fmt.Errorf("read lead emails: %w", err)
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			return nil, fmt.Errorf("list lead emails: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}
		var page []struct {
			Email string `json:"email"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("decode lead emails: %w", err)
		}
		for _, lead := range page {
			if email := strings.ToLower(strings.TrimSpace(lead.Email)); email != "" {
				emails[email] = struct{}{}
			}
		}
		rowsRead += len(page)
		total, hasTotal := contentRangeTotal(resp.Header.Get("Content-Range"))
		if len(page) == 0 || (hasTotal && rowsRead >= total) || (!hasTotal && len(page) < pageSize) {
			return emails, nil
		}
		start += len(page)
	}
}

func (c *Client) insertLeadBatch(ctx context.Context, leads []leadInsert) error {
	body, err := json.Marshal(leads)
	if err != nil {
		return fmt.Errorf("encode leads: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/rest/v1/leads", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create lead insert request: %w", err)
	}
	c.setSupabaseHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "return=minimal")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("insert leads: %w", err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return fmt.Errorf("read lead insert response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("insert leads: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func (c *Client) setSupabaseHeaders(req *http.Request) {
	req.Header.Set("apikey", c.serviceKey)
	req.Header.Set("Authorization", "Bearer "+c.serviceKey)
}
