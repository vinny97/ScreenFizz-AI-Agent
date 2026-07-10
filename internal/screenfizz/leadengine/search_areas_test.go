package leadengine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestLoadEnabledSearchAreas(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("enabled") != "eq.true" {
			t.Fatalf("enabled filter = %q", r.URL.Query().Get("enabled"))
		}
		if r.URL.Query().Get("select") != "county" {
			t.Fatalf("select = %q", r.URL.Query().Get("select"))
		}
		_, _ = w.Write([]byte(`[{"county":"Bedfordshire"},{"county":"Oxfordshire"}]`))
	}))
	defer server.Close()

	counties, err := LoadEnabledSearchAreas(context.Background(), Config{
		SupabaseURL:            server.URL,
		SupabaseServiceRoleKey: "service-key",
		SearchAreasTable:       "screenfizz_search_areas",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Bedfordshire", "Oxfordshire"}; !reflect.DeepEqual(counties, want) {
		t.Fatalf("counties = %#v, want %#v", counties, want)
	}
}
