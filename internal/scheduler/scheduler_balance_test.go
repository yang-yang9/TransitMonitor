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

// balanceMockAdapter returns a configurable CapabilityReport carrying a balance
// reading, so the scheduler's balance store + quota_below / quota_drop_pct
// emission is tested with zero real I/O.
type balanceMockAdapter struct {
	caps domain.CapabilityReport
}

func (m *balanceMockAdapter) ProbeCapabilities(_ context.Context) (domain.CapabilityReport, error) {
	return m.caps, nil
}

func (m *balanceMockAdapter) FetchRatios(_ context.Context, caps domain.CapabilityReport) (domain.RawSnapshot, []domain.RatioObservation, error) {
	return domain.RawSnapshot{}, nil, nil
}

func newBalanceSched(t *testing.T, ma *balanceMockAdapter) (*Scheduler, *store.Store, *alert.SinkNotifier) {
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
		Rules: []alert.Rule{
			{Name: "low", Type: alert.RuleQuotaBelow, Threshold: 1, Enabled: true},
			{Name: "drop", Type: alert.RuleQuotaDropPct, Threshold: 20, Enabled: true},
		},
		Notifier:   sink,
		DiffCfg:    changedet.DefaultConfig(),
		lastAlert:  map[string]time.Time{},
		failStreak: map[string]int{},
		authOK:     map[string]bool{},
		Now: func() time.Time {
			tick++
			return time.Date(2026, 7, 21, 12, tick, 0, 0, time.UTC)
		},
	}
	// cooldown=0 → every qualifying alert fires (no dedup); the scheduler default
	// is 30m, which would suppress the second alert in the same test minute.
	sched.SetCooldown(0)
	return sched, st, sink
}

// TestPollOnce_BalanceStoredAndLowAlert: a sub2api-style reading (USD) below the
// quota_below threshold fires the low alert and is persisted as a time-series row.
func TestPollOnce_BalanceStoredAndLowAlert(t *testing.T) {
	ma := &balanceMockAdapter{caps: domain.CapabilityReport{
		StationID: "s1", Kind: domain.KindSub2API, HasQuota: true,
		QuotaRemaining: 0.50, // $0.50 < $1 threshold → low alert
	}}
	sched, st, sink := newBalanceSched(t, ma)
	defer st.Close()
	ctx := context.Background()

	if err := sched.PollOnce(ctx, "s1"); err != nil {
		t.Fatalf("poll1: %v", err)
	}
	// Persisted as a balance observation (USD, no QuotaPerUnit conversion).
	got, err := st.LatestBalance(ctx, "s1")
	if err != nil {
		t.Fatalf("latest balance: %v", err)
	}
	if got.RemainingUSD != 0.50 || got.Currency != "USD" {
		t.Errorf("stored balance wrong: %+v", got)
	}
	// quota_below fired exactly once.
	n := 0
	for _, ev := range sink.Sent {
		if ev.Rule == "low" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("want 1 low alert, got %d", n)
	}
}

// TestPollOnce_BalanceDropPctAlert: a ≥20% drop between polls fires the drop
// alert; the low alert stays silent because remaining is still above $1.
func TestPollOnce_BalanceDropPctAlert(t *testing.T) {
	ma := &balanceMockAdapter{caps: domain.CapabilityReport{
		StationID: "s1", Kind: domain.KindSub2API, HasQuota: true,
		QuotaRemaining: 10.0,
	}}
	sched, st, sink := newBalanceSched(t, ma)
	defer st.Close()
	ctx := context.Background()

	if err := sched.PollOnce(ctx, "s1"); err != nil {
		t.Fatalf("poll1: %v", err)
	}
	if len(sink.Sent) != 0 {
		t.Fatalf("poll1: want 0 alerts, got %d", len(sink.Sent))
	}
	// Drop $10 → $5 (50% ≥ 20% threshold) → drop alert; still above $1 → no low.
	ma.caps.QuotaRemaining = 5.0
	if err := sched.PollOnce(ctx, "s1"); err != nil {
		t.Fatalf("poll2: %v", err)
	}
	nLow, nDrop := 0, 0
	for _, ev := range sink.Sent {
		switch ev.Rule {
		case "low":
			nLow++
		case "drop":
			nDrop++
		}
	}
	if nLow != 0 {
		t.Errorf("want 0 low alerts (still above $1), got %d", nLow)
	}
	if nDrop != 1 {
		t.Errorf("want 1 drop alert, got %d", nDrop)
	}
}
