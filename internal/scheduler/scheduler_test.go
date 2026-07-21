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

// mockAdapter returns a canned observation set, so the scheduler's
// probe→fetch→store→diff→alert wiring is tested with zero real I/O.
type mockAdapter struct {
	obs []domain.RatioObservation
}

func (m *mockAdapter) ProbeCapabilities(_ context.Context) (domain.CapabilityReport, error) {
	return domain.CapabilityReport{}, nil
}

func (m *mockAdapter) FetchRatios(_ context.Context, _ domain.CapabilityReport) (domain.RawSnapshot, []domain.RatioObservation, error) {
	out := make([]domain.RatioObservation, len(m.obs))
	copy(out, m.obs)
	return domain.RawSnapshot{}, out, nil
}

func newSched(t *testing.T, ma *mockAdapter) (*Scheduler, *store.Store, *alert.SinkNotifier) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	sink := &alert.SinkNotifier{}
	var tick int
	sched := &Scheduler{
		Adapters: map[string]adapter.Adapter{"s1": ma},
		Store:    st,
		Rules:    []alert.Rule{{Name: "delta5", Type: alert.RuleDeltaPct, Threshold: 5, Enabled: true}},
		Notifier: sink,
		DiffCfg:  changedet.DefaultConfig(),
		Now: func() time.Time {
			tick++
			return time.Date(2026, 7, 21, 12, tick, 0, 0, time.UTC)
		},
	}
	return sched, st, sink
}

func TestPollOnce_ChangeAndAlert(t *testing.T) {
	ma := &mockAdapter{obs: []domain.RatioObservation{{
		StationID: "s1", GroupName: "default", ModelName: "gpt-4o",
		InputUSDPer1M: 2.5, NativeRatio: 1.25,
	}}}
	sched, st, sink := newSched(t, ma)
	defer st.Close()
	ctx := context.Background()

	// First poll: station is new → model_added event (no delta rule matches → no alert).
	if err := sched.PollOnce(ctx, "s1"); err != nil {
		t.Fatalf("poll1: %v", err)
	}
	if got := len(sink.Sent); got != 0 {
		t.Errorf("after poll1: want 0 alerts (no model_added rule), got %d", got)
	}

	// Second poll: price 2.5 → 3.0 (20%, critical) → delta alert fires.
	ma.obs[0].InputUSDPer1M = 3.0
	if err := sched.PollOnce(ctx, "s1"); err != nil {
		t.Fatalf("poll2: %v", err)
	}

	latest, _ := st.LatestRatioObservations(ctx, "s1")
	if len(latest) != 1 || latest[0].InputUSDPer1M != 3.0 {
		t.Errorf("latest stored input: want 3.0, got %+v", latest)
	}
	evs, _ := st.ListChangeEvents(ctx, "s1", 10)
	if len(evs) == 0 {
		t.Error("want change events recorded, got 0")
	}
	if len(sink.Sent) != 1 {
		t.Errorf("want 1 delta alert after poll2, got %d", len(sink.Sent))
	}
}
