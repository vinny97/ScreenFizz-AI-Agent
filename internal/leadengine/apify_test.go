package leadengine

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestApifyRun(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("token"); got != "apify-token" {
			t.Errorf("token = %q", got)
		}
		switch r.URL.Path {
		case "/v2/actors/example/runs":
			if r.Method != http.MethodPost {
				t.Errorf("start method = %s", r.Method)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read input: %v", err)
				return
			}
			if string(body) != `{"search":"coffee"}` {
				t.Errorf("input = %s", body)
			}
			fmt.Fprint(w, `{"data":{"id":"run-123","status":"RUNNING"}}`)
		case "/v2/actor-runs/run-123":
			fmt.Fprint(w, `{"data":{"id":"run-123","status":"SUCCEEDED"}}`)
		case "/v2/actor-runs/run-123/dataset/items":
			fmt.Fprint(w, `[{"name":"Result"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &ApifyClient{
		token:        "apify-token",
		httpClient:   server.Client(),
		pollInterval: time.Millisecond,
	}
	got, err := client.Run(context.Background(), &ActiveCampaign{
		ApifyAPIURL: server.URL + "/v2/actors/example/runs?memory=256",
		ApifyInput:  []byte(`{"search":"coffee"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `[{"name":"Result"}]` {
		t.Fatalf("dataset = %s", got)
	}
}

func TestCampaignIsActive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		campaign Campaign
		want     bool
	}{
		{Campaign{"active": true}, true},
		{Campaign{"active": false, "status": "active"}, false},
		{Campaign{"is_active": true}, true},
		{Campaign{"status": "ACTIVE"}, true},
		{Campaign{"status": "draft"}, false},
	}
	for _, test := range tests {
		if got := campaignIsActive(test.campaign); !reflect.DeepEqual(got, test.want) {
			t.Errorf("campaignIsActive(%v) = %v, want %v", test.campaign, got, test.want)
		}
	}
}
