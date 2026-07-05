package leadengine

import (
	"testing"
	"time"
)

func TestInsideSendWindow(t *testing.T) {
	t.Parallel()
	location, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		time time.Time
		want bool
	}{
		{name: "weekday start", time: time.Date(2026, 7, 6, 9, 0, 0, 0, location), want: true},
		{name: "weekday before", time: time.Date(2026, 7, 6, 8, 59, 59, 0, location), want: false},
		{name: "weekday end", time: time.Date(2026, 7, 6, 16, 0, 0, 0, location), want: false},
		{name: "saturday", time: time.Date(2026, 7, 4, 12, 0, 0, 0, location), want: false},
		{name: "sunday", time: time.Date(2026, 7, 5, 12, 0, 0, 0, location), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := insideSendWindow(tt.time, 9, 16); got != tt.want {
				t.Fatalf("insideSendWindow() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultSendSchedule(t *testing.T) {
	t.Parallel()
	schedule := DefaultSendSchedule()
	if schedule.DailyLimit != 500 || schedule.HourlyLimit != 72 {
		t.Fatalf("limits = daily %d hourly %d", schedule.DailyLimit, schedule.HourlyLimit)
	}
	if schedule.MinDelay != 20*time.Second || schedule.MaxDelay != 60*time.Second {
		t.Fatalf("delay = %s-%s", schedule.MinDelay, schedule.MaxDelay)
	}
	if schedule.MinHourlyPause != 2*time.Minute || schedule.MaxHourlyPause != 5*time.Minute {
		t.Fatalf("pause = %s-%s", schedule.MinHourlyPause, schedule.MaxHourlyPause)
	}
}
