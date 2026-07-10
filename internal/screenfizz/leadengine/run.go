package leadengine

import (
	"context"
	"fmt"
	"log/slog"
)

// Runner is the ScreenFizz Lead Engine entry point.
//
// It intentionally does not reuse internal/leadengine so ScreenFizz can have
// its own configuration, prompts, and Supabase tables.
type Runner struct {
	Config Config
}

type RunResult struct {
	TotalReturned     int
	Inserted          int
	NoWebsiteSkipped  int
	NoEmailSkipped    int
	ClosedSkipped     int
	DuplicatesSkipped int
	ProspectsAdded    int
	ProspectsSkipped  int
}

func NewRunner(cfg Config) *Runner {
	return &Runner{Config: cfg}
}

func NewRunnerFromEnv() (*Runner, error) {
	cfg, err := ConfigFromEnv()
	if err != nil {
		return nil, err
	}
	return NewRunner(cfg), nil
}

func (r *Runner) Run(ctx context.Context) (RunResult, error) {
	return r.runEnabledAreas(ctx, DefaultApifyCampaign)
}

// RunTest runs the small actor input once for every enabled search area.
func (r *Runner) RunTest(ctx context.Context) (RunResult, error) {
	return r.runEnabledAreas(ctx, TestApifyCampaign)
}

type campaignFactory func(Config, string) (ApifyCampaign, error)

func (r *Runner) runEnabledAreas(ctx context.Context, newCampaign campaignFactory) (RunResult, error) {
	counties, err := LoadEnabledSearchAreas(ctx, r.Config)
	if err != nil {
		return RunResult{}, err
	}
	var total RunResult
	for _, county := range counties {
		slog.Info("screenfizz.leadengine.search_area_started", "county", county)
		campaign, err := newCampaign(r.Config, county)
		if err != nil {
			return RunResult{}, err
		}
		result, err := r.RunCampaign(ctx, campaign)
		if err != nil {
			return RunResult{}, fmt.Errorf("run ScreenFizz search area %q: %w", county, err)
		}
		total = addRunResult(total, result)
	}
	return total, nil
}

func addRunResult(total RunResult, result RunResult) RunResult {
	total.TotalReturned += result.TotalReturned
	total.Inserted += result.Inserted
	total.NoWebsiteSkipped += result.NoWebsiteSkipped
	total.NoEmailSkipped += result.NoEmailSkipped
	total.ClosedSkipped += result.ClosedSkipped
	total.DuplicatesSkipped += result.DuplicatesSkipped
	total.ProspectsAdded += result.ProspectsAdded
	total.ProspectsSkipped += result.ProspectsSkipped
	return total
}

// RunCampaign runs an actor campaign and imports its returned businesses.
func (r *Runner) RunCampaign(ctx context.Context, campaign ApifyCampaign) (RunResult, error) {
	if err := ctx.Err(); err != nil {
		return RunResult{}, err
	}
	apify, err := NewApifyClient(r.Config)
	if err != nil {
		return RunResult{}, err
	}
	dataset, err := apify.Run(ctx, campaign)
	if err != nil {
		return RunResult{}, err
	}
	return r.importDataset(ctx, dataset)
}

// ImportCompletedRun resumes importing a completed Apify run. It does not
// start another actor run.
func (r *Runner) ImportCompletedRun(ctx context.Context, runID string) (RunResult, error) {
	if err := ctx.Err(); err != nil {
		return RunResult{}, err
	}
	apify, err := NewApifyClient(r.Config)
	if err != nil {
		return RunResult{}, err
	}
	dataset, err := apify.DownloadRunDataset(ctx, r.Config.ApifyAPIURL, runID)
	if err != nil {
		return RunResult{}, err
	}
	return r.importDataset(ctx, dataset)
}

func (r *Runner) importDataset(ctx context.Context, dataset []byte) (RunResult, error) {
	businesses, err := DecodeBusinesses(dataset)
	if err != nil {
		return RunResult{}, err
	}
	slog.Info("screenfizz.leadengine.businesses_returned", "total", len(businesses))

	importer := NewImporter(r.Config)
	result, err := importer.ImportDataset(ctx, dataset)
	if err != nil {
		return RunResult{}, err
	}
	slog.Info("screenfizz.leadengine.import_completed",
		"inserted", result.Imported,
		"no_website_skipped", result.NoWebsiteSkipped,
		"no_email_skipped", result.NoEmailSkipped,
		"closed_skipped", result.ClosedSkipped,
		"duplicates_skipped", result.DuplicatesSkipped,
		"prospects_added", result.ProspectsAdded,
		"prospects_skipped", result.ProspectsSkipped,
	)
	return RunResult{
		TotalReturned:     len(businesses),
		Inserted:          result.Imported,
		NoWebsiteSkipped:  result.NoWebsiteSkipped,
		NoEmailSkipped:    result.NoEmailSkipped,
		ClosedSkipped:     result.ClosedSkipped,
		DuplicatesSkipped: result.DuplicatesSkipped,
		ProspectsAdded:    result.ProspectsAdded,
		ProspectsSkipped:  result.ProspectsSkipped,
	}, nil
}
