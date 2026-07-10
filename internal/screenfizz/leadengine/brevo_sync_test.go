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

func TestSyncBrevoContactsUpsertsAndRecordsContactID(t *testing.T) {
	brevo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v3/contacts/lists":
			if r.Header.Get("api-key") != "brevo-key" {
				t.Fatalf("api-key = %q", r.Header.Get("api-key"))
			}
			_, _ = w.Write([]byte(`{"lists":[{"id":42,"name":"ScreenFizz Leads"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v3/contacts":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			var request map[string]any
			if err := json.Unmarshal(body, &request); err != nil {
				t.Fatal(err)
			}
			if request["email"] != "hello@example.com" || request["updateEnabled"] != true || request["getId"] != true {
				t.Fatalf("unexpected Brevo contact request: %#v", request)
			}
			attributes, ok := request["attributes"].(map[string]any)
			if !ok || attributes["BUSINESS_NAME"] != "Example Business" || attributes["PERSONALISATION_LINE"] != "A personal observation" || attributes["RECOMMENDED_USE_CASE"] != "Digital menu boards" {
				t.Fatalf("unexpected Brevo attributes: %#v", request["attributes"])
			}
			_, _ = w.Write([]byte(`{"id":12345}`))
		default:
			t.Fatalf("unexpected Brevo request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer brevo.Close()

	getCalls := 0
	updated := false
	supabase := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls++
			if r.URL.Query().Get("brevo_contact_id") != "is.null" || r.URL.Query().Get("screenfizz_businesses.email") != "not.is.null" {
				t.Fatalf("unexpected Brevo sync filters: %s", r.URL.RawQuery)
			}
			if getCalls == 1 {
				_, _ = w.Write([]byte(`[{"id":"prospect-1","personalisation_line":"A personal observation","recommended_use_case":"Digital menu boards","screenfizz_businesses":{"email":"hello@example.com","business_name":"Example Business","category":"restaurant","town":"Marlow","website":"example.com"}}]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case http.MethodPatch:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), `"brevo_contact_id":12345`) || !strings.Contains(string(body), `"brevo_synced_at"`) {
				t.Fatalf("unexpected Brevo sync update: %s", body)
			}
			updated = true
		default:
			t.Fatalf("unexpected Supabase request: %s", r.Method)
		}
	}))
	defer supabase.Close()

	err := SyncBrevoContacts(context.Background(), Config{
		SupabaseURL:            supabase.URL,
		SupabaseServiceRoleKey: "service-key",
		ProspectsTable:         "screenfizz_prospects",
		BrevoAPIKey:            "brevo-key",
		BrevoAPIURL:            brevo.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Fatal("expected Brevo contact ID to be saved")
	}
}
