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

const (
	emailGenerationBatchSize = 25
	generatedEmailMaxWords   = 220
	screenFizzEmailSubject   = "A simple way to improve your in-store screens"
	screenFizzEmailBase      = `Hi [Business Name] team,

I came across [Business Name] and wanted to introduce ScreenFizz.

We help local businesses display menus, offers, promotions and announcements on TVs or digital screens.

Here is what we provide:

• A ScreenFizz player that connects to your TV
• Digital signage software
• Professionally designed content
• Remote screen updates
• Scheduling for different times and days
• WhatsApp support for quick changes

You can send us a new offer, price change or announcement through WhatsApp, and we can update the screen remotely for you.

You do not need to design anything, use USB drives or manage complicated software.

If you already have a TV, we can usually use it. We can also provide a complete screen setup if needed.

Our managed service starts from £15 per month per screen.

Would you be open to seeing a free example of what we could create for [Business Name]?

Best,
Vinny
ScreenFizz
screenfizz.com`
)

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
		"status":          {"is.null"},
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
	personalisation := toBritishEnglish(strings.TrimSpace(prospect.PersonalisationLine))
	if personalisation != "" {
		personalisation += "\n\n"
	}
	body := fmt.Sprintf(`Hi %s team,

I came across %s and wanted to introduce ScreenFizz.

%sWe help local businesses display menus, offers, promotions and announcements on TVs or digital screens.

Here is what we provide:

• A ScreenFizz player that connects to your TV
• Digital signage software
• Professionally designed content
• Remote screen updates
• Scheduling for different times and days
• WhatsApp support for quick changes

You can send us a new offer, price change or announcement through WhatsApp, and we can update the screen remotely for you.

You do not need to design anything, use USB drives or manage complicated software.

If you already have a TV, we can usually use it. We can also provide a complete screen setup if needed.

Our managed service starts from £15 per month per screen.

Would you be open to seeing a free example of what we could create for %s?

Best,
Vinny
ScreenFizz
screenfizz.com`, businessName, businessName, personalisation, businessName)
	return GeneratedEmail{Subject: screenFizzEmailSubject, Body: toBritishEnglish(removeEmailEmDashes(body))}
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
	if len(strings.Fields(body)) > generatedEmailMaxWords {
		return GeneratedEmail{}, errors.New("generated email body exceeds 150 words")
	}
	return GeneratedEmail{Subject: strings.TrimSpace(*raw.Subject), Body: body}, nil
}

func removeEmailEmDashes(value string) string {
	return emDashPattern.ReplaceAllString(value, ", ")
}

var britishEnglishReplacer = strings.NewReplacer(
	"specialized", "specialised",
	"specializing", "specialising",
	"specialize", "specialise",
	"customized", "customised",
	"customizing", "customising",
	"customize", "customise",
	"personalized", "personalised",
	"personalizing", "personalising",
	"personalize", "personalise",
	"organized", "organised",
	"organizing", "organising",
	"organize", "organise",
	"favorite", "favourite",
	"color", "colour",
)

func toBritishEnglish(value string) string {
	return britishEnglishReplacer.Replace(value)
}

func saveGeneratedEmail(ctx context.Context, cfg Config, prospectID string, email GeneratedEmail) error {
	return saveGeneratedEmailStatus(ctx, cfg, prospectID, email, "ready_to_send")
}

func saveGeneratedEmailStatus(ctx context.Context, cfg Config, prospectID string, email GeneratedEmail, status string) error {
	body, err := json.Marshal(map[string]any{
		"email_subject":   email.Subject,
		"email_body":      email.Body,
		"email_generated": true,
		"status":          status,
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
