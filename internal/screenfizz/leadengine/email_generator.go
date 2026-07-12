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
	"strings"
	"time"
)

const emailGenerationBatchSize = 25

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
	client, err := newAnalysisClient(cfg)
	if err != nil {
		return err
	}
	for {
		prospects, err := nextEmailProspects(ctx, cfg)
		if err != nil {
			return err
		}
		if len(prospects) == 0 {
			return nil
		}
		failed := 0
		for _, prospect := range prospects {
			email, err := client.generateEmail(ctx, prospect)
			if err != nil {
				slog.Error("Failed to generate prospect email", "prospect_id", prospect.ID, "error", err)
				failed++
				continue
			}
			if err := saveGeneratedEmail(ctx, cfg, prospect.ID, email); err != nil {
				slog.Error("Failed to save prospect email", "prospect_id", prospect.ID, "error", err)
				failed++
			}
		}
		if failed > 0 {
			return fmt.Errorf("failed to generate or save %d ScreenFizz prospect emails", failed)
		}
	}
}

func nextEmailProspects(ctx context.Context, cfg Config) ([]emailProspect, error) {
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable)
	query := url.Values{
		"select":          {"id,business_summary,business_type,recommended_use_case,personalisation_line,screenfizz_businesses(business_name,category)"},
		"analysed":        {"eq.true"},
		"email_generated": {"eq.false"},
		"order":           {"created_at.asc"},
		"limit":           {fmt.Sprintf("%d", emailGenerationBatchSize)},
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

func (c *analysisClient) generateEmail(ctx context.Context, prospect emailProspect) (GeneratedEmail, error) {
	input, err := json.Marshal(map[string]string{
		"business_name":        prospect.Business.BusinessName,
		"category":             prospect.Business.Category,
		"business_summary":     prospect.BusinessSummary,
		"business_type":        prospect.BusinessType,
		"recommended_use_case": prospect.RecommendedUseCase,
		"personalisation_line": prospect.PersonalisationLine,
	})
	if err != nil {
		return GeneratedEmail{}, fmt.Errorf("encode email generation input: %w", err)
	}
	content, err := c.completeJSON(ctx,
		"Return only a JSON object with exactly these fields: subject (string), email_body (string). Write a personal, natural outreach email under 150 words. Mention the business by name, include the supplied personalisation_line exactly, explain one specific way ScreenFizz could help this business, and end by asking whether they would like a free mock-up. Do not use markdown. Do not include a signature. Do not claim to have used AI.",
		string(input))
	if err != nil {
		return GeneratedEmail{}, err
	}
	return decodeGeneratedEmail(content)
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
	if len(strings.Fields(*raw.Body)) > 150 {
		return GeneratedEmail{}, errors.New("generated email body exceeds 150 words")
	}
	return GeneratedEmail{Subject: strings.TrimSpace(*raw.Subject), Body: strings.TrimSpace(*raw.Body)}, nil
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
