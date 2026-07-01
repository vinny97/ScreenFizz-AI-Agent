package leadengine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveDatasetUsesCampaignAndUTCTimestamp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	job := &Job{
		OutputDir: dir,
		Now: func() time.Time {
			return time.Date(2026, 7, 1, 13, 0, 0, 0, time.FixedZone("BST", 3600))
		},
	}
	path, err := job.saveDataset("Influocial UK / Retail", json.RawMessage(`[{"email":"lead@example.com"}]`))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "Influocial UK - Retail-2026-07-01-12-00.json")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestNextDailyRun(t *testing.T) {
	t.Parallel()
	tests := []struct {
		now  time.Time
		want time.Time
	}{
		{time.Date(2026, 7, 1, 11, 59, 0, 0, time.UTC), time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)},
		{time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC), time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)},
	}
	for _, test := range tests {
		if got := nextDailyRun(test.now); !got.Equal(test.want) {
			t.Errorf("nextDailyRun(%s) = %s, want %s", test.now, got, test.want)
		}
	}
}
