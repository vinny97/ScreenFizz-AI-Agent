package leadengine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInsertTestBusiness(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/rest/v1/screenfizz_businesses" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var rows []map[string]any
		if err := json.Unmarshal(body, &rows); err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 {
			t.Fatalf("inserted rows = %d, want 1", len(rows))
		}
		row := rows[0]
		if row["business_name"] != "Test Restaurant" || row["category"] != "restaurant" || row["website"] != "https://testrestaurant.co.uk" || row["email"] != "hello@testrestaurant.co.uk" || row["phone"] != "01234 567890" || row["address"] != "1 High Street" || row["town"] != "Milton Keynes" || row["postcode"] != "MK9 1AA" || row["source"] != "test" {
			t.Fatalf("unexpected test business: %#v", row)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	err := InsertTestBusiness(context.Background(), Config{
		SupabaseURL:            server.URL,
		SupabaseServiceRoleKey: "service-role-key",
		BusinessesTable:        "screenfizz_businesses",
	})
	if err != nil {
		t.Fatal(err)
	}
}
