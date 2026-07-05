package leadengine

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type BrevoWebhookHandler struct {
	client *Client
	secret string
}

func NewBrevoWebhookHandler(client *Client, secret string) (*BrevoWebhookHandler, error) {
	if client == nil {
		return nil, fmt.Errorf("Supabase client is required")
	}
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil, fmt.Errorf("BREVO_WEBHOOK_SECRET is required")
	}
	return &BrevoWebhookHandler{client: client, secret: secret}, nil
}

func (h *BrevoWebhookHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/leadengine/brevo/events/{token}", h.handleEvent)
	mux.HandleFunc("POST /v1/leadengine/brevo/inbound/{token}", h.handleInbound)
}

func (h *BrevoWebhookHandler) authorized(r *http.Request) bool {
	token := r.PathValue("token")
	return len(token) == len(h.secret) && subtle.ConstantTimeCompare([]byte(token), []byte(h.secret)) == 1
}

func (h *BrevoWebhookHandler) handleEvent(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var event struct {
		Event     string `json:"event"`
		Email     string `json:"email"`
		MessageID string `json:"message-id"`
		Reason    string `json:"reason"`
		Timestamp int64  `json:"ts_event"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || json.Unmarshal(body, &event) != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := h.client.ProcessBrevoEvent(r.Context(), event.Event, event.Email, event.MessageID, event.Reason, event.Timestamp, body); err != nil {
		http.Error(w, err.Error(), http.StatusTooManyRequests)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *BrevoWebhookHandler) handleInbound(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var payload struct {
		Items []struct {
			From struct {
				Address string `json:"Address"`
			} `json:"From"`
			SentAtDate string `json:"SentAtDate"`
		} `json:"items"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil || json.Unmarshal(body, &payload) != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	for _, item := range payload.Items {
		if err := h.client.MarkEmailReplied(r.Context(), item.From.Address, body); err != nil {
			http.Error(w, err.Error(), http.StatusTooManyRequests)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *Client) ProcessBrevoEvent(ctx context.Context, event, email, messageID, reason string, timestamp int64, raw json.RawMessage) error {
	recipient, err := c.findRecipient(ctx, email, messageID)
	if err != nil || recipient == nil {
		return err
	}
	normalized := strings.ToLower(strings.TrimSpace(event))
	switch normalized {
	case "hardbounce":
		normalized = "hard_bounce"
	case "softbounce":
		normalized = "soft_bounce"
	}
	status := map[string]string{
		"delivered": "DELIVERED", "deferred": "DEFERRED", "soft_bounce": "SOFT_BOUNCED",
		"hard_bounce": "HARD_BOUNCED", "invalid": "INVALID", "blocked": "BLOCKED",
		"unsubscribed": "UNSUBSCRIBED", "spam": "BLOCKED", "error": "FAILED_RETRYABLE",
	}[normalized]
	if status == "" {
		status = recipient.Status
	}
	occurred := time.Now().UTC()
	if timestamp > 0 {
		occurred = time.Unix(timestamp, 0).UTC()
	}
	fields := map[string]any{"status": status}
	switch status {
	case "DELIVERED":
		fields["delivered_at"] = occurred.Format(time.RFC3339)
	case "DEFERRED":
		fields["deferred_at"] = occurred.Format(time.RFC3339)
	case "SOFT_BOUNCED":
		fields["soft_bounced_at"] = occurred.Format(time.RFC3339)
		fields["next_retry_at"] = occurred.Add(24 * time.Hour).Format(time.RFC3339)
	case "HARD_BOUNCED", "INVALID", "BLOCKED", "UNSUBSCRIBED":
		fields["hard_bounced_at"] = occurred.Format(time.RFC3339)
	}
	if err := c.updateRecipient(ctx, recipient.ID, fields); err != nil {
		return err
	}
	if status == "SOFT_BOUNCED" || status == "FAILED_RETRYABLE" {
		if err := c.restJSON(ctx, http.MethodPatch, "leads", url.Values{"id": {"eq." + recipient.LeadID}}, map[string]any{"status": leadStatusEmailReady}, nil); err != nil {
			return err
		}
	}
	if err := c.restJSON(ctx, http.MethodPost, "lead_email_events", nil, map[string]any{
		"recipient_id": recipient.ID, "lead_id": recipient.LeadID, "email": email,
		"event_type": normalized, "brevo_message_id": messageID, "reason": reason,
		"occurred_at": occurred.Format(time.RFC3339), "payload": json.RawMessage(raw),
	}, nil); err != nil {
		return err
	}
	if status == "HARD_BOUNCED" || status == "INVALID" || status == "BLOCKED" || status == "UNSUBSCRIBED" {
		if err := c.suppressEmail(ctx, email, status, recipient); err != nil {
			return err
		}
	}
	return c.pauseCampaignForBounceRate(ctx, recipient.CampaignID)
}

func (c *Client) findRecipient(ctx context.Context, email, messageID string) (*emailRecipient, error) {
	query := url.Values{"select": {"*"}, "limit": {"1"}, "order": {"sent_at.desc.nullslast"}}
	if strings.TrimSpace(messageID) != "" {
		query.Set("brevo_message_id", "eq."+messageID)
	}
	var recipients []emailRecipient
	if err := c.restJSON(ctx, http.MethodGet, "lead_email_recipients", query, nil, &recipients); err != nil {
		return nil, err
	}
	if len(recipients) == 0 && strings.TrimSpace(email) != "" {
		query.Del("brevo_message_id")
		query.Set("email", "ilike."+strings.TrimSpace(email))
		if err := c.restJSON(ctx, http.MethodGet, "lead_email_recipients", query, nil, &recipients); err != nil {
			return nil, err
		}
	}
	if len(recipients) == 0 {
		return nil, nil
	}
	return &recipients[0], nil
}

func (c *Client) suppressEmail(ctx context.Context, email, reason string, recipient *emailRecipient) error {
	suppressed, err := c.recipientSuppressed(ctx, email)
	if err != nil || suppressed {
		return err
	}
	payload := map[string]any{"email": strings.ToLower(strings.TrimSpace(email)), "reason": reason}
	if recipient != nil {
		payload["source_lead_id"] = recipient.LeadID
		payload["source_recipient_id"] = recipient.ID
	}
	return c.restJSON(ctx, http.MethodPost, "lead_email_suppressions", nil, payload, nil)
}

func (c *Client) MarkEmailReplied(ctx context.Context, email string, raw json.RawMessage) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil
	}
	query := url.Values{"select": {"*"}, "email": {"ilike." + email}}
	var recipients []emailRecipient
	if err := c.restJSON(ctx, http.MethodGet, "lead_email_recipients", query, nil, &recipients); err != nil {
		return err
	}
	for _, recipient := range recipients {
		now := time.Now().UTC().Format(time.RFC3339)
		if err := c.updateRecipient(ctx, recipient.ID, map[string]any{"status": "REPLIED", "replied_at": now}); err != nil {
			return err
		}
		if err := c.restJSON(ctx, http.MethodPatch, "leads", url.Values{"id": {"eq." + recipient.LeadID}}, map[string]any{"status": "REPLIED", "replied_at": now}, nil); err != nil {
			return err
		}
		if err := c.suppressEmail(ctx, email, "REPLIED", &recipient); err != nil {
			return err
		}
		if err := c.restJSON(ctx, http.MethodPost, "lead_email_events", nil, map[string]any{"recipient_id": recipient.ID, "lead_id": recipient.LeadID, "email": email, "event_type": "replied", "payload": json.RawMessage(raw)}, nil); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) pauseCampaignForBounceRate(ctx context.Context, campaignID string) error {
	if strings.TrimSpace(campaignID) == "" {
		return nil
	}
	var campaigns []Campaign
	if err := c.restJSON(ctx, http.MethodGet, "campaigns", url.Values{"select": {"bounce_pause_threshold,bounce_min_sample"}, "id": {"eq." + campaignID}}, nil, &campaigns); err != nil || len(campaigns) == 0 {
		return err
	}
	threshold := floatValue(campaigns[0]["bounce_pause_threshold"])
	minimum := intValue(campaigns[0]["bounce_min_sample"])
	if threshold <= 0 {
		threshold = 0.03
	}
	if minimum <= 0 {
		minimum = 20
	}
	query := url.Values{"select": {"status"}, "campaign_id": {"eq." + campaignID}, "status": {"in.(DELIVERED,SOFT_BOUNCED,HARD_BOUNCED,INVALID,BLOCKED)"}}
	var outcomes []struct {
		Status string `json:"status"`
	}
	if err := c.restJSON(ctx, http.MethodGet, "lead_email_recipients", query, nil, &outcomes); err != nil {
		return err
	}
	if len(outcomes) < minimum {
		return nil
	}
	bounces := 0
	for _, outcome := range outcomes {
		if outcome.Status != "DELIVERED" {
			bounces++
		}
	}
	rate := float64(bounces) / float64(len(outcomes))
	if rate <= threshold {
		return nil
	}
	reason := fmt.Sprintf("bounce rate %.2f%% exceeded %.2f%%", rate*100, threshold*100)
	return c.restJSON(ctx, http.MethodPatch, "campaigns", url.Values{"id": {"eq." + campaignID}}, map[string]any{"paused_at": time.Now().UTC().Format(time.RFC3339), "pause_reason": reason}, nil)
}
