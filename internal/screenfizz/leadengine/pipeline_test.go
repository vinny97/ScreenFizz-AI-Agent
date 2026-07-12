package leadengine

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestApproveReadyToSendProspects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Query().Get("status") != "eq.ready_to_send" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != `{"status":"approved"}` {
			t.Fatalf("unexpected update: %s", body)
		}
		_, _ = w.Write([]byte(`[{"id":"one"},{"id":"two"}]`))
	}))
	defer server.Close()

	count, err := ApproveReadyToSendProspects(context.Background(), Config{
		SupabaseURL:            server.URL,
		SupabaseServiceRoleKey: "service-key",
		ProspectsTable:         "screenfizz_prospects",
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("approved = %d, want 2", count)
	}
}
