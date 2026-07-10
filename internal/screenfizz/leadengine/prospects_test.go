package leadengine

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSyncProspectsQueuesEligibleBusinesses(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if r.URL.Query().Get("contacted") != "eq.false" || r.URL.Query().Get("website") != "not.is.null" || r.URL.Query().Get("email") != "not.is.null" {
				t.Fatalf("unexpected eligible-business filters: %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Range", "0-1/2")
			_, _ = w.Write([]byte(`[{"id":"11111111-1111-1111-1111-111111111111"},{"id":"22222222-2222-2222-2222-222222222222"}]`))
		case http.MethodPost:
			if r.URL.Path != "/rest/v1/screenfizz_prospects" || r.URL.Query().Get("on_conflict") != "business_id" {
				t.Fatalf("unexpected prospect insert target: %s", r.URL.String())
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "11111111-1111-1111-1111-111111111111") || !strings.Contains(string(body), "22222222-2222-2222-2222-222222222222") {
				t.Fatalf("unexpected prospect insert body: %s", body)
			}
			_, _ = w.Write([]byte(`[{"business_id":"11111111-1111-1111-1111-111111111111"}]`))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	result, err := SyncProspects(context.Background(), Config{
		SupabaseURL:            server.URL,
		SupabaseServiceRoleKey: "service-key",
		BusinessesTable:        "screenfizz_businesses",
		ProspectsTable:         "screenfizz_prospects",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 1 || result.Skipped != 1 {
		t.Fatalf("result = %#v, want one added and one skipped", result)
	}
}
