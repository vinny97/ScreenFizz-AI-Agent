package leadengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const emailGenerationBatchSize = 25

var emDashPattern = regexp.MustCompile(`\s*—\s*`)

type emailProspect struct {
	ID                  string `json:"id"`
	BusinessSummary     string `json:"business_summary"`
	BusinessType        string `json:"business_type"`
	RecommendedUseCase  string `json:"recommended_use_case"`
	PersonalisationLine string `json:"personalisation_line"`
	Business            struct {
		BusinessName string `json:"business_name"`
		Category     string `json:"category"`
	} `json:"screenfizz_businesses"`
}

type GeneratedEmail struct {
	Subject string
	Body    string
}

type rawGeneratedEmail struct {
	Subject *string `json:"subject"`
	Body    *string `json:"email_body"`
}

// GenerateProspectEmails stores a short, personal draft for each analysed
// prospect that has not already received a generated email. It never sends it.
func GenerateProspectEmails(ctx context.Context, cfg Config) error {
	return generateProspectEmails(ctx, cfg, 0)
}

// GenerateProspectEmailsUpTo creates at most maximum new drafts. A
// non-positive maximum generates every pending draft.
func GenerateProspectEmailsUpTo(ctx context.Context, cfg Config, maximum int) error {
	return generateProspectEmails(ctx, cfg, maximum)
}

func generateProspectEmails(ctx context.Context, cfg Config, maximum int) error {
	processedTotal := 0
	for {
		if maximum > 0 && processedTotal >= maximum {
			return nil
		}
		limit := emailGenerationBatchSize
		if maximum > 0 && maximum-processedTotal < limit {
			limit = maximum - processedTotal
		}
		prospects, err := nextEmailProspects(ctx, cfg, limit)
		if err != nil {
			return err
		}
		if len(prospects) == 0 {
			return nil
		}
		failed := 0
		for _, prospect := range prospects {
			email := generateScreenFizzEmail(prospect)
			if err := saveGeneratedEmail(ctx, cfg, prospect.ID, email); err != nil {
				slog.Error("Failed to save prospect email", "prospect_id", prospect.ID, "error", err)
				failed++
			}
		}
		if failed > 0 {
			slog.Warn("ScreenFizz email generation batch completed with failures; successful drafts will continue through the pipeline", "failed", failed)
			if maximum <= 0 {
				return nil
			}
		}
		processedTotal += len(prospects)
	}
}

func nextEmailProspects(ctx context.Context, cfg Config, limit int) ([]emailProspect, error) {
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable)
	query := url.Values{
		"select":          {"id,business_summary,business_type,recommended_use_case,personalisation_line,screenfizz_businesses(business_name,category)"},
		"analysed":        {"eq.true"},
		"email_generated": {"eq.false"},
		"order":           {"created_at.asc"},
		"limit":           {fmt.Sprintf("%d", limit)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create ScreenFizz email generation request: %w", err)
	}
	setSupabaseHeaders(req, cfg)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("list email-pending ScreenFizz prospects: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read email-pending ScreenFizz prospects response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("list email-pending ScreenFizz prospects: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var prospects []emailProspect
	if err := json.Unmarshal(body, &prospects); err != nil {
		return nil, fmt.Errorf("decode email-pending ScreenFizz prospects response: %w", err)
	}
	return prospects, nil
}

func generateScreenFizzEmail(prospect emailProspect) GeneratedEmail {
	businessName := strings.TrimSpace(prospect.Business.BusinessName)
	if businessName == "" {
		businessName = "there"
	}
	body := fmt.Sprintf(`Hi %s team,

I came across %s and thought ScreenFizz could be a good fit for your business.

We help local businesses turn ordinary TVs and commercial displays into professional digital signage for menus, promotions, offers, events and customer information.

The main difference is that we can manage everything for you. We create the content, update the screens remotely and keep everything looking fresh, so your team does not have to design graphics or keep changing USB drives.

If you already have a TV or screen, we can usually use your existing setup. We also provide the player, software and full screen packages if needed.

Our managed service starts from £15 per month per screen, and you can request changes through WhatsApp whenever you need something updated.

Would you be open to seeing a free example of what a ScreenFizz display could look like for your business?

Best,
Vinny
ScreenFizz
screenfizz.com`, businessName, businessName)
	return GeneratedEmail{
		Subject: "A simple way to improve your in-store screens",
		Body:    removeEmailEmDashes(body),
	}
}

func decodeGeneratedEmail(content string) (GeneratedEmail, error) {
	var raw rawGeneratedEmail
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return GeneratedEmail{}, fmt.Errorf("generated email is not valid JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return GeneratedEmail{}, errors.New("generated email must contain one JSON object")
	}
	if raw.Subject == nil || raw.Body == nil || strings.TrimSpace(*raw.Subject) == "" || strings.TrimSpace(*raw.Body) == "" {
		return GeneratedEmail{}, errors.New("generated email response is missing required fields")
	}
	body := removeEmailEmDashes(strings.TrimSpace(*raw.Body))
	if len(strings.Fields(body)) > 150 {
		return GeneratedEmail{}, errors.New("generated email body exceeds 150 words")
	}
	return GeneratedEmail{Subject: strings.TrimSpace(*raw.Subject), Body: body}, nil
}

func removeEmailEmDashes(value string) string {
	return emDashPattern.ReplaceAllString(value, ", ")
}

func saveGeneratedEmail(ctx context.Context, cfg Config, prospectID string, email GeneratedEmail) error {
	body, err := json.Marshal(map[string]any{
		"email_subject":   email.Subject,
		"email_body":      email.Body,
		"email_generated": true,
		"status":          "ready_to_send",
	})
	if err != nil {
		return fmt.Errorf("encode generated email: %w", err)
	}
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable) + "?id=eq." + url.QueryEscape(prospectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ScreenFizz generated email update request: %w", err)
	}
	setSupabaseHeaders(req, cfg)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "return=minimal")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("save ScreenFizz generated email: %w", err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return fmt.Errorf("read ScreenFizz generated email update response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("save ScreenFizz generated email: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}
	return nil
}
