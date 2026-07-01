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
)

const (
	leadStatusNew          = "NEW"
	leadStatusReadyToEmail = "READY_TO_EMAIL"
	leadStatusRejected     = "REJECTED"
)

// QualificationResult summarizes a lead qualification run.
type QualificationResult struct {
	Qualified int
	Rejected  int
}

type qualificationLead struct {
	ID      json.RawMessage `json:"id"`
	Email   string          `json:"email"`
	Website string          `json:"website"`
}

// QualifyNewLeads evaluates every NEW lead and updates its status.
func (c *Client) QualifyNewLeads(ctx context.Context) (QualificationResult, error) {
	leads, err := c.listNewLeads(ctx)
	if err != nil {
		return QualificationResult{}, err
	}

	result := QualificationResult{}
	for _, lead := range leads {
		status := leadStatusReadyToEmail
		if shouldRejectLead(lead.Email, lead.Website) {
			status = leadStatusRejected
		}
		if err := c.updateLeadStatus(ctx, lead.ID, status); err != nil {
			return QualificationResult{}, err
		}
		if status == leadStatusReadyToEmail {
			result.Qualified++
		} else {
			result.Rejected++
		}
	}
	return result, nil
}

func shouldRejectLead(email, website string) bool {
	return strings.TrimSpace(email) == "" || strings.TrimSpace(website) == ""
}

func (c *Client) listNewLeads(ctx context.Context) ([]qualificationLead, error) {
	leads := make([]qualificationLead, 0)
	for start := 0; ; {
		query := url.Values{}
		query.Set("select", "id,email,website")
		query.Set("status", "eq."+leadStatusNew)
		requestURL := c.baseURL + "/rest/v1/leads?" + query.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create new leads request: %w", err)
		}
		c.setSupabaseHeaders(req)
		req.Header.Set("Prefer", "count=exact")
		req.Header.Set("Range", fmt.Sprintf("%d-%d", start, start+pageSize-1))

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("list new leads: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		closeErr := resp.Body.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return nil, fmt.Errorf("read new leads: %w", err)
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			return nil, fmt.Errorf("list new leads: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}

		var page []qualificationLead
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("decode new leads: %w", err)
		}
		leads = append(leads, page...)
		total, hasTotal := contentRangeTotal(resp.Header.Get("Content-Range"))
		if len(page) == 0 || (hasTotal && len(leads) >= total) || (!hasTotal && len(page) < pageSize) {
			return leads, nil
		}
		start += len(page)
	}
}

func (c *Client) updateLeadStatus(ctx context.Context, rawID json.RawMessage, status string) error {
	id, err := qualificationLeadID(rawID)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{"status": status})
	if err != nil {
		return fmt.Errorf("encode lead status: %w", err)
	}
	query := url.Values{}
	query.Set("id", "eq."+id)
	requestURL := c.baseURL + "/rest/v1/leads?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, requestURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create lead status request: %w", err)
	}
	c.setSupabaseHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "return=minimal")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("update lead status: %w", err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return fmt.Errorf("read lead status response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("update lead status: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func qualificationLeadID(rawID json.RawMessage) (string, error) {
	var stringID string
	if err := json.Unmarshal(rawID, &stringID); err == nil && stringID != "" {
		return stringID, nil
	}
	var numberID json.Number
	if err := json.Unmarshal(rawID, &numberID); err == nil && numberID.String() != "" {
		return numberID.String(), nil
	}
	return "", errors.New("NEW lead has no valid id")
}
