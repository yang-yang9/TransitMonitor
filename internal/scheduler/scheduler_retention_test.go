package scheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"transitmonitor/internal/adapter"
	"transitmonitor/internal/alert"
	"transitmonitor/internal/changedet"
	"transitmonitor/internal/domain"
	"transitmonitor/internal/store"
)

// Backfills a test for the daily retention job: old obs deleted + aggregated,
// recent obs kept (real 7/30 defaults, not 0/0).

func TestRunRetentionDeletesOldObservations(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	ctx := context.Background()
	now := time.Now()
	old := now.AddDate(0, 0, -40)     // > 30d obs retention → deleted
	recent := now.Add(-1 * time.Hour) // within 30d → kept
	if err := st.InsertRatioObservations(ctx, []domain.RatioObservation{
		{StationID: "s1", GroupName: "default", ModelName: "gpt-4o", InputUSDPer1M: 2.5, ObservedAt: old},
		{StationID: "s1", GroupName: "default", ModelName: "gpt-4o", InputUSDPer1M: 3.0, ObservedAt: recent},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	ma := &mockAdapter{}
	sched := &Scheduler{
		Adapters: map[string]adapter.Adapter{"s1": ma},
		Store:    st, Notifier: &alert.SinkNotifier{},
		DiffCfg: changedet.DefaultConfig(), Now: func() time.Time { return now },
		snapshotRetentionDays: 7, obsRetentionDays: 30,
	}
	sched.runRetention(ctx)

	got, _ := st.LatestRatioObservations(ctx, "s1")
	if len(got) != 1 {
		t.Fatalf("want 1 (recent) remaining, got %d: %+v", len(got), got)
	}
	if got[0].InputUSDPer1M != 3.0 {
		t.Errorf("kept obs input: want 3.0 got %v", got[0].InputUSDPer1M)
	}
}
