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
		{name: "default", limit: 0, want: 500},
		{name: "cap", limit: 1000, want: 500},
		{name: "daily", limit: 500, want: 500},
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

func TestCampaignNotDueOnWeekend(t *testing.T) {
	t.Parallel()

	campaign := ScheduledCampaign{ScheduleTime: "09:00:00", Timezone: "Europe/London"}
	saturday := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	if campaignDue(saturday, campaign) {
		t.Fatal("campaign should not be due on Saturday")
	}
}

func TestParseScheduledCampaignDoesNotRequireApifyConfig(t *testing.T) {
	t.Parallel()

	campaign, err := parseScheduledCampaign(Campaign{
		"id":            "campaign-1",
		"name":          "Influocial",
		"enabled":       true,
		"schedule_time": "09:00:00",
		"timezone":      "Europe/London",
	})
	if err != nil {
		t.Fatalf("parseScheduledCampaign() error = %v", err)
	}
	if campaign.Name != "Influocial" {
		t.Fatalf("campaign.Name = %q, want Influocial", campaign.Name)
	}
}
