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

const analysisBatchSize = 25

type analysisProspect struct {
	ID              string `json:"id"`
	PageTitle       string `json:"page_title"`
	MetaDescription string `json:"meta_description"`
	H1              string `json:"h1"`
	BodyText        string `json:"body_text"`
	Business        struct {
		BusinessName string `json:"business_name"`
		Category     string `json:"category"`
	} `json:"screenfizz_businesses"`
}

type BusinessAnalysis struct {
	BusinessSummary     string
	BusinessType        string
	RecommendedUseCase  string
	PersonalisationLine string
}

type rawBusinessAnalysis struct {
	BusinessSummary     *string `json:"business_summary"`
	BusinessType        *string `json:"business_type"`
	RecommendedUseCase  *string `json:"recommended_use_case"`
	PersonalisationLine *string `json:"personalisation_line"`
}

type analysisClient struct {
	apiKey     string
	apiURL     string
	model      string
	httpClient *http.Client
}

func newAnalysisClient(cfg Config) (*analysisClient, error) {
	if strings.TrimSpace(cfg.AIAPIKey) == "" {
		return nil, errors.New("SCREENFIZZ_AI_API_KEY or OPENAI_API_KEY is required")
	}
	if strings.TrimSpace(cfg.AIAPIURL) == "" {
		return nil, errors.New("SCREENFIZZ_AI_API_URL is required")
	}
	if strings.TrimSpace(cfg.AIModel) == "" {
		return nil, errors.New("SCREENFIZZ_AI_MODEL is required")
	}
	return &analysisClient{
		apiKey:     cfg.AIAPIKey,
		apiURL:     cfg.AIAPIURL,
		model:      cfg.AIModel,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// AnalyseProspects sends parsed prospect data to the configured AI and stores
// the validated analysis.
func AnalyseProspects(ctx context.Context, cfg Config) error {
	return analyseProspects(ctx, cfg, 0)
}

// AnalyseProspectsUpTo runs AI analysis for at most maximum prospects. A
// non-positive maximum processes all analysis-ready prospects.
func AnalyseProspectsUpTo(ctx context.Context, cfg Config, maximum int) error {
	return analyseProspects(ctx, cfg, maximum)
}

func analyseProspects(ctx context.Context, cfg Config, maximum int) error {
	client, err := newAnalysisClient(cfg)
	if err != nil {
		return err
	}
	processedTotal := 0
	for {
		if maximum > 0 && processedTotal >= maximum {
			return nil
		}
		limit := analysisBatchSize
		if maximum > 0 && maximum-processedTotal < limit {
			limit = maximum - processedTotal
		}
		prospects, err := nextUnanalysedProspects(ctx, cfg, limit)
		if err != nil {
			return err
		}
		if len(prospects) == 0 {
			return nil
		}
		failed := 0
		for _, prospect := range prospects {
			analysis, err := client.analyse(ctx, prospect)
			if err != nil {
				slog.Error("Failed to analyse prospect", "prospect_id", prospect.ID, "error", err)
				failed++
				continue
			}
			if err := saveBusinessAnalysis(ctx, cfg, prospect.ID, analysis); err != nil {
				slog.Error("Failed to save prospect analysis", "prospect_id", prospect.ID, "error", err)
				failed++
			}
		}
		if failed > 0 {
			slog.Warn("ScreenFizz analysis batch completed with failures; successful prospects will continue to email generation", "failed", failed)
			if maximum <= 0 {
				return nil
			}
		}
		processedTotal += len(prospects)
	}
}

func nextUnanalysedProspects(ctx context.Context, cfg Config, limit int) ([]analysisProspect, error) {
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable)
	query := url.Values{
		"select":   {"id,page_title,meta_description,h1,body_text,screenfizz_businesses(business_name,category)"},
		"parsed":   {"eq.true"},
		"analysed": {"eq.false"},
		"order":    {"created_at.asc"},
		"limit":    {fmt.Sprintf("%d", limit)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create ScreenFizz prospects analysis request: %w", err)
	}
	setSupabaseHeaders(req, cfg)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("list unanalysed ScreenFizz prospects: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read unanalysed ScreenFizz prospects response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("list unanalysed ScreenFizz prospects: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var prospects []analysisProspect
	if err := json.Unmarshal(body, &prospects); err != nil {
		return nil, fmt.Errorf("decode unanalysed ScreenFizz prospects response: %w", err)
	}
	return prospects, nil
}

func (c *analysisClient) analyse(ctx context.Context, prospect analysisProspect) (BusinessAnalysis, error) {
	input, err := json.Marshal(map[string]string{
		"business_name":    prospect.Business.BusinessName,
		"category":         prospect.Business.Category,
		"page_title":       prospect.PageTitle,
		"meta_description": prospect.MetaDescription,
		"h1":               prospect.H1,
		"body_text":        prospect.BodyText,
	})
	if err != nil {
		return BusinessAnalysis{}, fmt.Errorf("encode analysis input: %w", err)
	}
	requestBody, err := json.Marshal(map[string]any{
		"model":       c.model,
		"temperature": 0,
		"response_format": map[string]string{
			"type": "json_object",
		},
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "Return only a JSON object with exactly these fields: business_summary (string), business_type (string), recommended_use_case (string), personalisation_line (string). The personalisation_line must be a specific, natural observation grounded in the supplied website data, suitable for the opening of a future outreach email. Do not generate a full email. Do not include markdown or additional fields.",
			},
			{"role": "user", "content": string(input)},
		},
	})
	if err != nil {
		return BusinessAnalysis{}, fmt.Errorf("encode AI analysis request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL, bytes.NewReader(requestBody))
	if err != nil {
		return BusinessAnalysis{}, fmt.Errorf("create AI analysis request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return BusinessAnalysis{}, fmt.Errorf("send AI analysis request: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return BusinessAnalysis{}, fmt.Errorf("read AI analysis response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return BusinessAnalysis{}, fmt.Errorf("AI returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &completion); err != nil {
		return BusinessAnalysis{}, fmt.Errorf("decode AI analysis response: %w", err)
	}
	if len(completion.Choices) == 0 || strings.TrimSpace(completion.Choices[0].Message.Content) == "" {
		return BusinessAnalysis{}, errors.New("AI analysis response has no content")
	}
	return decodeBusinessAnalysis(completion.Choices[0].Message.Content)
}

func (c *analysisClient) completeJSON(ctx context.Context, systemPrompt, userContent string) (string, error) {
	requestBody, err := json.Marshal(map[string]any{
		"model":       c.model,
		"temperature": 0,
		"response_format": map[string]string{
			"type": "json_object",
		},
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userContent},
		},
	})
	if err != nil {
		return "", fmt.Errorf("encode AI request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL, bytes.NewReader(requestBody))
	if err != nil {
		return "", fmt.Errorf("create AI request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send AI request: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return "", fmt.Errorf("read AI response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("AI returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &completion); err != nil {
		return "", fmt.Errorf("decode AI response: %w", err)
	}
	if len(completion.Choices) == 0 || strings.TrimSpace(completion.Choices[0].Message.Content) == "" {
		return "", errors.New("AI response has no content")
	}
	return completion.Choices[0].Message.Content, nil
}

func decodeBusinessAnalysis(content string) (BusinessAnalysis, error) {
	var raw rawBusinessAnalysis
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return BusinessAnalysis{}, fmt.Errorf("AI analysis is not valid JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return BusinessAnalysis{}, errors.New("AI analysis must contain one JSON object")
	}
	if raw.BusinessSummary == nil || raw.BusinessType == nil || raw.RecommendedUseCase == nil || raw.PersonalisationLine == nil {
		return BusinessAnalysis{}, errors.New("AI analysis response is missing required fields")
	}
	return BusinessAnalysis{
		BusinessSummary:     *raw.BusinessSummary,
		BusinessType:        *raw.BusinessType,
		RecommendedUseCase:  *raw.RecommendedUseCase,
		PersonalisationLine: *raw.PersonalisationLine,
	}, nil
}

func saveBusinessAnalysis(ctx context.Context, cfg Config, prospectID string, analysis BusinessAnalysis) error {
	body, err := json.Marshal(map[string]any{
		"business_summary":     analysis.BusinessSummary,
		"business_type":        analysis.BusinessType,
		"recommended_use_case": analysis.RecommendedUseCase,
		"personalisation_line": analysis.PersonalisationLine,
		"analysed":             true,
	})
	if err != nil {
		return fmt.Errorf("encode business analysis: %w", err)
	}
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable) + "?id=eq." + url.QueryEscape(prospectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ScreenFizz analysis update request: %w", err)
	}
	setSupabaseHeaders(req, cfg)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "return=minimal")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("save ScreenFizz analysis: %w", err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return fmt.Errorf("read ScreenFizz analysis update response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("save ScreenFizz analysis: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}
	return nil
}
