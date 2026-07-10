package leadengine

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseWebsiteHTMLExtractsRequestedFields(t *testing.T) {
	parsed, err := parseWebsiteHTML(`
		<html><head><title> Example title </title><meta name="description" content=" Example description "></head>
		<body><script>ignored script</script><h1> Main heading </h1><p>Hello <strong>world</strong>.</p></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.PageTitle != "Example title" || parsed.MetaDescription != "Example description" || parsed.H1 != "Main heading" || parsed.BodyText != "Main heading Hello world ." {
		t.Fatalf("unexpected parsed website: %#v", parsed)
	}
	if len([]rune(truncateText(strings.Repeat("a", 5001), 5000))) != 5000 {
		t.Fatal("body text truncation did not preserve the 5,000-character limit")
	}
}

func TestParseProspectsSavesParsedFields(t *testing.T) {
	getCalls := 0
	updated := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			if r.URL.Query().Get("analysed") == "eq.false" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			if r.URL.Query().Get("website_html") != "not.is.null" || r.URL.Query().Get("parsed") != "eq.false" {
				t.Fatalf("unexpected parse filters: %s", r.URL.RawQuery)
			}
			if getCalls == 1 {
				_, _ = w.Write([]byte(`[{"id":"prospect-1","website_html":"<html><head><title>Title</title><meta name=\"description\" content=\"Description\"></head><body><h1>Heading</h1><p>Body text</p></body></html>"}]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case http.MethodPatch:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), `"page_title":"Title"`) || !strings.Contains(string(body), `"meta_description":"Description"`) || !strings.Contains(string(body), `"h1":"Heading"`) || !strings.Contains(string(body), `"body_text":"Heading Body text"`) || !strings.Contains(string(body), `"parsed":true`) {
				t.Fatalf("unexpected parsed update: %s", body)
			}
			updated = true
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	err := ParseProspects(context.Background(), Config{
		SupabaseURL:            server.URL,
		SupabaseServiceRoleKey: "service-key",
		ProspectsTable:         "screenfizz_prospects",
		AIAPIKey:               "ai-key",
		AIAPIURL:               server.URL,
		AIModel:                "test-model",
		BrevoAPIKey:            "brevo-key",
		BrevoAPIURL:            server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Fatal("expected parsed HTML to be saved")
	}
}
