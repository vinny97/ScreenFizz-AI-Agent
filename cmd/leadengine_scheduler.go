package cmd

import (
	"context"
	"log/slog"
	"os"

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

func newLeadEngineWebhookHandler() *leadengine.BrevoWebhookHandler {
	client, err := leadengine.NewFromEnv()
	if err != nil {
		slog.Info("leadengine.webhooks.disabled", "reason", err)
		return nil
	}
	handler, err := leadengine.NewBrevoWebhookHandler(client, os.Getenv("BREVO_WEBHOOK_SECRET"))
	if err != nil {
		slog.Info("leadengine.webhooks.disabled", "reason", err)
		return nil
	}
	return handler
}
