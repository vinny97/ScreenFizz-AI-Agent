package http

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/url"
	"strings"
	"time"

	screenfizz "github.com/nextlevelbuilder/goclaw/internal/screenfizz/leadengine"
)

const (
	screenFizzBrevoWebhookPath  = "POST /v1/webhooks/screenfizz/brevo"
	screenFizzBrevoSecretHeader = "X-ScreenFizz-Webhook-Secret"
	screenFizzBrevoMaxBodyBytes = 1 << 20
)

// ScreenFizzBrevoWebhookHandler records transactional email events received
// from the ScreenFizz Brevo account. It is deliberately independent from the
// internal dashboard authentication because Brevo calls it directly.
type ScreenFizzBrevoWebhookHandler struct {
	config func() (screenfizz.Config, error)
	client *stdhttp.Client
}

func NewScreenFizzBrevoWebhookHandlerFromEnv() *ScreenFizzBrevoWebhookHandler {
	return NewScreenFizzBrevoWebhookHandler(screenfizz.ConfigFromEnv, nil)
}

func NewScreenFizzBrevoWebhookHandler(config func() (screenfizz.Config, error), client *stdhttp.Client) *ScreenFizzBrevoWebhookHandler {
	if client == nil {
		client = &stdhttp.Client{Timeout: 30 * time.Second}
	}
	return &ScreenFizzBrevoWebhookHandler{config: config, client: client}
}

func (h *ScreenFizzBrevoWebhookHandler) RegisterRoutes(mux *stdhttp.ServeMux) {
	mux.HandleFunc(screenFizzBrevoWebhookPath, h.handleWebhook)
}

type screenFizzBrevoEvent struct {
	Event     string `json:"event"`
	Email     string `json:"email"`
	MessageID string `json:"message-id"`
	TSEvent   int64  `json:"ts_event"`
}

func (h *ScreenFizzBrevoWebhookHandler) handleWebhook(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	cfg, err := h.config()
	if err != nil {
		writeJSON(w, stdhttp.StatusServiceUnavailable, map[string]string{"error": "ScreenFizz email tracking is not configured"})
		return
	}
	if !validScreenFizzWebhookSecret(r.Header.Get(screenFizzBrevoSecretHeader), cfg.BrevoWebhookSecret) {
		writeJSON(w, stdhttp.StatusUnauthorized, map[string]string{"error": "invalid webhook secret"})
		return
	}

	r.Body = stdhttp.MaxBytesReader(w, r.Body, screenFizzBrevoMaxBodyBytes)
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, stdhttp.StatusBadRequest, map[string]string{"error": "invalid webhook payload"})
		return
	}
	var event screenFizzBrevoEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		writeJSON(w, stdhttp.StatusBadRequest, map[string]string{"error": "invalid webhook payload"})
		return
	}
	event.Event = normalizeScreenFizzEmailEvent(event.Event)
	if event.Event == "" {
		writeJSON(w, stdhttp.StatusBadRequest, map[string]string{"error": "webhook event is required"})
		return
	}

	occurredAt := screenFizzEventTime(event.TSEvent)
	prospectID, err := h.findProspectByMessageID(r.Context(), cfg, event.MessageID)
	if err != nil {
		slog.Error("screenfizz.email_event.prospect_lookup_failed", "brevo_message_id", event.MessageID, "error", err)
		writeJSON(w, stdhttp.StatusBadGateway, map[string]string{"error": "could not process webhook event"})
		return
	}
	if err := h.storeEvent(r.Context(), cfg, prospectID, event, occurredAt, payload); err != nil {
		slog.Error("screenfizz.email_event.audit_store_failed", "brevo_message_id", event.MessageID, "error", err)
		writeJSON(w, stdhttp.StatusBadGateway, map[string]string{"error": "could not record webhook event"})
		return
	}
	if prospectID != "" {
		if err := h.updateProspectTracking(r.Context(), cfg, prospectID, event.Event, occurredAt); err != nil {
			slog.Error("screenfizz.email_event.prospect_update_failed", "prospect_id", prospectID, "event", event.Event, "error", err)
			writeJSON(w, stdhttp.StatusBadGateway, map[string]string{"error": "could not update prospect tracking"})
			return
		}
	}
	slog.Info("screenfizz.email_event.recorded", "event", event.Event, "prospect_id", prospectID, "brevo_message_id", event.MessageID)
	writeJSON(w, stdhttp.StatusOK, map[string]string{"status": "recorded"})
}

func validScreenFizzWebhookSecret(provided, expected string) bool {
	provided = strings.TrimSpace(provided)
	expected = strings.TrimSpace(expected)
	if provided == "" || expected == "" || len(provided) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func normalizeScreenFizzEmailEvent(value string) string {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), "-", "_"), " ", "_"))
	switch normalized {
	case "delivered":
		return "delivered"
	case "opened", "open", "first_opening", "unique_opened", "uniqueopened":
		return "opened"
	case "click", "clicked":
		return "clicked"
	case "hard_bounce", "hardbounce", "soft_bounce", "softbounce", "bounced":
		return "bounced"
	case "unsubscribed", "unsubscribe":
		return "unsubscribed"
	default:
		return normalized
	}
}

func screenFizzEventTime(ts int64) time.Time {
	if ts > 0 {
		return time.Unix(ts, 0).UTC()
	}
	return time.Now().UTC()
}

func (h *ScreenFizzBrevoWebhookHandler) findProspectByMessageID(ctx context.Context, cfg screenfizz.Config, messageID string) (string, error) {
	if strings.TrimSpace(messageID) == "" {
		return "", nil
	}
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable)
	query := url.Values{"select": {"id"}, "brevo_message_id": {"eq." + messageID}, "limit": {"1"}}
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("create ScreenFizz event prospect lookup: %w", err)
	}
	setScreenFizzSupabaseHeaders(req, cfg)
	resp, err := h.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("look up ScreenFizz event prospect: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return "", fmt.Errorf("read ScreenFizz event prospect lookup: %w", err)
	}
	if resp.StatusCode < stdhttp.StatusOK || resp.StatusCode >= stdhttp.StatusMultipleChoices {
		return "", fmt.Errorf("look up ScreenFizz event prospect: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var prospects []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &prospects); err != nil {
		return "", fmt.Errorf("decode ScreenFizz event prospect lookup: %w", err)
	}
	if len(prospects) == 0 {
		return "", nil
	}
	return prospects[0].ID, nil
}

func (h *ScreenFizzBrevoWebhookHandler) storeEvent(ctx context.Context, cfg screenfizz.Config, prospectID string, event screenFizzBrevoEvent, occurredAt time.Time, payload []byte) error {
	var rawPayload any
	if err := json.Unmarshal(payload, &rawPayload); err != nil {
		return fmt.Errorf("decode ScreenFizz event audit payload: %w", err)
	}
	values := map[string]any{
		"event_type":       event.Event,
		"recipient_email":  strings.TrimSpace(event.Email),
		"brevo_message_id": strings.TrimSpace(event.MessageID),
		"occurred_at":      occurredAt.Format(time.RFC3339),
		"payload":          rawPayload,
	}
	if prospectID != "" {
		values["prospect_id"] = prospectID
	}
	body, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("encode ScreenFizz event audit row: %w", err)
	}
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/email_events"
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ScreenFizz event audit request: %w", err)
	}
	setScreenFizzSupabaseHeaders(req, cfg)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "return=minimal")
	return executeScreenFizzSupabaseRequest(h.client, req, "store ScreenFizz email event")
}

func (h *ScreenFizzBrevoWebhookHandler) updateProspectTracking(ctx context.Context, cfg screenfizz.Config, prospectID, eventName string, occurredAt time.Time) error {
	values := map[string]any{"last_event": eventName}
	switch eventName {
	case "delivered":
		values["delivered_at"] = occurredAt.Format(time.RFC3339)
	case "opened":
		values["opened_at"] = occurredAt.Format(time.RFC3339)
	case "clicked":
		values["clicked_at"] = occurredAt.Format(time.RFC3339)
	case "bounced":
		values["bounced_at"] = occurredAt.Format(time.RFC3339)
	case "unsubscribed":
		values["unsubscribed_at"] = occurredAt.Format(time.RFC3339)
	}
	body, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("encode ScreenFizz event tracking update: %w", err)
	}
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable) + "?id=eq." + url.QueryEscape(prospectID)
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodPatch, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ScreenFizz event tracking update: %w", err)
	}
	setScreenFizzSupabaseHeaders(req, cfg)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "return=minimal")
	return executeScreenFizzSupabaseRequest(h.client, req, "update ScreenFizz prospect tracking")
}

func setScreenFizzSupabaseHeaders(req *stdhttp.Request, cfg screenfizz.Config) {
	req.Header.Set("apikey", cfg.SupabaseServiceRoleKey)
	req.Header.Set("Authorization", "Bearer "+cfg.SupabaseServiceRoleKey)
}

func executeScreenFizzSupabaseRequest(client *stdhttp.Client, req *stdhttp.Request, operation string) error {
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return fmt.Errorf("read %s response: %w", operation, err)
	}
	if resp.StatusCode < stdhttp.StatusOK || resp.StatusCode >= stdhttp.StatusMultipleChoices {
		return fmt.Errorf("%s: Supabase returned %s: %s", operation, resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}
