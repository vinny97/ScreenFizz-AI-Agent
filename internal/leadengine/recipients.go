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

type emailRecipient struct {
	ID             string     `json:"id"`
	LeadID         string     `json:"lead_id"`
	CampaignID     string     `json:"campaign_id"`
	Email          string     `json:"email"`
	ReplyToken     string     `json:"reply_token"`
	Status         string     `json:"status"`
	AttemptCount   int        `json:"attempt_count"`
	NextRetryAt    *time.Time `json:"next_retry_at"`
	BrevoMessageID string     `json:"brevo_message_id"`
}

func (c *Client) ensureLeadRecipients(ctx context.Context, campaignID string, lead emailReadyLead) ([]emailRecipient, error) {
	leadID, err := qualificationLeadID(lead.ID)
	if err != nil {
		return nil, err
	}
	existing, err := c.listRecipients(ctx, "lead_id", leadID)
	if err != nil {
		return nil, err
	}
	byEmail := make(map[string]emailRecipient, len(existing))
	for _, recipient := range existing {
		byEmail[strings.ToLower(strings.TrimSpace(recipient.Email))] = recipient
	}
	for _, address := range splitLeadEmails(lead.Email) {
		key := strings.ToLower(address)
		if _, found := byEmail[key]; found {
			continue
		}
		payload := map[string]any{"lead_id": leadID, "campaign_id": campaignID, "email": address, "status": "PENDING"}
		var created []emailRecipient
		if err := c.restJSON(ctx, http.MethodPost, "lead_email_recipients", nil, payload, &created); err != nil {
			return nil, err
		}
		if len(created) > 0 {
			byEmail[key] = created[0]
		}
	}
	result := make([]emailRecipient, 0, len(byEmail))
	for _, address := range splitLeadEmails(lead.Email) {
		if recipient, found := byEmail[strings.ToLower(address)]; found {
			result = append(result, recipient)
		}
	}
	return result, nil
}

func (c *Client) listRecipients(ctx context.Context, field, value string) ([]emailRecipient, error) {
	query := url.Values{"select": {"*"}, field: {"eq." + value}}
	var recipients []emailRecipient
	if err := c.restJSON(ctx, http.MethodGet, "lead_email_recipients", query, nil, &recipients); err != nil {
		return nil, err
	}
	return recipients, nil
}

func (c *Client) recipientSuppressed(ctx context.Context, email string) (bool, error) {
	query := url.Values{"select": {"id"}, "email": {"ilike." + strings.TrimSpace(email)}, "limit": {"1"}}
	var rows []map[string]any
	if err := c.restJSON(ctx, http.MethodGet, "lead_email_suppressions", query, nil, &rows); err != nil {
		return false, err
	}
	return len(rows) > 0, nil
}

func (c *Client) updateRecipient(ctx context.Context, id string, fields map[string]any) error {
	fields["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	query := url.Values{"id": {"eq." + id}}
	return c.restJSON(ctx, http.MethodPatch, "lead_email_recipients", query, fields, nil)
}

func (c *Client) countSentRecipientsSince(ctx context.Context, campaignID string, since time.Time) (int, error) {
	query := url.Values{
		"select":      {"id"},
		"campaign_id": {"eq." + campaignID},
		"sent_at":     {"gte." + since.UTC().Format(time.RFC3339)},
	}
	var rows []struct {
		ID string `json:"id"`
	}
	if err := c.restJSON(ctx, http.MethodGet, "lead_email_recipients", query, nil, &rows); err != nil {
		return 0, err
	}
	return len(rows), nil
}

func (c *Client) restJSON(ctx context.Context, method, table string, query url.Values, payload any, target any) error {
	requestURL := c.baseURL + "/rest/v1/" + table
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.setSupabaseHeaders(req)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if target != nil {
		req.Header.Set("Prefer", "return=representation")
	} else {
		req.Header.Set("Prefer", "return=minimal")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: Supabase returned %s: %s", method, table, resp.Status, strings.TrimSpace(string(responseBody)))
	}
	if target != nil && len(responseBody) > 0 {
		return json.Unmarshal(responseBody, target)
	}
	return nil
}
