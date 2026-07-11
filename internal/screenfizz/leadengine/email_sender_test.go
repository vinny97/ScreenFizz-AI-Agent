package leadengine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendApprovedProspectsHonorsDailyLimitAndContinuesAfterFailure(t *testing.T) {
	var sendRecipients []string
	var sentUpdates []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/v1/screenfizz_prospects":
			switch r.Method {
			case http.MethodGet:
				if r.URL.Query().Get("select") == "id" {
					if r.URL.Query().Get("sent_at") != "gte."+"2026-07-11T00:00:00Z" {
						t.Fatalf("daily count sent_at filter = %q", r.URL.Query()["sent_at"])
					}
					w.Header().Set("Content-Range", "0-0/2")
					_, _ = w.Write([]byte(`[{"id":"already-sent"}]`))
					return
				}
				if r.URL.Query().Get("status") != "eq.approved" || r.URL.Query().Get("sent_at") != "is.null" || r.URL.Query().Get("limit") != "28" {
					t.Fatalf("approved email query = %s", r.URL.RawQuery)
				}
				_, _ = w.Write([]byte(`[
					{"id":"one","email_subject":"Hello One","email_body":"Body one","screenfizz_businesses":{"email":"one@example.com","business_name":"One Ltd"}},
					{"id":"two","email_subject":"Hello Two","email_body":"Body two","screenfizz_businesses":{"email":"two@example.com","business_name":"Two Ltd"}}
				]`))
			case http.MethodPatch:
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				var update map[string]any
				if err := json.Unmarshal(body, &update); err != nil {
					t.Fatal(err)
				}
				status, _ := update["status"].(string)
				queryStatus := r.URL.Query().Get("status")
				if (status == "sending" && queryStatus != "eq.approved") || (status == "sent" && queryStatus != "eq.sending") || (status == "approved" && queryStatus != "eq.sending") || r.URL.Query().Get("sent_at") != "is.null" {
					t.Fatalf("status update must be conditional: body=%s query=%s", body, r.URL.RawQuery)
				}
				sentUpdates = append(sentUpdates, update)
				_, _ = w.Write([]byte(`[{"id":"updated"}]`))
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		case "/v3/smtp/email":
			if r.Header.Get("api-key") != "brevo-key" {
				t.Fatalf("Brevo API key = %q", r.Header.Get("api-key"))
			}
			var payload struct {
				Sender  map[string]string   `json:"sender"`
				To      []map[string]string `json:"to"`
				Subject string              `json:"subject"`
				Text    string              `json:"textContent"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Sender["email"] != "hello@screenfizz.co.uk" || payload.Sender["name"] != "ScreenFizz" {
				t.Fatalf("unexpected sender: %#v", payload.Sender)
			}
			sendRecipients = append(sendRecipients, payload.To[0]["email"])
			if payload.To[0]["email"] == "two@example.com" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"code":"invalid_parameter"}`))
				return
			}
			_, _ = w.Write([]byte(`{"messageId":"brevo-one"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := SendApprovedProspects(context.Background(), Config{
		SupabaseURL:            server.URL,
		SupabaseServiceRoleKey: "service-key",
		ProspectsTable:         "screenfizz_prospects",
		BrevoAPIKey:            "brevo-key",
		BrevoAPIURL:            server.URL,
		BrevoSenderName:        "ScreenFizz",
		BrevoSenderEmail:       "hello@screenfizz.co.uk",
		DailySendLimit:         30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Sent != 1 || result.Failed != 1 {
		t.Fatalf("result = %#v, want 1 sent and 1 failed", result)
	}
	if got := strings.Join(sendRecipients, ","); got != "one@example.com,two@example.com" {
		t.Fatalf("sent recipients = %q", got)
	}
	if len(sentUpdates) != 4 || sentUpdates[0]["status"] != "sending" || sentUpdates[1]["status"] != "sent" || sentUpdates[1]["brevo_message_id"] != "brevo-one" || sentUpdates[1]["sent_at"] == "" || sentUpdates[2]["status"] != "sending" || sentUpdates[3]["status"] != "approved" {
		t.Fatalf("sent updates = %#v", sentUpdates)
	}
}

func TestSendApprovedProspectsStopsWhenDailyLimitReached(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/v1/screenfizz_prospects" || r.Method != http.MethodGet {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		w.Header().Set("Content-Range", "0-0/30")
		_, _ = w.Write([]byte(`[{"id":"already-sent"}]`))
	}))
	defer server.Close()

	result, err := SendApprovedProspects(context.Background(), Config{
		SupabaseURL:            server.URL,
		SupabaseServiceRoleKey: "service-key",
		ProspectsTable:         "screenfizz_prospects",
		BrevoAPIKey:            "brevo-key",
		BrevoAPIURL:            server.URL,
		BrevoSenderEmail:       "hello@screenfizz.co.uk",
		DailySendLimit:         30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != (SendApprovedResult{}) {
		t.Fatalf("result = %#v, want no sends", result)
	}
}
