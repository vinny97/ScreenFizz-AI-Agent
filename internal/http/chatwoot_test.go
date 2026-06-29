package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestChatwootHealth(t *testing.T) {
	h := NewChatwootHandler("", "", "", false, "", "", "", nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/chatwoot/health", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(rec.Body.String(), "CHATWOOT_BASE_URL") {
		t.Fatalf("body does not report missing configuration: %s", rec.Body.String())
	}
}

func TestChatwootWebhookRepliesAndDeduplicates(t *testing.T) {
	var completions, replies atomic.Int32
	goclaw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		completions.Add(1)
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer goclaw-key" {
			t.Errorf("unexpected GoClaw request: %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "support-agent" {
			t.Errorf("model = %v", body["model"])
		}
		writeJSON(w, http.StatusOK, map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "AI reply"}}}})
	}))
	defer goclaw.Close()

	chatwoot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		replies.Add(1)
		if r.URL.Path != "/api/v1/accounts/7/conversations/42/messages" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("api_access_token") != "chatwoot-token" {
			t.Errorf("token = %q", r.Header.Get("api_access_token"))
		}
		payload, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(payload), `"message_type":"outgoing"`) || !strings.Contains(string(payload), `"content":"AI reply"`) {
			t.Errorf("unexpected reply body: %s", payload)
		}
		writeJSON(w, http.StatusOK, map[string]int{"id": 99})
	}))
	defer chatwoot.Close()

	h := NewChatwootHandler(chatwoot.URL, "chatwoot-token", "", false, goclaw.URL, "goclaw-key", "support-agent", nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	payload := `{"event":"message_created","id":123,"content":" Help me ","message_type":"incoming","private":false,"account":{"id":7},"conversation":{"id":42},"sender":{"type":"contact"}}`
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/chatwoot/webhook", strings.NewReader(payload)))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status = %d: %s", i, rec.Code, rec.Body.String())
		}
	}
	if completions.Load() != 1 || replies.Load() != 1 {
		t.Fatalf("completions=%d replies=%d, want 1 each", completions.Load(), replies.Load())
	}
}

func TestChatwootWebhookIgnoreFilters(t *testing.T) {
	h := NewChatwootHandler("https://chatwoot.example", "token", "", false, "https://goclaw.example", "key", "model", &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("ignored webhook made an upstream request")
			return nil, nil
		}),
	})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	tests := []string{
		`{"event":"conversation_updated","id":1,"content":"hello","message_type":"incoming"}`,
		`{"event":"message_created","id":2,"content":"hello","message_type":"outgoing"}`,
		`{"event":"message_created","id":3,"content":"hello","message_type":"incoming","private":true}`,
		`{"event":"message_created","id":4,"content":"  ","message_type":"incoming"}`,
		`{"event":"message_created","id":5,"content":"hello","message_type":0,"sender":{"type":"agent_bot"}}`,
	}
	for _, payload := range tests {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/chatwoot/webhook", strings.NewReader(payload)))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"ignored"`) {
			t.Errorf("payload %s: status=%d body=%s", payload, rec.Code, rec.Body.String())
		}
	}
}

func TestChatwootWebhookSignature(t *testing.T) {
	secret := "agentbot-webhook-secret"
	payload := []byte(`{"event":"conversation_updated","id":1,"content":"hello","message_type":"incoming"}`)
	h := NewChatwootHandler("https://chatwoot.example", "token", secret, true, "https://goclaw.example", "key", "model", nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	tests := []struct {
		name      string
		timestamp string
		signature string
		want      int
	}{
		{name: "valid", timestamp: "1770000000", signature: signChatwootTestPayload(secret, "1770000000", payload), want: http.StatusOK},
		{name: "invalid", timestamp: "1770000000", signature: "sha256=bad", want: http.StatusUnauthorized},
		{name: "missing timestamp", signature: signChatwootTestPayload(secret, "1770000000", payload), want: http.StatusUnauthorized},
		{name: "missing signature", timestamp: "1770000000", want: http.StatusUnauthorized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/chatwoot/webhook", strings.NewReader(string(payload)))
			if test.timestamp != "" {
				req.Header.Set("X-Chatwoot-Timestamp", test.timestamp)
			}
			if test.signature != "" {
				req.Header.Set("X-Chatwoot-Signature", test.signature)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, test.want, rec.Body.String())
			}
		})
	}
}

func TestChatwootWebhookMissingSignatureAllowedWhenNotStrict(t *testing.T) {
	h := NewChatwootHandler("https://chatwoot.example", "token", "secret", false, "https://goclaw.example", "key", "model", nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	payload := `{"event":"conversation_updated","id":1,"content":"hello","message_type":"incoming"}`
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/chatwoot/webhook", strings.NewReader(payload)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func signChatwootTestPayload(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	return fmt.Sprintf("sha256=%x", mac.Sum(nil))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }
