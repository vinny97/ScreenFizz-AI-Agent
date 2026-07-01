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

func TestQueueReadyLeads(t *testing.T) {
	t.Parallel()

	updated := make(map[string]string)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/v1/leads" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if got := r.URL.Query().Get("status"); got != "eq."+leadStatusReadyToEmail {
				t.Errorf("status filter = %q", got)
			}
			if got := r.URL.Query().Get("order"); got != "created_at.asc" {
				t.Errorf("order = %q", got)
			}
			if got := r.Header.Get("Range"); got != "0-99" {
				t.Errorf("range = %q", got)
			}
			w.Header().Set("Content-Range", "0-1/2")
			fmt.Fprint(w, `[{"id":"1"},{"id":"2"}]`)
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
	result, err := client.QueueReadyLeads(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Queued != 2 {
		t.Fatalf("result = %+v", result)
	}
	want := map[string]string{
		"eq.1": leadStatusQueued,
		"eq.2": leadStatusQueued,
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
