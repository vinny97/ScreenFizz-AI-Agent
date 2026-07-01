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

func TestImportLeadsMapsAndSkipsEmails(t *testing.T) {
	t.Parallel()

	var inserted []leadInsert
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/v1/leads" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("apikey") != "service-key" {
			t.Error("missing Supabase authentication")
		}
		switch r.Method {
		case http.MethodGet:
			if r.URL.Query().Get("select") != "email" {
				t.Errorf("select = %q", r.URL.Query().Get("select"))
			}
			w.Header().Set("Content-Range", "0-0/1")
			fmt.Fprint(w, `[{"email":"existing@example.com"}]`)
		case http.MethodPost:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read insert: %v", err)
				return
			}
			if err := json.Unmarshal(body, &inserted); err != nil {
				t.Errorf("decode insert: %v", err)
			}
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	client, err := New(server.URL, "service-key")
	if err != nil {
		t.Fatal(err)
	}
	dataset := json.RawMessage(`[
		{"first_name":"Existing","email":"existing@example.com"},
		{"first_name":"Ada","last_name":"Lovelace","email":"ada@example.com","company_name":"Analytical Engines","company_website":"https://example.com","linkedin":"https://linkedin.com/in/ada","job_title":"Founder","industry":"Technology","company_size":"10","country":"GB"},
		{"firstName":"Grace","lastName":"Hopper","email":"grace@example.com","organizationName":"Compiler Corp","organizationWebsite":"https://compiler.example","linkedinUrl":"https://linkedin.com/in/grace","position":"Admiral","organizationIndustry":"Software","organizationSize":"100","country":"US"},
		{"first_name":"Duplicate","email":"ADA@example.com"},
		{"first_name":"Empty","email":""},
		{"first_name":"Null","email":null},
		{"first_name":"Whitespace","email":"   "}
	]`)
	result, err := client.ImportLeads(context.Background(), "Launch", dataset)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 2 || result.Skipped != 5 {
		t.Fatalf("result = %+v", result)
	}
	if len(inserted) != 2 {
		t.Fatalf("inserted %d leads", len(inserted))
	}
	want := leadInsert{
		Campaign: "Launch", FirstName: "Ada", LastName: "Lovelace",
		Email: "ada@example.com", CompanyName: "Analytical Engines",
		CompanyURL: "https://example.com", LinkedIn: "https://linkedin.com/in/ada",
		JobTitle: "Founder", Industry: "Technology", CompanySize: "10",
		Country: "GB", Status: "NEW",
	}
	if inserted[0] != want {
		t.Fatalf("inserted = %+v, want %+v", inserted[0], want)
	}
	newActorWant := leadInsert{
		Campaign: "Launch", FirstName: "Grace", LastName: "Hopper",
		Email: "grace@example.com", CompanyName: "Compiler Corp",
		CompanyURL: "https://compiler.example", LinkedIn: "https://linkedin.com/in/grace",
		JobTitle: "Admiral", Industry: "Software", CompanySize: "100",
		Country: "US", Status: "NEW",
	}
	if inserted[1] != newActorWant {
		t.Fatalf("inserted = %+v, want %+v", inserted[1], newActorWant)
	}
}
