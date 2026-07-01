package cmd

import (
	"context"
	"log/slog"

	"github.com/nextlevelbuilder/goclaw/internal/leadengine"
)

func startLeadEngineScheduler(ctx context.Context) {
	scheduler, err := leadengine.NewCampaignSchedulerFromEnv("data/leads")
	if err != nil {
		slog.Info("leadengine.scheduler.disabled", "reason", err)
		return
	}
	scheduler.Start(ctx)
}
