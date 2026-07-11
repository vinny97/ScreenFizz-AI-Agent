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

// SendApprovedResult reports the outcome of one ScreenFizz sending run.
type SendApprovedResult struct {
	Sent   int
	Failed int
}

type approvedEmailProspect struct {
	ID       string `json:"id"`
	Subject  string `json:"email_subject"`
	Body     string `json:"email_body"`
	Business struct {
		Email        string `json:"email"`
		BusinessName string `json:"business_name"`
	} `json:"screenfizz_businesses"`
}

// SendApprovedProspects sends no more than the remaining daily allowance of
// approved, unsent ScreenFizz emails. Failed emails deliberately remain
// approved so a later run can retry them.
func SendApprovedProspects(ctx context.Context, cfg Config) (SendApprovedResult, error) {
	client, err := newBrevoClient(cfg)
	if err != nil {
		return SendApprovedResult{}, err
	}
	if strings.TrimSpace(cfg.BrevoSenderEmail) == "" {
		return SendApprovedResult{}, errors.New("SCREENFIZZ_SENDER_EMAIL is required")
	}

	limit := cfg.DailySendLimit
	if limit <= 0 {
		limit = defaultDailySendLimit
	}
	sentToday, err := countScreenFizzEmailsSentToday(ctx, cfg, time.Now().UTC())
	if err != nil {
		return SendApprovedResult{}, err
	}
	remaining := limit - sentToday
	if remaining <= 0 {
		slog.Info("screenfizz.email.daily_limit_reached", "daily_limit", limit, "sent_today", sentToday)
		return SendApprovedResult{}, nil
	}

	prospects, err := nextApprovedEmailProspects(ctx, cfg, remaining)
	if err != nil {
		return SendApprovedResult{}, err
	}

	result := SendApprovedResult{}
	for _, prospect := range prospects {
		if err := validateApprovedEmailProspect(prospect); err != nil {
			result.Failed++
			slog.Error("screenfizz.email.send_failed", "prospect_id", prospect.ID, "error", err)
			continue
		}
		claimed, err := claimScreenFizzProspectForSending(ctx, cfg, prospect.ID)
		if err != nil {
			result.Failed++
			slog.Error("screenfizz.email.claim_failed", "prospect_id", prospect.ID, "error", err)
			continue
		}
		if !claimed {
			slog.Info("screenfizz.email.send_skipped", "prospect_id", prospect.ID, "reason", "already claimed or sent")
			continue
		}
		messageID, err := client.sendScreenFizzEmail(ctx, cfg, prospect)
		if err != nil {
			result.Failed++
			slog.Error("screenfizz.email.send_failed", "prospect_id", prospect.ID, "email", prospect.Business.Email, "error", err)
			if restoreErr := restoreScreenFizzProspectApproval(ctx, cfg, prospect.ID); restoreErr != nil {
				slog.Error("screenfizz.email.restore_approval_failed", "prospect_id", prospect.ID, "error", restoreErr)
			}
			continue
		}
		if err := markScreenFizzProspectSent(ctx, cfg, prospect.ID, messageID, time.Now().UTC()); err != nil {
			result.Failed++
			slog.Error("screenfizz.email.sent_record_failed", "prospect_id", prospect.ID, "error", err)
			continue
		}
		result.Sent++
		slog.Info("screenfizz.email.sent", "prospect_id", prospect.ID, "email", prospect.Business.Email, "brevo_message_id", messageID)
	}
	return result, nil
}

func validateApprovedEmailProspect(prospect approvedEmailProspect) error {
	switch {
	case strings.TrimSpace(prospect.Business.Email) == "":
		return errors.New("prospect has no email")
	case strings.TrimSpace(prospect.Subject) == "":
		return errors.New("prospect has no generated subject")
	case strings.TrimSpace(prospect.Body) == "":
		return errors.New("prospect has no generated email body")
	default:
		return nil
	}
}

func countScreenFizzEmailsSentToday(ctx context.Context, cfg Config, now time.Time) (int, error) {
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.AddDate(0, 0, 1)
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable)
	query := url.Values{
		"select":  {"id"},
		"sent_at": {"gte." + dayStart.Format(time.RFC3339), "lt." + dayEnd.Format(time.RFC3339)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return 0, fmt.Errorf("create ScreenFizz daily send count request: %w", err)
	}
	setSupabaseHeaders(req, cfg)
	req.Header.Set("Prefer", "count=exact")
	req.Header.Set("Range", "0-0")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, fmt.Errorf("count ScreenFizz emails sent today: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return 0, fmt.Errorf("read ScreenFizz daily send count response: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("count ScreenFizz emails sent today: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return exactContentRangeTotal(resp.Header.Get("Content-Range"))
}

func exactContentRangeTotal(value string) (int, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[1] == "" || parts[1] == "*" {
		return 0, fmt.Errorf("Supabase response is missing an exact count")
	}
	total, err := strconv.Atoi(parts[1])
	if err != nil || total < 0 {
		return 0, fmt.Errorf("invalid Supabase count %q", value)
	}
	return total, nil
}

func nextApprovedEmailProspects(ctx context.Context, cfg Config, limit int) ([]approvedEmailProspect, error) {
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable)
	query := url.Values{
		"select":  {"id,email_subject,email_body,screenfizz_businesses(email,business_name)"},
		"status":  {"eq.approved"},
		"sent_at": {"is.null"},
		"order":   {"created_at.asc"},
		"limit":   {strconv.Itoa(limit)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create approved ScreenFizz email request: %w", err)
	}
	setSupabaseHeaders(req, cfg)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("list approved ScreenFizz emails: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read approved ScreenFizz emails response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("list approved ScreenFizz emails: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var prospects []approvedEmailProspect
	if err := json.Unmarshal(body, &prospects); err != nil {
		return nil, fmt.Errorf("decode approved ScreenFizz emails response: %w", err)
	}
	return prospects, nil
}

func (c *brevoClient) sendScreenFizzEmail(ctx context.Context, cfg Config, prospect approvedEmailProspect) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"sender": map[string]string{
			"name":  strings.TrimSpace(cfg.BrevoSenderName),
			"email": strings.TrimSpace(cfg.BrevoSenderEmail),
		},
		"to":          []map[string]string{{"email": strings.TrimSpace(prospect.Business.Email)}},
		"subject":     prospect.Subject,
		"textContent": prospect.Body,
	})
	if err != nil {
		return "", fmt.Errorf("encode ScreenFizz Brevo email: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v3/smtp/email", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create ScreenFizz Brevo send request: %w", err)
	}
	req.Header.Set("api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send ScreenFizz email: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return "", fmt.Errorf("read ScreenFizz Brevo send response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("send ScreenFizz email: Brevo returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var response struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("decode ScreenFizz Brevo send response: %w", err)
	}
	return strings.TrimSpace(response.MessageID), nil
}

func claimScreenFizzProspectForSending(ctx context.Context, cfg Config, prospectID string) (bool, error) {
	updated, err := patchScreenFizzProspectStatus(ctx, cfg, prospectID, "approved", "sending", nil)
	if err != nil {
		return false, fmt.Errorf("claim ScreenFizz prospect for sending: %w", err)
	}
	return updated, nil
}

func restoreScreenFizzProspectApproval(ctx context.Context, cfg Config, prospectID string) error {
	_, err := patchScreenFizzProspectStatus(ctx, cfg, prospectID, "sending", "approved", nil)
	if err != nil {
		return fmt.Errorf("restore ScreenFizz prospect approval: %w", err)
	}
	return nil
}

func markScreenFizzProspectSent(ctx context.Context, cfg Config, prospectID, messageID string, sentAt time.Time) error {
	updated, err := patchScreenFizzProspectStatus(ctx, cfg, prospectID, "sending", "sent", map[string]any{
		"sent_at":          sentAt.Format(time.RFC3339),
		"brevo_message_id": messageID,
	})
	if err != nil {
		return fmt.Errorf("save ScreenFizz sent email: %w", err)
	}
	if !updated {
		return errors.New("ScreenFizz prospect is no longer claimed for sending")
	}
	return nil
}

func patchScreenFizzProspectStatus(ctx context.Context, cfg Config, prospectID, fromStatus, toStatus string, fields map[string]any) (bool, error) {
	values := map[string]any{
		"status": toStatus,
	}
	for key, value := range fields {
		values[key] = value
	}
	body, err := json.Marshal(values)
	if err != nil {
		return false, fmt.Errorf("encode ScreenFizz prospect status update: %w", err)
	}
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable)
	query := url.Values{"id": {"eq." + prospectID}, "status": {"eq." + fromStatus}, "sent_at": {"is.null"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint+"?"+query.Encode(), bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("create ScreenFizz prospect status update request: %w", err)
	}
	setSupabaseHeaders(req, cfg)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "return=representation")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return false, fmt.Errorf("save ScreenFizz prospect status: %w", err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return false, fmt.Errorf("read ScreenFizz prospect status response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return false, fmt.Errorf("save ScreenFizz prospect status: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}
	var updated []json.RawMessage
	if err := json.Unmarshal(responseBody, &updated); err != nil {
		return false, fmt.Errorf("decode ScreenFizz prospect status response: %w", err)
	}
	return len(updated) == 1, nil
}
