package http

import (
	"encoding/json"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	screenfizz "github.com/nextlevelbuilder/goclaw/internal/screenfizz/leadengine"
)

func TestScreenFizzBrevoWebhookTracksClickAndAuditsPayload(t *testing.T) {
	var audit map[string]any
	var tracking map[string]any
	supabase := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		switch {
		case r.Method == stdhttp.MethodGet && r.URL.Path == "/rest/v1/screenfizz_prospects":
			if r.URL.Query().Get("brevo_message_id") != "eq.message-123" {
				t.Fatalf("message lookup query = %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`[{"id":"prospect-1"}]`))
		case r.Method == stdhttp.MethodPost && r.URL.Path == "/rest/v1/email_events":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(body, &audit); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(stdhttp.StatusCreated)
		case r.Method == stdhttp.MethodPatch && r.URL.Path == "/rest/v1/screenfizz_prospects":
			if r.URL.Query().Get("id") != "eq.prospect-1" {
				t.Fatalf("tracking query = %s", r.URL.RawQuery)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(body, &tracking); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(stdhttp.StatusNoContent)
		default:
			t.Fatalf("unexpected Supabase request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer supabase.Close()

	handler := NewScreenFizzBrevoWebhookHandler(func() (screenfizz.Config, error) {
		return screenfizz.Config{
			SupabaseURL:            supabase.URL,
			SupabaseServiceRoleKey: "service-key",
			ProspectsTable:         "screenfizz_prospects",
			BrevoWebhookSecret:     "webhook-secret",
		}, nil
	}, nil)
	mux := stdhttp.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(stdhttp.MethodPost, "/v1/webhooks/screenfizz/brevo", strings.NewReader(`{"event":"click","email":"owner@example.com","message-id":"message-123","ts_event":1783728000}`))
	req.Header.Set(screenFizzBrevoSecretHeader, "webhook-secret")
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)

	if res.Code != stdhttp.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	if audit["prospect_id"] != "prospect-1" || audit["event_type"] != "clicked" || audit["recipient_email"] != "owner@example.com" || audit["brevo_message_id"] != "message-123" {
		t.Fatalf("audit = %#v", audit)
	}
	if tracking["last_event"] != "clicked" || tracking["clicked_at"] != "2026-07-11T00:00:00Z" {
		t.Fatalf("tracking = %#v", tracking)
	}
}

func TestScreenFizzBrevoWebhookRejectsIncorrectSecret(t *testing.T) {
	handler := NewScreenFizzBrevoWebhookHandler(func() (screenfizz.Config, error) {
		return screenfizz.Config{BrevoWebhookSecret: "webhook-secret"}, nil
	}, nil)
	mux := stdhttp.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(stdhttp.MethodPost, "/v1/webhooks/screenfizz/brevo", strings.NewReader(`{"event":"delivered"}`))
	req.Header.Set(screenFizzBrevoSecretHeader, "wrong-secret")
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, stdhttp.StatusUnauthorized)
	}
}

func TestScreenFizzBrevoWebhookAuditsUnmatchedEvent(t *testing.T) {
	audited := false
	supabase := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		switch r.Method {
		case stdhttp.MethodGet:
			_, _ = w.Write([]byte(`[]`))
		case stdhttp.MethodPost:
			audited = true
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), "prospect_id") {
				t.Fatalf("unmatched event should not have a prospect id: %s", body)
			}
			w.WriteHeader(stdhttp.StatusCreated)
		default:
			t.Fatalf("unexpected request: %s", r.Method)
		}
	}))
	defer supabase.Close()
	handler := NewScreenFizzBrevoWebhookHandler(func() (screenfizz.Config, error) {
		return screenfizz.Config{SupabaseURL: supabase.URL, SupabaseServiceRoleKey: "service-key", ProspectsTable: "screenfizz_prospects", BrevoWebhookSecret: "webhook-secret"}, nil
	}, nil)
	mux := stdhttp.NewServeMux()
	handler.RegisterRoutes(mux)
	req := httptest.NewRequest(stdhttp.MethodPost, "/v1/webhooks/screenfizz/brevo", strings.NewReader(`{"event":"hard_bounce","email":"missing@example.com","message-id":"missing-id","ts_event":1783728000}`))
	req.Header.Set(screenFizzBrevoSecretHeader, "webhook-secret")
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != stdhttp.StatusOK || !audited {
		t.Fatalf("status = %d, audited = %t", res.Code, audited)
	}
}
