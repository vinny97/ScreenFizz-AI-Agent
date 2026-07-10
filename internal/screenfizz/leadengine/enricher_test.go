package leadengine

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/security"
)

func TestEnrichProspectsDownloadsAndSavesHTML(t *testing.T) {
	security.SetAllowLoopbackForTest(true)
	defer security.SetAllowLoopbackForTest(false)

	website := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>ScreenFizz test</body></html>"))
	}))
	defer website.Close()

	updated := false
	supabase := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if r.URL.Path != "/rest/v1/screenfizz_prospects" || r.URL.Query().Get("enriched") != "eq.false" || r.URL.Query().Get("limit") != "25" {
				t.Fatalf("unexpected prospect query: %s", r.URL.String())
			}
			_, _ = w.Write([]byte(`[{"id":"prospect-1","screenfizz_businesses":{"business_name":"Example Business","website":"` + website.URL + `"}}]`))
		case http.MethodPatch:
			if r.URL.Query().Get("id") != "eq.prospect-1" {
				t.Fatalf("unexpected update query: %s", r.URL.RawQuery)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "ScreenFizz test") || !strings.Contains(string(body), `"enriched":true`) {
				t.Fatalf("unexpected update body: %s", body)
			}
			updated = true
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer supabase.Close()

	err := EnrichProspects(context.Background(), Config{
		SupabaseURL:            supabase.URL,
		SupabaseServiceRoleKey: "service-key",
		ProspectsTable:         "screenfizz_prospects",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Fatal("expected prospect HTML to be saved")
	}
}
