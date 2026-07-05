package leadengine

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRetryableBrevoError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status int
		want   bool
	}{
		{status: http.StatusBadRequest, want: false},
		{status: http.StatusUnauthorized, want: false},
		{status: http.StatusRequestTimeout, want: true},
		{status: http.StatusTooManyRequests, want: true},
		{status: http.StatusInternalServerError, want: true},
	}
	for _, tt := range tests {
		err := &BrevoAPIError{StatusCode: tt.status}
		if got := retryableBrevoError(err); got != tt.want {
			t.Fatalf("retryableBrevoError(%d) = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestBrevoWebhookRejectsInvalidSecret(t *testing.T) {
	t.Parallel()
	client, err := New("https://example.supabase.co", "service-key")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewBrevoWebhookHandler(client, "correct-secret")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/leadengine/brevo/events/wrong-secret", nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}
