package leadengine

import (
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

const aiReviewBatchSize = 10

// StartAIReviewBatchScheduler creates one small review batch only when the
// previous batch has been completely reviewed. Pending-review drafts are never
// auto-approved by the sending pipeline.
func StartAIReviewBatchScheduler(ctx context.Context, cfg Config) {
	go func() {
		tick := func() {
			created, err := GenerateAIReviewBatch(ctx, cfg, aiReviewBatchSize)
			if err != nil {
				slog.Error("screenfizz.ai_review_batch.failed", "error", err)
				return
			}
			if created > 0 {
				slog.Info("screenfizz.ai_review_batch.created", "count", created)
			}
		}
		tick()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				tick()
			}
		}
	}()
}

// GenerateAIReviewBatch creates no more than limit AI-personalised drafts for
// review. It will not create another batch while any pending-review draft
// remains.
func GenerateAIReviewBatch(ctx context.Context, cfg Config, limit int) (int, error) {
	if limit <= 0 {
		limit = aiReviewBatchSize
	}
	pending, err := countProspectsByStatus(ctx, cfg, "pending_review")
	if err != nil {
		return 0, err
	}
	if pending > 0 {
		return 0, nil
	}
	client, err := newAnalysisClient(cfg)
	if err != nil {
		return 0, err
	}
	prospects, err := nextAIPendingEmailProspects(ctx, cfg, limit)
	if err != nil {
		return 0, err
	}
	created := 0
	for _, prospect := range prospects {
		email, err := client.generateReviewEmail(ctx, prospect)
		if err != nil {
			slog.Error("screenfizz.ai_review_batch.email_failed", "prospect_id", prospect.ID, "error", err)
			continue
		}
		if err := saveGeneratedEmailWithStatus(ctx, cfg, prospect.ID, email, "pending_review"); err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}

func (c *analysisClient) generateReviewEmail(ctx context.Context, prospect emailProspect) (GeneratedEmail, error) {
	input, err := json.Marshal(map[string]string{
		"business_name": prospect.Business.BusinessName, "category": prospect.Business.Category,
		"business_summary": prospect.BusinessSummary, "business_type": prospect.BusinessType,
		"recommended_use_case": prospect.RecommendedUseCase, "personalisation_line": prospect.PersonalisationLine,
	})
	if err != nil {
		return GeneratedEmail{}, fmt.Errorf("encode AI review email input: %w", err)
	}
	content, err := c.completeJSON(ctx, fmt.Sprintf(`Return only JSON with subject and email_body. Subject must be exactly %q. Write in British English, naturally and professionally. Use this as the foundation, retain the managed service, WhatsApp support, £15 per month per screen, and free mock-up CTA, but personalise it from the supplied analysis. Use no em dashes. Do not invent facts.\n\n%s`, screenFizzEmailSubject, screenFizzEmailBase), string(input))
	if err != nil {
		return GeneratedEmail{}, err
	}
	email, err := decodeGeneratedEmail(content)
	if err != nil {
		return GeneratedEmail{}, err
	}
	email.Subject = screenFizzEmailSubject
	email.Body = toBritishEnglish(removeEmailEmDashes(email.Body))
	return email, nil
}

func nextAIPendingEmailProspects(ctx context.Context, cfg Config, limit int) ([]emailProspect, error) {
	prospects, err := nextEmailProspects(ctx, cfg, limit)
	if err != nil {
		return nil, err
	}
	return prospects, nil
}

func countProspectsByStatus(ctx context.Context, cfg Config, status string) (int, error) {
	endpoint := fmt.Sprintf("%s/rest/v1/%s", strings.TrimRight(cfg.SupabaseURL, "/"), url.PathEscape(cfg.ProspectsTable))
	query := url.Values{"select": {"id"}, "status": {"eq." + status}, "limit": {"1"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return 0, err
	}
	setSupabaseHeaders(req, cfg)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	body, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("list pending AI review prospects: %s", strings.TrimSpace(string(body)))
	}
	var rows []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		return 0, err
	}
	return len(rows), nil
}

func saveGeneratedEmailWithStatus(ctx context.Context, cfg Config, prospectID string, email GeneratedEmail, status string) error {
	return saveGeneratedEmailStatus(ctx, cfg, prospectID, email, status)
}
