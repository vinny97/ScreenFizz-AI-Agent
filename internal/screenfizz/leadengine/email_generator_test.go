package leadengine

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

const validGeneratedEmailJSON = `{"subject":"A quick idea for Example Restaurant","email_body":"Hi Example Restaurant,\n\nI noticed you regularly promote seasonal offers on your website. A digital display near your entrance could automatically showcase those promotions. ScreenFizz could make those updates quick and visible to every visitor.\n\nWould you like a free mock-up?"}`

func TestDecodeGeneratedEmailEnforcesWordLimit(t *testing.T) {
	email, err := decodeGeneratedEmail(validGeneratedEmailJSON)
	if err != nil {
		t.Fatal(err)
	}
	if email.Subject == "" || len(strings.Fields(email.Body)) > 150 {
		t.Fatalf("unexpected generated email: %#v", email)
	}
	tooLong := `{"subject":"Subject","email_body":"` + strings.Repeat("word ", 151) + `"}`
	if _, err := decodeGeneratedEmail(tooLong); err == nil {
		t.Fatal("expected an email body above 150 words to be rejected")
	}
}

func TestDecodeGeneratedEmailRemovesEmDashes(t *testing.T) {
	email, err := decodeGeneratedEmail(`{"subject":"Subject","email_body":"Hi there — a polite, professional note."}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(email.Body, "—") || email.Body != "Hi there, a polite, professional note." {
		t.Fatalf("email body = %q", email.Body)
	}
}

func TestGenerateProspectEmailsSavesDraftWithoutSending(t *testing.T) {
	ai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "Example Restaurant") || !strings.Contains(string(body), "free mock-up") {
			t.Fatalf("email prompt missing expected data: %s", body)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":` + strconv.Quote(validGeneratedEmailJSON) + `}}]}`))
	}))
	defer ai.Close()

	getCalls := 0
	updated := false
	supabase := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if r.URL.Path == "/v3/contacts/lists" {
				_, _ = w.Write([]byte(`{"lists":[{"id":1,"name":"ScreenFizz Leads"}]}`))
				return
			}
			getCalls++
			if r.URL.Query().Get("brevo_contact_id") == "is.null" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			if r.URL.Query().Get("analysed") != "eq.true" || r.URL.Query().Get("email_generated") != "eq.false" {
				t.Fatalf("unexpected email generation filters: %s", r.URL.RawQuery)
			}
			if getCalls == 1 {
				_, _ = w.Write([]byte(`[{"id":"prospect-1","business_summary":"A local restaurant","business_type":"restaurant","recommended_use_case":"Digital menu boards","personalisation_line":"I noticed you regularly promote seasonal offers on your website. A digital display near your entrance could automatically showcase those promotions.","screenfizz_businesses":{"business_name":"Example Restaurant","category":"restaurant"}}]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case http.MethodPatch:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), `"email_subject":"A quick idea for Example Restaurant"`) || !strings.Contains(string(body), `"email_generated":true`) || !strings.Contains(string(body), `"status":"ready_to_send"`) || strings.Contains(string(body), "brevo") {
				t.Fatalf("unexpected generated email update: %s", body)
			}
			updated = true
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer supabase.Close()

	err := GenerateProspectEmails(context.Background(), Config{
		SupabaseURL:            supabase.URL,
		SupabaseServiceRoleKey: "service-key",
		ProspectsTable:         "screenfizz_prospects",
		AIAPIKey:               "ai-key",
		AIAPIURL:               ai.URL,
		AIModel:                "test-model",
		BrevoAPIKey:            "brevo-key",
		BrevoAPIURL:            supabase.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Fatal("expected generated email to be stored")
	}
}
