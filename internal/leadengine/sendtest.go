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
	"os"
	"strings"
	"time"
)

type emailReadyLead struct {
	ID       json.RawMessage `json:"id"`
	Email    string          `json:"email"`
	Subject  string          `json:"email_subject"`
	HTMLBody string          `json:"email_body_html"`
	TextBody string          `json:"email_body_text"`
}

type activeSenderCampaign struct {
	SenderName  string
	SenderEmail string
}

type TestSender struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

type SendResult struct {
	Sent   int
	Failed int
}

const leadStatusSent = "SENT"

// NewTestSender creates a Brevo client for sending a single test email.
func NewTestSender(apiKey string) (*TestSender, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("BREVO_API_KEY is required")
	}
	return &TestSender{
		apiKey:     apiKey,
		baseURL:    "https://api.brevo.com",
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// NewTestSenderFromEnv creates a Brevo client from BREVO_API_KEY.
func NewTestSenderFromEnv() (*TestSender, error) {
	return NewTestSender(os.Getenv("BREVO_API_KEY"))
}

// SendTestEmail sends the first EMAIL_READY lead to the supplied test address.
func (c *Client) SendTestEmail(ctx context.Context, sender *TestSender, to string) error {
	if sender == nil {
		return errors.New("Brevo sender is required")
	}
	to = strings.TrimSpace(to)
	if to == "" {
		return errors.New("--to is required")
	}

	lead, err := c.getFirstEmailReadyLead(ctx)
	if err != nil {
		return err
	}
	campaign, err := c.getActiveSenderCampaign(ctx)
	if err != nil {
		return err
	}

	_, err = sender.Send(ctx, campaign, lead, to)
	return err
}

func (c *Client) getFirstEmailReadyLead(ctx context.Context) (*emailReadyLead, error) {
	query := url.Values{}
	query.Set("select", "id,email,email_subject,email_body_html,email_body_text")
	query.Set("status", "eq."+leadStatusEmailReady)
	query.Set("order", "created_at.asc")
	requestURL := c.baseURL + "/rest/v1/leads?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create EMAIL_READY leads request: %w", err)
	}
	c.setSupabaseHeaders(req)
	req.Header.Set("Prefer", "count=exact")
	req.Header.Set("Range", "0-0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list EMAIL_READY leads: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read EMAIL_READY leads: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("list EMAIL_READY leads: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var leads []emailReadyLead
	if err := json.Unmarshal(body, &leads); err != nil {
		return nil, fmt.Errorf("decode EMAIL_READY leads: %w", err)
	}
	if len(leads) == 0 {
		return nil, errors.New("no EMAIL_READY leads found")
	}
	return &leads[0], nil
}

func (c *Client) SendReadyLeads(ctx context.Context, sender *TestSender) (SendResult, error) {
	if sender == nil {
		return SendResult{}, errors.New("Brevo sender is required")
	}
	campaign, err := c.getActiveSenderCampaign(ctx)
	if err != nil {
		return SendResult{}, err
	}
	leads, err := c.listEmailReadyLeads(ctx)
	if err != nil {
		return SendResult{}, err
	}

	result := SendResult{}
	for _, lead := range leads {
		recipients := splitLeadEmails(lead.Email)
		if len(recipients) == 0 {
			result.Failed++
			slog.Error("leadengine.send.failed", "reason", "lead has no email", "lead_id", string(lead.ID))
			continue
		}
		messageIDs, sendErr := sender.sendLeadEmails(ctx, campaign, &lead, recipients)
		if sendErr != nil {
			result.Failed++
			slog.Error("leadengine.send.failed", "error", sendErr, "lead_id", string(lead.ID), "email", lead.Email)
			continue
		}
		if err := c.markLeadSent(ctx, lead.ID, strings.Join(messageIDs, ",")); err != nil {
			return SendResult{}, err
		}
		result.Sent++
	}
	return result, nil
}

func splitLeadEmails(value string) []string {
	parts := strings.Split(value, ",")
	recipients := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		email := strings.TrimSpace(part)
		if email == "" {
			continue
		}
		key := strings.ToLower(email)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		recipients = append(recipients, email)
	}
	return recipients
}

func (s *TestSender) sendLeadEmails(ctx context.Context, campaign *activeSenderCampaign, lead *emailReadyLead, recipients []string) ([]string, error) {
	messageIDs := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		messageID, err := s.Send(ctx, campaign, lead, recipient)
		if err != nil {
			return messageIDs, fmt.Errorf("send to %s: %w", recipient, err)
		}
		messageIDs = append(messageIDs, messageID)
	}
	return messageIDs, nil
}

func (c *Client) listEmailReadyLeads(ctx context.Context) ([]emailReadyLead, error) {
	query := url.Values{}
	query.Set("select", "id,email,email_subject,email_body_html,email_body_text")
	query.Set("status", "eq."+leadStatusEmailReady)
	query.Set("order", "created_at.asc")
	requestURL := c.baseURL + "/rest/v1/leads?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create EMAIL_READY leads request: %w", err)
	}
	c.setSupabaseHeaders(req)
	req.Header.Set("Prefer", "count=exact")
	req.Header.Set("Range", "0-99")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list EMAIL_READY leads: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read EMAIL_READY leads: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("list EMAIL_READY leads: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var leads []emailReadyLead
	if err := json.Unmarshal(body, &leads); err != nil {
		return nil, fmt.Errorf("decode EMAIL_READY leads: %w", err)
	}
	return leads, nil
}

func (c *Client) getActiveSenderCampaign(ctx context.Context) (*activeSenderCampaign, error) {
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

	senderName, ok := active["sender_name"].(string)
	if !ok {
		return nil, errors.New("active campaign has no sender_name")
	}
	senderEmail, ok := active["sender_email"].(string)
	if !ok {
		return nil, errors.New("active campaign has no sender_email")
	}
	if strings.TrimSpace(senderEmail) == "" {
		return nil, errors.New("active campaign has no sender_email")
	}
	return &activeSenderCampaign{
		SenderName:  senderName,
		SenderEmail: senderEmail,
	}, nil
}

func (c *Client) markLeadSent(ctx context.Context, rawID json.RawMessage, messageID string) error {
	id, err := qualificationLeadID(rawID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]string{
		"status":           leadStatusSent,
		"sent_at":          time.Now().UTC().Format(time.RFC3339),
		"brevo_message_id": messageID,
	})
	if err != nil {
		return fmt.Errorf("encode lead sent update: %w", err)
	}
	query := url.Values{}
	query.Set("id", "eq."+id)
	requestURL := c.baseURL + "/rest/v1/leads?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, requestURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create lead sent update request: %w", err)
	}
	c.setSupabaseHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "return=minimal")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("update lead sent status: %w", err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return fmt.Errorf("read lead sent response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("update lead sent status: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func (s *TestSender) Send(ctx context.Context, campaign *activeSenderCampaign, lead *emailReadyLead, to string) (string, error) {
	if campaign == nil {
		return "", errors.New("sender campaign is required")
	}
	if lead == nil {
		return "", errors.New("EMAIL_READY lead is required")
	}

	payload, err := json.Marshal(map[string]any{
		"sender": map[string]string{
			"name":  campaign.SenderName,
			"email": campaign.SenderEmail,
		},
		"to": []map[string]string{{
			"email": strings.TrimSpace(to),
		}},
		"subject":     lead.Subject,
		"htmlContent": lead.HTMLBody,
		"textContent": lead.TextBody,
	})
	if err != nil {
		return "", fmt.Errorf("encode Brevo email: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/v3/smtp/email", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create Brevo request: %w", err)
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("api-key", s.apiKey)
	req.Header.Set("content-type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send email: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return "", fmt.Errorf("read Brevo response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Brevo returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var response struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("decode Brevo response: %w", err)
	}
	return response.MessageID, nil
}
