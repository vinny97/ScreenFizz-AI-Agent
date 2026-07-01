package leadengine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQualifyNewLeads(t *testing.T) {
	t.Parallel()

	updated := make(map[string]string)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/v1/leads" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if got := r.URL.Query().Get("status"); got != "eq.NEW" {
				t.Errorf("status filter = %q", got)
			}
			w.Header().Set("Content-Range", "0-3/4")
			fmt.Fprint(w, `[
				{"id":"1","email":"person@example.com","website":"https://example.com"},
				{"id":"2","email":" INFO@example.com ","website":"https://example.com"},
				{"id":"3","email":"","website":"https://example.com"},
				{"id":"4","email":"person2@example.com","website":"   "}
			]`)
		case http.MethodPatch:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read status update: %v", err)
				return
			}
			var patch map[string]string
			if err := json.Unmarshal(body, &patch); err != nil {
				t.Errorf("decode status update: %v", err)
				return
			}
			updated[r.URL.Query().Get("id")] = patch["status"]
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	client, err := New(server.URL, "service-key")
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.QualifyNewLeads(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Qualified != 2 || result.Rejected != 2 {
		t.Fatalf("result = %+v", result)
	}
	want := map[string]string{
		"eq.1": leadStatusReadyToEmail,
		"eq.2": leadStatusReadyToEmail,
		"eq.3": leadStatusRejected,
		"eq.4": leadStatusRejected,
	}
	if len(updated) != len(want) {
		t.Fatalf("updated = %#v", updated)
	}
	for id, status := range want {
		if updated[id] != status {
			t.Errorf("updated[%q] = %q, want %q", id, updated[id], status)
		}
	}
}

func TestGenericEmailsAreQualified(t *testing.T) {
	t.Parallel()
	for _, email := range []string{"info@x.com", "hello@x.com", "support@x.com", "sales@x.com", "admin@x.com", "contact@x.com"} {
		if shouldRejectLead(email, "https://x.com") {
			t.Errorf("shouldRejectLead(%q) = true", email)
		}
	}
}
