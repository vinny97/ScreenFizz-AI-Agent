package cmd

import (
	"context"
	"log/slog"

	screenfizz "github.com/nextlevelbuilder/goclaw/internal/screenfizz/leadengine"
)

func startScreenFizzScheduler(ctx context.Context) {
	scheduler, err := screenfizz.NewDailySchedulerFromEnv()
	if err != nil {
		slog.Info("screenfizz.scheduler.disabled", "reason", err)
		return
	}
	scheduler.Start(ctx)
}
