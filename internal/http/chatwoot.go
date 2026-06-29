package http

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	chatwootMaxBody  = 1 << 20
	chatwootDedupTTL = 24 * time.Hour
	chatwootDedupMax = 10_000
)

// ChatwootHandler adapts Chatwoot AgentBot webhooks to GoClaw's OpenAI-compatible API.
type ChatwootHandler struct {
	baseURL          string
	accessToken      string
	webhookSecret    string
	requireSignature bool
	goclawURL        string
	goclawKey        string
	model            string
	client           *http.Client

	mu   sync.Mutex
	seen map[string]time.Time
	now  func() time.Time
}

// NewChatwootHandlerFromEnv creates an adapter using the documented environment variables.
func NewChatwootHandlerFromEnv() *ChatwootHandler {
	return NewChatwootHandler(
		os.Getenv("CHATWOOT_BASE_URL"),
		os.Getenv("CHATWOOT_API_ACCESS_TOKEN"),
		os.Getenv("CHATWOOT_WEBHOOK_SECRET"),
		envBool("CHATWOOT_REQUIRE_WEBHOOK_SIGNATURE"),
		os.Getenv("GOCLAW_BASE_URL"),
		os.Getenv("GOCLAW_API_KEY"),
		os.Getenv("GOCLAW_MODEL"),
		nil,
	)
}

// NewChatwootHandler creates an adapter. A nil client gets a bounded default client.
func NewChatwootHandler(chatwootURL, accessToken, webhookSecret string, requireSignature bool, goclawURL, goclawKey, model string, client *http.Client) *ChatwootHandler {
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	return &ChatwootHandler{
		baseURL:          strings.TrimRight(strings.TrimSpace(chatwootURL), "/"),
		accessToken:      strings.TrimSpace(accessToken),
		webhookSecret:    strings.TrimSpace(webhookSecret),
		requireSignature: requireSignature,
		goclawURL:        strings.TrimRight(strings.TrimSpace(goclawURL), "/"),
		goclawKey:        strings.TrimSpace(goclawKey),
		model:            strings.TrimSpace(model),
		client:           client,
		seen:             make(map[string]time.Time),
		now:              time.Now,
	}
}

func (h *ChatwootHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /chatwoot/health", h.handleHealth)
	mux.HandleFunc("POST /chatwoot/webhook", h.handleWebhook)
}

type chatwootWebhook struct {
	Event       string          `json:"event"`
	ID          json.RawMessage `json:"id"`
	Content     string          `json:"content"`
	MessageType json.RawMessage `json:"message_type"`
	Private     bool            `json:"private"`
	Account struct {
		ID int64 `json:"id"`
	} `json:"account"`
	AccountID    int64 `json:"account_id"`
	Conversation struct {
		ID int64 `json:"id"`
	} `json:"conversation"`
	ConversationID int64  `json:"conversation_id"`
	SenderType     string `json:"sender_type"`
	Sender         struct {
		Type string `json:"type"`
	} `json:"sender"`
}

type goclawCompletion struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (h *ChatwootHandler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	missing := h.missingConfig()
	status := http.StatusOK
	if len(missing) > 0 {
		status = http.StatusServiceUnavailable
	}
	state := "ok"
	if len(missing) > 0 {
		state = "unconfigured"
	}
	writeJSON(w, status, map[string]any{"status": state, "missing": missing})
}

func (h *ChatwootHandler) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if missing := h.missingConfig(); len(missing) > 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "chatwoot adapter is not configured", "missing": missing})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, chatwootMaxBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid webhook payload"})
		return
	}
	if !h.verifyWebhookSignature(r, body) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid webhook signature"})
		return
	}
	var event chatwootWebhook
	if err := json.Unmarshal(body, &event); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid webhook payload"})
		return
	}
	messageID := rawID(event.ID)
	if reason := ignoreReason(event, messageID); reason != "" {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ignored", "reason": reason})
		return
	}
	if !h.claim(messageID) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ignored", "reason": "duplicate"})
		return
	}

	accountID := event.Account.ID
	if accountID == 0 {
		accountID = event.AccountID
	}
	conversationID := event.Conversation.ID
	if conversationID == 0 {
		conversationID = event.ConversationID
	}
	if accountID <= 0 || conversationID <= 0 {
		h.release(messageID)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account and conversation IDs are required"})
		return
	}

	answer, err := h.complete(r.Context(), strings.TrimSpace(event.Content), accountID, conversationID)
	if err == nil {
		err = h.reply(r.Context(), accountID, conversationID, answer)
	}
	if err != nil {
		h.release(messageID) // let Chatwoot retry transient upstream failures
		slog.Error("chatwoot.webhook_failed", "message_id", messageID, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "upstream request failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "replied"})
}

func (h *ChatwootHandler) verifyWebhookSignature(r *http.Request, body []byte) bool {
	signature := strings.TrimSpace(r.Header.Get("X-Chatwoot-Signature"))
	if signature == "" {
		if h.requireSignature {
			slog.Warn("security.chatwoot.signature_missing", "remote_addr", r.RemoteAddr, "strict", true)
			return false
		}
		slog.Warn("security.chatwoot.signature_missing", "remote_addr", r.RemoteAddr, "strict", false)
		return true
	}

	timestamp := strings.TrimSpace(r.Header.Get("X-Chatwoot-Timestamp"))
	if h.webhookSecret == "" || timestamp == "" {
		slog.Warn("security.chatwoot.signature_unverifiable", "remote_addr", r.RemoteAddr, "has_secret", h.webhookSecret != "", "has_timestamp", timestamp != "")
		return false
	}

	mac := hmac.New(sha256.New, []byte(h.webhookSecret))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	expected := "sha256=" + fmt.Sprintf("%x", mac.Sum(nil))
	valid := hmac.Equal([]byte(expected), []byte(signature))
	if !valid {
		slog.Warn("security.chatwoot.signature_invalid", "remote_addr", r.RemoteAddr)
	}
	return valid
}

func ignoreReason(e chatwootWebhook, messageID string) string {
	if e.Event != "message_created" {
		return "event"
	}
	if !isIncoming(e.MessageType) {
		return "not_incoming"
	}
	if e.Private {
		return "private"
	}
	if strings.TrimSpace(e.Content) == "" {
		return "empty"
	}
	sender := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(chatwootFirstNonEmpty(e.SenderType, e.Sender.Type)))
	if strings.Contains(sender, "agentbot") || strings.Contains(sender, "captain::assistant") {
		return "bot"
	}
	if messageID == "" {
		return "missing_id"
	}
	return ""
}

func isIncoming(raw json.RawMessage) bool {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.EqualFold(s, "incoming")
	}
	var n int
	return json.Unmarshal(raw, &n) == nil && n == 0
}

func rawID(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

func (h *ChatwootHandler) complete(ctx context.Context, content string, accountID, conversationID int64) (string, error) {
	body := map[string]any{
		"model":  h.model,
		"stream": false,
		"user":   fmt.Sprintf("chatwoot:%d:%d", accountID, conversationID),
		"messages": []map[string]string{
			{"role": "user", "content": content},
		},
	}
	var out goclawCompletion
	if err := h.doJSON(ctx, http.MethodPost, h.goclawURL+"/v1/chat/completions", "Bearer "+h.goclawKey, body, &out); err != nil {
		return "", fmt.Errorf("goclaw completion: %w", err)
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", errors.New("goclaw completion returned no content")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

func (h *ChatwootHandler) reply(ctx context.Context, accountID, conversationID int64, content string) error {
	endpoint := h.baseURL + "/api/v1/accounts/" + strconv.FormatInt(accountID, 10) + "/conversations/" + strconv.FormatInt(conversationID, 10) + "/messages"
	body := map[string]any{"content": content, "message_type": "outgoing", "private": false, "content_type": "text"}
	return h.doJSON(ctx, http.MethodPost, endpoint, h.accessToken, body, nil)
}

func (h *ChatwootHandler) doJSON(ctx context.Context, method, endpoint, auth string, input, output any) error {
	if _, err := url.ParseRequestURI(endpoint); err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.HasPrefix(auth, "Bearer ") {
		req.Header.Set("Authorization", auth)
	} else {
		req.Header.Set("api_access_token", auth)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(limited)))
	}
	if output != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, chatwootMaxBody)).Decode(output); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func (h *ChatwootHandler) missingConfig() []string {
	values := []struct {
		name  string
		value string
	}{
		{"CHATWOOT_BASE_URL", h.baseURL},
		{"CHATWOOT_API_ACCESS_TOKEN", h.accessToken},
		{"GOCLAW_BASE_URL", h.goclawURL},
		{"GOCLAW_API_KEY", h.goclawKey},
		{"GOCLAW_MODEL", h.model},
	}
	if h.requireSignature && h.webhookSecret == "" {
		values = append(values, struct {
			name  string
			value string
		}{"CHATWOOT_WEBHOOK_SECRET", h.webhookSecret})
	}
	missing := make([]string, 0)
	for _, item := range values {
		if item.value == "" {
			missing = append(missing, item.name)
		}
	}
	return missing
}

func envBool(name string) bool {
	value, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(name)))
	return err == nil && value
}

func (h *ChatwootHandler) claim(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	oldestID := ""
	oldestAt := now
	for key, seenAt := range h.seen {
		if now.Sub(seenAt) >= chatwootDedupTTL {
			delete(h.seen, key)
			continue
		}
		if seenAt.Before(oldestAt) {
			oldestID, oldestAt = key, seenAt
		}
	}
	if _, ok := h.seen[id]; ok {
		return false
	}
	if len(h.seen) >= chatwootDedupMax && oldestID != "" {
		delete(h.seen, oldestID)
	}
	h.seen[id] = now
	return true
}

func (h *ChatwootHandler) release(id string) {
	h.mu.Lock()
	delete(h.seen, id)
	h.mu.Unlock()
}

func chatwootFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
