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

const validBusinessAnalysisJSON = `{"business_summary":"A restaurant","business_type":"restaurant","recommended_use_case":"Digital menu boards","personalisation_line":"I noticed you regularly promote seasonal offers on your website. A digital display near your entrance could automatically showcase those promotions."}`

func TestDecodeBusinessAnalysisRequiresExactSchema(t *testing.T) {
	analysis, err := decodeBusinessAnalysis(validBusinessAnalysisJSON)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.BusinessType != "restaurant" || analysis.RecommendedUseCase != "Digital menu boards" || !strings.Contains(analysis.PersonalisationLine, "seasonal offers") {
		t.Fatalf("unexpected analysis: %#v", analysis)
	}
	if _, err := decodeBusinessAnalysis(`{"business_summary":"","business_type":"","recommended_use_case":"","personalisation_line":"","extra":true}`); err == nil {
		t.Fatal("expected extra field to be rejected")
	}
}

func TestAnalyseProspectsSavesAIAnalysis(t *testing.T) {
	ai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "Example Restaurant") || !strings.Contains(string(body), "Seasonal menu") {
			t.Fatalf("analysis input missing expected data: %s", body)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":` + strconv.Quote(validBusinessAnalysisJSON) + `}}]}`))
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
			if r.URL.Query().Get("email_generated") == "eq.false" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			if r.URL.Query().Get("parsed") != "eq.true" || r.URL.Query().Get("analysed") != "eq.false" {
				t.Fatalf("unexpected analysis filters: %s", r.URL.RawQuery)
			}
			if getCalls == 1 {
				_, _ = w.Write([]byte(`[{"id":"prospect-1","page_title":"Example Restaurant","meta_description":"Local restaurant","h1":"Welcome","body_text":"Seasonal menu and events","screenfizz_businesses":{"business_name":"Example Restaurant","category":"restaurant"}}]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case http.MethodPatch:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), `"business_summary":"A restaurant"`) || !strings.Contains(string(body), `"personalisation_line":"I noticed you regularly promote seasonal offers`) || !strings.Contains(string(body), `"analysed":true`) {
				t.Fatalf("unexpected analysis update: %s", body)
			}
			updated = true
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer supabase.Close()

	err := AnalyseProspects(context.Background(), Config{
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
		t.Fatal("expected analysis to be saved")
	}
}
