package leadengine

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReviewProspectsApprovesEditsAndSkips(t *testing.T) {
	updates := make(map[string]string)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if r.URL.Query().Get("status") != "eq.ready_to_send" || r.URL.Query().Get("limit") != "20" {
				t.Fatalf("unexpected review query: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`[
				{"id":"one","business_summary":"Summary one","email_subject":"Subject one","email_body":"Body one","screenfizz_businesses":{"business_name":"One Ltd","email":"one@example.com"}},
				{"id":"two","business_summary":"Summary two","email_subject":"Subject two","email_body":"Body two","screenfizz_businesses":{"business_name":"Two Ltd","email":"two@example.com"}},
				{"id":"three","business_summary":"Summary three","email_subject":"Subject three","email_body":"Body three","screenfizz_businesses":{"business_name":"Three Ltd","email":"three@example.com"}}
			]`))
		case http.MethodPatch:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			updates[r.URL.Query().Get("id")] = string(body)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	var output bytes.Buffer
	input := strings.NewReader("A\nE\nEdited subject\nEdited line one\nEdited line two\n.\nS\n")
	err := ReviewProspects(context.Background(), Config{
		SupabaseURL:            server.URL,
		SupabaseServiceRoleKey: "service-key",
		ProspectsTable:         "screenfizz_prospects",
	}, input, &output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(updates["eq.one"], `"status":"approved"`) {
		t.Fatalf("approval update = %s", updates["eq.one"])
	}
	if !strings.Contains(updates["eq.two"], `"email_subject":"Edited subject"`) || !strings.Contains(updates["eq.two"], `"email_body":"Edited line one\nEdited line two"`) || !strings.Contains(updates["eq.two"], `"status":"ready_to_send"`) {
		t.Fatalf("edit update = %s", updates["eq.two"])
	}
	if !strings.Contains(updates["eq.three"], `"status":"skipped"`) {
		t.Fatalf("skip update = %s", updates["eq.three"])
	}
	for _, expected := range []string{"Business: One Ltd", "Email: one@example.com", "AI summary: Summary one", "Subject: Subject one", "Email body:\nBody one"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("review output is missing %q: %s", expected, output.String())
		}
	}
}
