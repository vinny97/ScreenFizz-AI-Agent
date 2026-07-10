package leadengine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeBusinessesMapsGooglePlacesFields(t *testing.T) {
	t.Parallel()

	dataset := json.RawMessage(`[
		{
			"title": "Buckingham Cafe",
			"searchString": "cafe",
			"website": "https://www.buckinghamcafe.example/",
			"emails": ["HELLO@BUCKINGHAMCAFE.EXAMPLE"],
			"phoneNumber": "01280 000000",
			"address": "1 High Street, Buckingham MK18 1AA",
			"city": "Buckingham",
			"url": "https://maps.google.com/?cid=123",
			"totalScore": 4.7,
			"reviewsCount": 42,
			"lat": 51.999,
			"lng": -0.987
		}
	]`)

	businesses, err := DecodeBusinesses(dataset)
	if err != nil {
		t.Fatalf("DecodeBusinesses() error = %v", err)
	}
	if len(businesses) != 1 {
		t.Fatalf("len(businesses) = %d, want 1", len(businesses))
	}
	business := businesses[0]
	if business.BusinessName != "Buckingham Cafe" {
		t.Fatalf("BusinessName = %q, want Buckingham Cafe", business.BusinessName)
	}
	if business.Email != "hello@buckinghamcafe.example" {
		t.Fatalf("Email = %q, want normalized email", business.Email)
	}
	if business.Website != "buckinghamcafe.example" {
		t.Fatalf("Website = %q, want normalized website", business.Website)
	}
	if business.Postcode != "MK18 1AA" {
		t.Fatalf("Postcode = %q, want MK18 1AA", business.Postcode)
	}
	if business.Rating == nil || *business.Rating != 4.7 {
		t.Fatalf("Rating = %v, want 4.7", business.Rating)
	}
	if business.ReviewCount == nil || *business.ReviewCount != 42 {
		t.Fatalf("ReviewCount = %v, want 42", business.ReviewCount)
	}
}

func TestImportDatasetSkipsExistingAndBatchDuplicates(t *testing.T) {
	t.Parallel()

	var insertedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Range", "0-0/1")
			_, _ = w.Write([]byte(`[{"website":"https://existing.example","business_name":"Existing","town":"Aylesbury"}]`))
		case http.MethodPost:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read insert body: %v", err)
			}
			insertedBody = string(body)
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	importer := NewImporter(Config{
		SupabaseURL:            server.URL,
		SupabaseServiceRoleKey: "service-key",
		BusinessesTable:        "screenfizz_businesses",
	})
	dataset := json.RawMessage(`[
		{"title":"Existing Copy","website":"https://www.existing.example/","emails":["existing@example"],"city":"Aylesbury"},
		{"title":"New Cafe","website":"https://new.example","emails":["new@example"],"city":"Marlow"},
		{"title":"New Cafe","website":"https://new.example/","emails":["new@example"],"city":"Marlow"},
		{"title":"No Website But Unique","city":"High Wycombe"}
	]`)

	result, err := importer.ImportDataset(context.Background(), dataset)
	if err != nil {
		t.Fatalf("ImportDataset() error = %v", err)
	}
	if result.Imported != 1 {
		t.Fatalf("Imported = %d, want 1", result.Imported)
	}
	if result.DuplicatesSkipped != 2 {
		t.Fatalf("DuplicatesSkipped = %d, want 2", result.DuplicatesSkipped)
	}
	if result.NoWebsiteSkipped != 1 {
		t.Fatalf("NoWebsiteSkipped = %d, want 1", result.NoWebsiteSkipped)
	}
	if !strings.Contains(insertedBody, "New Cafe") || strings.Contains(insertedBody, "No Website But Unique") {
		t.Fatalf("insert body does not contain expected businesses: %s", insertedBody)
	}
}

func TestImportDatasetSkipsNoEmailAndPermanentlyClosedBusinesses(t *testing.T) {
	t.Parallel()

	var insertedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Range", "*/0")
			_, _ = w.Write([]byte(`[]`))
		case http.MethodPost:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			insertedBody = string(body)
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	importer := NewImporter(Config{
		SupabaseURL:            server.URL,
		SupabaseServiceRoleKey: "service-key",
		BusinessesTable:        "screenfizz_businesses",
	})
	result, err := importer.ImportDataset(context.Background(), json.RawMessage(`[
		{"title":"No Email","website":"https://no-email.example"},
		{"title":"Closed","website":"https://closed.example","emails":["closed@example"],"permanentlyClosed":true},
		{"title":"Open","website":"https://open.example","emails":["open@example"]}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 || result.NoEmailSkipped != 1 || result.ClosedSkipped != 1 || result.NoWebsiteSkipped != 0 || result.DuplicatesSkipped != 0 {
		t.Fatalf("unexpected import result: %#v", result)
	}
	if !strings.Contains(insertedBody, "Open") || strings.Contains(insertedBody, "No Email") || strings.Contains(insertedBody, "Closed") {
		t.Fatalf("unexpected insert body: %s", insertedBody)
	}
}
