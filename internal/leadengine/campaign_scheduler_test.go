package leadengine

import (
	"testing"
	"time"
)

func TestCampaignDueWhenMissedToday(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 2, 10, 15, 0, 0, time.UTC)
	campaign := ScheduledCampaign{
		Name:         "Influocial",
		Enabled:      true,
		ScheduleTime: "10:00:00",
		Timezone:     "UTC",
	}
	if !campaignDue(now, campaign) {
		t.Fatal("campaign should be due after a missed run today")
	}
}

func TestCampaignNotDueBeforeSchedule(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 2, 9, 58, 0, 0, time.UTC)
	campaign := ScheduledCampaign{
		Name:         "Influocial",
		Enabled:      true,
		ScheduleTime: "10:00:00",
		Timezone:     "UTC",
	}
	if campaignDue(now, campaign) {
		t.Fatal("campaign should not be due before the schedule time")
	}
}

func TestCampaignNotDueAfterAlreadyRunningToday(t *testing.T) {
	t.Parallel()

	lastRun := time.Date(2026, 7, 2, 10, 1, 0, 0, time.UTC)
	now := time.Date(2026, 7, 2, 10, 15, 0, 0, time.UTC)
	campaign := ScheduledCampaign{
		Name:         "Influocial",
		Enabled:      true,
		ScheduleTime: "10:00:00",
		Timezone:     "UTC",
		LastRunAt:    &lastRun,
	}
	if campaignDue(now, campaign) {
		t.Fatal("campaign should not be due after already running today")
	}
}

func TestSendLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{name: "default", limit: 0, want: 100},
		{name: "cap", limit: 500, want: 100},
		{name: "custom", limit: 25, want: 25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			campaign := ScheduledCampaign{DailyLimit: tt.limit}
			if got := campaign.SendLimit(); got != tt.want {
				t.Fatalf("SendLimit() = %d, want %d", got, tt.want)
			}
		})
	}
}
