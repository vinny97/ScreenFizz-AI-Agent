package leadengine

import (
	"testing"
	"time"
)

func TestDailyLeadTarget(t *testing.T) {
	if got := dailyLeadTarget(Config{}); got != defaultDailyLeadTarget {
		t.Fatalf("dailyLeadTarget(Config{}) = %d, want %d", got, defaultDailyLeadTarget)
	}
	if got := dailyLeadTarget(Config{DailyLeadTarget: 125}); got != 125 {
		t.Fatalf("dailyLeadTarget custom value = %d, want 125", got)
	}
}

func TestDailyRunUsesUKTime(t *testing.T) {
	utcEightAM := time.Date(2026, time.January, 15, 8, 0, 0, 0, time.UTC)
	if !isDailyRunHour(utcEightAM.In(londonLocation)) {
		t.Fatal("08:00 UK in winter should be due")
	}

	utcSevenAM := time.Date(2026, time.July, 15, 7, 0, 0, 0, time.UTC)
	if !isDailyRunHour(utcSevenAM.In(londonLocation)) {
		t.Fatal("07:00 UTC should be 08:00 UK in summer and due")
	}

	utcEightAMSummer := time.Date(2026, time.July, 15, 8, 0, 0, 0, time.UTC)
	if isDailyRunHour(utcEightAMSummer.In(londonLocation)) {
		t.Fatal("08:00 UTC should be 09:00 UK in summer and not due")
	}
}
