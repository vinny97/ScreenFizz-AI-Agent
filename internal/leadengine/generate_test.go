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

func TestGenerateQueuedEmails(t *testing.T) {
	t.Parallel()

	type update struct {
		Subject  string
		HTMLBody string
		TextBody string
		Status   string
	}

	updated := make(map[string]update)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/v1/campaigns":
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Range", "0-0/1")
			fmt.Fprint(w, `[{
				"name":"Campaign A",
				"active":true,
				"email_subject":"Hello from GoClaw",
				"sender_name":"Vinny",
				"sender_email":"vinny@example.com",
				"cta_text":"Try Influocial",
				"cta_url":"https://influocial.co.uk",
				"email_template_html":"<p>Hi <strong>{{full_name}}</strong> from {{company}}</p><p><a href=\"{{cta_url}}\">{{cta_text}}</a></p>{{signature}}",
				"email_template_text":"Hi {{first_name}},\nWe found {{company}} in {{country}}.\n\n{{cta_text}}: {{cta_url}}",
				"email_signature_html":"<p>Regards,<br>{{sender_name}}<br>{{sender_email}}<br><a href=\"{{cta_url}}\">{{cta_url}}</a></p>",
				"email_signature_text":"Regards,\n{{sender_name}}\n{{sender_email}}\n{{cta_url}}"
			}]`)
		case "/rest/v1/leads":
			switch r.Method {
			case http.MethodGet:
				if got := r.URL.Query().Get("status"); got != "eq."+leadStatusQueued {
					t.Errorf("status filter = %q", got)
				}
				if got := r.URL.Query().Get("select"); got != "id,first_name,last_name,email,company,website,linkedin_url,job_title,industry,company_size,country" {
					t.Errorf("select = %q", got)
				}
				w.Header().Set("Content-Range", "0-1/2")
				fmt.Fprint(w, `[
					{"id":"1","first_name":"Ada","last_name":"Lovelace","email":"ada@example.com","company":"Analytical Engines","website":"https://analytical.example","linkedin_url":"https://linkedin.com/in/ada","job_title":"Founder","industry":"Technology","company_size":"10","country":"UK"},
					{"id":"2","first_name":"Grace","last_name":"Hopper","email":"grace@example.com","company":"Compiler Corp","website":"https://compiler.example","linkedin_url":"https://linkedin.com/in/grace","job_title":"Admiral","industry":"Software","company_size":"100","country":"US"}
				]`)
			case http.MethodPatch:
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read email update: %v", err)
					return
				}
				var patch map[string]string
				if err := json.Unmarshal(body, &patch); err != nil {
					t.Errorf("decode email update: %v", err)
					return
				}
				updated[r.URL.Query().Get("id")] = update{
					Subject:  patch["email_subject"],
					HTMLBody: patch["email_body_html"],
					TextBody: patch["email_body_text"],
					Status:   patch["status"],
				}
				w.WriteHeader(http.StatusNoContent)
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := New(server.URL, "service-key")
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.GenerateQueuedEmails(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Generated != 2 {
		t.Fatalf("result = %+v", result)
	}

	want := map[string]update{
		"eq.1": {
			Subject:  "Hello from GoClaw",
			HTMLBody: "<p>Hi <strong>Ada Lovelace</strong> from Analytical Engines</p><p><a href=\"https://influocial.co.uk\">Try Influocial</a></p><p>Regards,<br>Vinny<br>vinny@example.com<br><a href=\"https://influocial.co.uk\">https://influocial.co.uk</a></p>",
			TextBody: "Hi Ada,\nWe found Analytical Engines in UK.\n\nTry Influocial: https://influocial.co.uk\n\nRegards,\nVinny\nvinny@example.com\nhttps://influocial.co.uk",
			Status:   leadStatusEmailReady,
		},
		"eq.2": {
			Subject:  "Hello from GoClaw",
			HTMLBody: "<p>Hi <strong>Grace Hopper</strong> from Compiler Corp</p><p><a href=\"https://influocial.co.uk\">Try Influocial</a></p><p>Regards,<br>Vinny<br>vinny@example.com<br><a href=\"https://influocial.co.uk\">https://influocial.co.uk</a></p>",
			TextBody: "Hi Grace,\nWe found Compiler Corp in US.\n\nTry Influocial: https://influocial.co.uk\n\nRegards,\nVinny\nvinny@example.com\nhttps://influocial.co.uk",
			Status:   leadStatusEmailReady,
		},
	}
	if len(updated) != len(want) {
		t.Fatalf("updated = %#v", updated)
	}
	for id, expected := range want {
		if updated[id] != expected {
			t.Errorf("updated[%q] = %+v, want %+v", id, updated[id], expected)
		}
	}
}

func TestRenderTemplateWithSignature(t *testing.T) {
	t.Parallel()

	got := renderTemplateWithSignature("Hello {{first_name}}", "Regards,\n{{sender_name}}", map[string]string{
		"first_name":  "Ada",
		"sender_name": "Vinny",
	})
	want := "Hello Ada\n\nRegards,\nVinny"
	if got != want {
		t.Fatalf("renderTemplateWithSignature() = %q, want %q", got, want)
	}
}

func TestRemoveEmailEmDashes(t *testing.T) {
	t.Parallel()

	got := removeEmailEmDashes("Hello — a natural note")
	if got != "Hello, a natural note" {
		t.Fatalf("removeEmailEmDashes() = %q", got)
	}
}

func TestRenderPlaceholdersSupportsUnsubscribeURL(t *testing.T) {
	t.Parallel()

	got := renderPlaceholders("Click here: {{unsubscribe_url}}", map[string]string{
		"unsubscribe_url": "https://example.com/unsubscribe",
	})
	want := "Click here: https://example.com/unsubscribe"
	if got != want {
		t.Fatalf("renderPlaceholders() = %q, want %q", got, want)
	}
}
