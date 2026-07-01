package leadengine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// Job runs the complete scheduled Lead Engine workflow.
type Job struct {
	Supabase  *Client
	Apify     *ApifyClient
	OutputDir string
	Now       func() time.Time
}

// JobResult describes a completed Lead Engine job.
type JobResult struct {
	FilePath string
	Import   ImportResult
}

// NewJobFromEnv creates a Lead Engine job from the configured environment.
func NewJobFromEnv(outputDir string) (*Job, error) {
	supabase, err := NewFromEnv()
	if err != nil {
		return nil, err
	}
	apify, err := NewApifyClientFromEnv()
	if err != nil {
		return nil, err
	}
	return &Job{Supabase: supabase, Apify: apify, OutputDir: outputDir, Now: time.Now}, nil
}

// Run reads the active campaign, runs Apify, saves the dataset, then imports it.
func (j *Job) Run(ctx context.Context) (JobResult, error) {
	slog.Info("leadengine.job.started")
	campaign, err := j.Supabase.GetActiveCampaign(ctx)
	if err != nil {
		slog.Error("leadengine.job.campaign_failed", "error", err)
		return JobResult{}, err
	}
	slog.Info("leadengine.job.campaign_loaded", "campaign", campaign.Name)

	slog.Info("leadengine.job.apify_starting", "campaign", campaign.Name)
	dataset, err := j.Apify.Run(ctx, campaign)
	if err != nil {
		slog.Error("leadengine.job.apify_failed", "campaign", campaign.Name, "error", err)
		return JobResult{}, err
	}
	slog.Info("leadengine.job.apify_succeeded", "campaign", campaign.Name, "bytes", len(dataset))

	filePath, err := j.saveDataset(campaign.Name, dataset)
	if err != nil {
		slog.Error("leadengine.job.save_failed", "campaign", campaign.Name, "error", err)
		return JobResult{}, err
	}
	slog.Info("leadengine.job.dataset_saved", "campaign", campaign.Name, "path", filePath)

	slog.Info("leadengine.job.import_starting", "campaign", campaign.Name, "path", filePath)
	result, err := j.Supabase.ImportLeads(ctx, campaign.Name, dataset)
	if err != nil {
		slog.Error("leadengine.job.import_failed", "campaign", campaign.Name, "path", filePath, "error", err)
		return JobResult{FilePath: filePath}, err
	}
	slog.Info("leadengine.job.completed", "campaign", campaign.Name, "path", filePath,
		"imported", result.Imported, "skipped", result.Skipped)
	return JobResult{FilePath: filePath, Import: result}, nil
}

func (j *Job) saveDataset(campaignName string, dataset json.RawMessage) (string, error) {
	if !json.Valid(dataset) {
		return "", fmt.Errorf("Apify dataset is not valid JSON")
	}
	if err := os.MkdirAll(j.OutputDir, 0755); err != nil {
		return "", fmt.Errorf("create leads directory: %w", err)
	}
	now := time.Now()
	if j.Now != nil {
		now = j.Now()
	}
	filename := fmt.Sprintf("%s-%s.json", safeCampaignFilename(campaignName), now.UTC().Format("2006-01-02-15-04"))
	path := filepath.Join(j.OutputDir, filename)
	if err := os.WriteFile(path, dataset, 0600); err != nil {
		return "", fmt.Errorf("write leads dataset: %w", err)
	}
	return path, nil
}

func safeCampaignFilename(name string) string {
	name = strings.TrimSpace(name)
	return strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\':
			return '-'
		case unicode.IsControl(r):
			return -1
		default:
			return r
		}
	}, name)
}
