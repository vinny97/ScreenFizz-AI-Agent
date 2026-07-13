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

// PipelineResult reports the import and optional sending outcome of a full
// ScreenFizz scheduled sync.
type PipelineResult struct {
	Import       RunResult
	AutoApproved int
}

// RunPipeline executes the complete ScreenFizz lead flow. Generated drafts
// are always stored first as ready_to_send. Sending happens only when
// SCREENFIZZ_AUTO_APPROVE is enabled.
func RunPipeline(ctx context.Context, cfg Config, campaign ApifyCampaign) (PipelineResult, error) {
	return runPipeline(ctx, cfg, func(ctx context.Context) (RunResult, error) {
		return NewRunner(cfg).RunCampaign(ctx, campaign)
	})
}

// RunPipelineFromCompletedRun resumes the production pipeline from an Apify
// dataset that has already finished. It does not start another actor run.
func RunPipelineFromCompletedRun(ctx context.Context, cfg Config, runID string) (PipelineResult, error) {
	return runPipeline(ctx, cfg, func(ctx context.Context) (RunResult, error) {
		return NewRunner(cfg).ImportCompletedRun(ctx, runID)
	})
}

func runPipeline(ctx context.Context, cfg Config, importRun func(context.Context) (RunResult, error)) (PipelineResult, error) {
	result := PipelineResult{}
	slog.Info("screenfizz.pipeline.stage_started", "stage", "import")
	imported, err := importRun(ctx)
	if err != nil {
		return result, fmt.Errorf("import businesses: %w", err)
	}
	result.Import = imported
	slog.Info("screenfizz.pipeline.stage_completed", "stage", "import", "inserted", imported.Inserted)

	for _, stage := range []struct {
		name string
		run  func(context.Context, Config) error
	}{
		{"download_website", func(ctx context.Context, cfg Config) error {
			return EnrichProspectsUpTo(ctx, cfg, cfg.PipelineBatchSize)
		}},
		{"extract_text", func(ctx context.Context, cfg Config) error {
			return ParseProspectsUpTo(ctx, cfg, cfg.PipelineBatchSize)
		}},
		{"analyse", func(ctx context.Context, cfg Config) error {
			return AnalyseProspectsUpTo(ctx, cfg, cfg.PipelineBatchSize)
		}},
		{"generate_email_draft", func(ctx context.Context, cfg Config) error {
			return GenerateProspectEmailsUpTo(ctx, cfg, cfg.PipelineBatchSize)
		}},
	} {
		slog.Info("screenfizz.pipeline.stage_started", "stage", stage.name)
		if err := stage.run(ctx, cfg); err != nil {
			return result, fmt.Errorf("%s: %w", stage.name, err)
		}
		slog.Info("screenfizz.pipeline.stage_completed", "stage", stage.name)
	}

	if strings.TrimSpace(cfg.BrevoAPIKey) != "" {
		slog.Info("screenfizz.pipeline.stage_started", "stage", "sync_brevo_contacts")
		if err := SyncBrevoContacts(ctx, cfg); err != nil {
			return result, fmt.Errorf("sync Brevo contacts: %w", err)
		}
		slog.Info("screenfizz.pipeline.stage_completed", "stage", "sync_brevo_contacts")
	}

	if !cfg.AutoApprove {
		slog.Info("screenfizz.pipeline.ready_for_review")
		return result, nil
	}
	if strings.TrimSpace(cfg.BrevoAPIKey) == "" || strings.TrimSpace(cfg.BrevoSenderEmail) == "" {
		return result, fmt.Errorf("auto-approve requires SCREENFIZZ_BREVO_API_KEY and SCREENFIZZ_SENDER_EMAIL")
	}

	approved, err := ApproveReadyToSendProspects(ctx, cfg)
	if err != nil {
		return result, err
	}
	result.AutoApproved = approved
	slog.Info("screenfizz.pipeline.stage_completed", "stage", "auto_approve", "approved", approved)

	return result, nil
}

// ApproveReadyToSendProspects advances only newly generated drafts. Existing
// legacy pending_review drafts are deliberately left for manual review.
func ApproveReadyToSendProspects(ctx context.Context, cfg Config) (int, error) {
	body, err := json.Marshal(map[string]string{"status": "approved"})
	if err != nil {
		return 0, fmt.Errorf("encode ScreenFizz auto-approval: %w", err)
	}
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable) + "?status=eq.ready_to_send"
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create ScreenFizz auto-approval request: %w", err)
	}
	setSupabaseHeaders(req, cfg)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "return=representation")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, fmt.Errorf("approve ScreenFizz drafts: %w", err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return 0, fmt.Errorf("read ScreenFizz auto-approval response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return 0, fmt.Errorf("approve ScreenFizz drafts: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}
	var updated []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(responseBody, &updated); err != nil {
		return 0, fmt.Errorf("decode ScreenFizz auto-approval response: %w", err)
	}
	return len(updated), nil
}
