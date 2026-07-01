package leadengine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestListCampaigns(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/v1/campaigns" || r.URL.Query().Get("select") != "*" {
			t.Errorf("unexpected request URL: %s", r.URL.String())
		}
		if got := r.Header.Get("apikey"); got != "service-key" {
			t.Errorf("apikey = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer service-key" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Range"); got != "0-999" {
			t.Errorf("Range = %q", got)
		}
		w.Header().Set("Content-Range", "0-1/2")
		fmt.Fprint(w, `[{"id":1,"name":"First"},{"id":2,"name":"Second"}]`)
	}))
	defer server.Close()

	client, err := New(server.URL, "service-key")
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.ListCampaigns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Campaign{{"id": json.Number("1"), "name": "First"}, {"id": json.Number("2"), "name": "Second"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListCampaigns() = %#v, want %#v", got, want)
	}
}

func TestNewRequiresConfiguration(t *testing.T) {
	t.Parallel()

	if _, err := New("", "key"); err == nil {
		t.Fatal("New() accepted an empty URL")
	}
	if _, err := New("https://example.supabase.co", ""); err == nil {
		t.Fatal("New() accepted an empty service role key")
	}
}
