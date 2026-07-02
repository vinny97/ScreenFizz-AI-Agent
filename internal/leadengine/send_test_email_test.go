package leadengine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendTestEmail(t *testing.T) {
	t.Parallel()

	var sentPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/v1/campaigns":
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Range", "0-0/1")
			fmt.Fprint(w, `[{
				"name":"Campaign A",
				"active":true,
				"sender_name":"Vinny",
				"sender_email":"vinny@example.com"
			}]`)
		case "/rest/v1/leads":
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if got := r.URL.Query().Get("status"); got != "eq."+leadStatusEmailReady {
				t.Errorf("status filter = %q", got)
			}
			if got := r.URL.Query().Get("order"); got != "created_at.asc" {
				t.Errorf("order = %q", got)
			}
			if got := r.Header.Get("Range"); got != "0-0" {
				t.Errorf("range = %q", got)
			}
			w.Header().Set("Content-Range", "0-0/1")
			fmt.Fprint(w, `[{
				"id":"1",
				"email":"ada@example.com",
				"email_subject":"Hello Ada",
				"email_body_html":"<p>Hi Ada</p>",
				"email_body_text":"Hi Ada"
			}]`)
		case "/v3/smtp/email":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if got := r.Header.Get("api-key"); got != "brevo-key" {
				t.Errorf("api-key = %q", got)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read Brevo body: %v", err)
				return
			}
			if err := json.Unmarshal(body, &sentPayload); err != nil {
				t.Errorf("decode Brevo body: %v", err)
				return
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"messageId":"123"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := New(server.URL, "service-key")
	if err != nil {
		t.Fatal(err)
	}
	sender, err := NewTestSender("brevo-key")
	if err != nil {
		t.Fatal(err)
	}
	sender.baseURL = server.URL

	if err := client.SendTestEmail(context.Background(), sender, "myemail@example.com"); err != nil {
		t.Fatal(err)
	}

	if got := sentPayload["subject"]; got != "Hello Ada" {
		t.Fatalf("subject = %#v", got)
	}
	if got := sentPayload["htmlContent"]; got != "<p>Hi Ada</p>" {
		t.Fatalf("htmlContent = %#v", got)
	}
	if got := sentPayload["textContent"]; got != "Hi Ada" {
		t.Fatalf("textContent = %#v", got)
	}
	toList, ok := sentPayload["to"].([]any)
	if !ok || len(toList) != 1 {
		t.Fatalf("to = %#v", sentPayload["to"])
	}
	toEntry, ok := toList[0].(map[string]any)
	if !ok || toEntry["email"] != "myemail@example.com" {
		t.Fatalf("to[0] = %#v", toList[0])
	}
	senderMap, ok := sentPayload["sender"].(map[string]any)
	if !ok {
		t.Fatalf("sender = %#v", sentPayload["sender"])
	}
	if senderMap["name"] != "Vinny" || senderMap["email"] != "vinny@example.com" {
		t.Fatalf("sender = %#v", senderMap)
	}
}

func TestSendReturnsFullBrevoError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/smtp/email" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"message":"invalid sender"}`)
	}))
	defer server.Close()

	sender, err := NewTestSender("brevo-key")
	if err != nil {
		t.Fatal(err)
	}
	sender.baseURL = server.URL

	_, err = sender.Send(context.Background(), &activeSenderCampaign{
		SenderName:  "Vinny",
		SenderEmail: "vinny@example.com",
	}, &emailReadyLead{
		ID:       json.RawMessage(`"1"`),
		Email:    "lead@example.com",
		Subject:  "Hello",
		HTMLBody: "<p>Body</p>",
		TextBody: "Body",
	}, "myemail@example.com")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != `Brevo returned 400 Bad Request: {"message":"invalid sender"}` {
		t.Fatalf("error = %q", got)
	}
}

func TestSendReadyLeadsContinuesAfterFailures(t *testing.T) {
	t.Parallel()

	type sentUpdate struct {
		Status         string
		SentAt         string
		BrevoMessageID string
	}

	updated := make(map[string]sentUpdate)
	var recipients []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/v1/campaigns":
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Range", "0-0/1")
			fmt.Fprint(w, `[{
				"name":"Campaign A",
				"active":true,
				"sender_name":"Vinny",
				"sender_email":"vinny@example.com"
			}]`)
		case "/rest/v1/leads":
			switch r.Method {
			case http.MethodGet:
				if got := r.URL.Query().Get("status"); got != "eq."+leadStatusEmailReady {
					t.Errorf("status filter = %q", got)
				}
				if got := r.URL.Query().Get("order"); got != "created_at.asc" {
					t.Errorf("order = %q", got)
				}
				if got := r.Header.Get("Range"); got != "0-99" {
					t.Errorf("range = %q", got)
				}
				w.Header().Set("Content-Range", "0-2/3")
				fmt.Fprint(w, `[
					{"id":"1","email":"first@example.com, alternate@example.com","email_subject":"First","email_body_html":"<p>One</p>","email_body_text":"One"},
					{"id":"2","email":"second@example.com","email_subject":"Second","email_body_html":"<p>Two</p>","email_body_text":"Two"},
					{"id":"3","email":"third@example.com","email_subject":"Third","email_body_html":"<p>Three</p>","email_body_text":"Three"}
				]`)
			case http.MethodPatch:
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read sent update: %v", err)
					return
				}
				var patch map[string]string
				if err := json.Unmarshal(body, &patch); err != nil {
					t.Errorf("decode sent update: %v", err)
					return
				}
				updated[r.URL.Query().Get("id")] = sentUpdate{
					Status:         patch["status"],
					SentAt:         patch["sent_at"],
					BrevoMessageID: patch["brevo_message_id"],
				}
				w.WriteHeader(http.StatusNoContent)
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		case "/v3/smtp/email":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read Brevo body: %v", err)
				return
			}
			var payload struct {
				To []struct {
					Email string `json:"email"`
				} `json:"to"`
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("decode Brevo body: %v", err)
				return
			}
			if len(payload.To) != 1 {
				t.Fatalf("to = %#v", payload.To)
			}
			recipients = append(recipients, payload.To[0].Email)
			if payload.To[0].Email == "second@example.com" {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"message":"bounce"}`)
				return
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"messageId":"msg-%s"}`, payload.To[0].Email)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := New(server.URL, "service-key")
	if err != nil {
		t.Fatal(err)
	}
	sender, err := NewTestSender("brevo-key")
	if err != nil {
		t.Fatal(err)
	}
	sender.baseURL = server.URL

	result, err := client.SendReadyLeads(context.Background(), sender)
	if err != nil {
		t.Fatal(err)
	}
	if result.Sent != 2 || result.Failed != 1 {
		t.Fatalf("result = %+v", result)
	}
	if strings.Join(recipients, ",") != "first@example.com,alternate@example.com,second@example.com,third@example.com" {
		t.Fatalf("recipients = %#v", recipients)
	}
	if _, found := updated["eq.2"]; found {
		t.Fatalf("failed lead should not be updated: %#v", updated)
	}
	for _, id := range []string{"eq.1", "eq.3"} {
		got, found := updated[id]
		if !found {
			t.Fatalf("missing update for %s: %#v", id, updated)
		}
		if got.Status != leadStatusSent {
			t.Fatalf("status for %s = %q", id, got.Status)
		}
		if got.SentAt == "" {
			t.Fatalf("sent_at for %s is empty", id)
		}
		if got.BrevoMessageID == "" {
			t.Fatalf("brevo_message_id for %s is empty", id)
		}
	}
	if got := updated["eq.1"].BrevoMessageID; got != "msg-first@example.com,msg-alternate@example.com" {
		t.Fatalf("brevo_message_id for multi-address lead = %q", got)
	}
}
