package scheduler

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"transitmonitor/internal/adapter"
	"transitmonitor/internal/alert"
	"transitmonitor/internal/changedet"
	"transitmonitor/internal/domain"
	"transitmonitor/internal/store"
)

// digestHasRule reports whether sink received a digest message mentioning the
// given rule name (alerts are delivered as per-station digests).
func digestHasRule(sink *alert.SinkNotifier, rule string) int {
	n := 0
	needle := "规则「" + rule + "」"
	for _, ev := range sink.Sent {
		if strings.Contains(ev.Message, needle) {
			n++
		}
	}
	return n
}

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
		Adapters:   map[string]adapter.Adapter{"s1": ma},
		Store:      st,
		Rules:      []alert.Rule{{Name: "delta5", Type: alert.RuleDeltaPct, Threshold: 5, Enabled: true}},
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

// failAdapter's FetchRatios returns a configurable error (used to test
// endpoint_auth_failed + poll_failure_streak emission with zero real I/O).
type failAdapter struct {
	err error
}

func (m *failAdapter) ProbeCapabilities(_ context.Context) (domain.CapabilityReport, error) {
	return domain.CapabilityReport{}, nil
}
func (m *failAdapter) FetchRatios(_ context.Context, _ domain.CapabilityReport) (domain.RawSnapshot, []domain.RatioObservation, error) {
	return domain.RawSnapshot{}, nil, m.err
}

// newFailSched builds a scheduler wired with a failing adapter and the two
// direct-emit alert rules. failStreak/authOK maps are initialized (newSched
// bypasses New, so the maps must be set up explicitly).
func newFailSched(t *testing.T, fa *failAdapter, rules []alert.Rule) (*Scheduler, *store.Store, *alert.SinkNotifier) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	sink := &alert.SinkNotifier{}
	var tick int
	sched := &Scheduler{
		Adapters:   map[string]adapter.Adapter{"s1": fa},
		Store:      st,
		Rules:      rules,
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
	return sched, st, sink
}

// TestPollOnce_PollFailureStreak fires only when the streak equals the rule
// threshold (once per crossing), and resets after a success.
func TestPollOnce_PollFailureStreak(t *testing.T) {
	fa := &failAdapter{err: fmt.Errorf("network boom")}
	rules := []alert.Rule{{Name: "streak3", Type: alert.RulePollFailureStreak, Threshold: 3, Enabled: true}}
	sched, st, sink := newFailSched(t, fa, rules)
	defer st.Close()
	ctx := context.Background()

	// 3 consecutive failures → fire on the 3rd (streak==threshold).
	for i := 1; i <= 3; i++ {
		if err := sched.PollOnce(ctx, "s1"); err == nil {
			t.Fatalf("poll %d: want error, got nil", i)
		}
	}
	if len(sink.Sent) != 1 {
		t.Fatalf("after 3 failures: want 1 streak alert, got %d", len(sink.Sent))
	}
	if sink.Sent[0].Rule != "告警汇总" {
		t.Errorf("want digest rule, got %s", sink.Sent[0].Rule)
	}
	if digestHasRule(sink, "streak3") != 1 {
		t.Errorf("digest should mention streak3, got %d", digestHasRule(sink, "streak3"))
	}

	// A 4th failure does NOT refire (streak 4 != threshold 3).
	if err := sched.PollOnce(ctx, "s1"); err == nil {
		t.Fatal("poll 4: want error")
	}
	if len(sink.Sent) != 1 {
		t.Errorf("4th failure should not refire: want 1, got %d", len(sink.Sent))
	}

	// Recovery: a successful poll resets the streak.
	// Swap to a returning-obs adapter for one success.
	sched.Adapters["s1"] = &mockAdapter{obs: []domain.RatioObservation{{StationID: "s1", GroupName: "default", ModelName: "m", InputUSDPer1M: 2.0}}}
	if err := sched.PollOnce(ctx, "s1"); err != nil {
		t.Fatalf("recovery poll: %v", err)
	}
	// Now fail again: streak must rebuild from 0, so 1 failure (streak 1) does
	// not fire; 2 more (streak 3) fire again.
	fa.err = fmt.Errorf("network: %w", domain.ErrAuthFailed) // any error counts for streak
	sched.Adapters["s1"] = fa
	for i := 1; i <= 2; i++ {
		_ = sched.PollOnce(ctx, "s1")
	}
	if len(sink.Sent) != 1 { // still 1 (streak 2 < 3)
		t.Errorf("post-reset streak 2 must not fire: want 1, got %d", len(sink.Sent))
	}
	_ = sched.PollOnce(ctx, "s1") // streak 3
	if len(sink.Sent) != 2 {
		t.Errorf("post-reset streak 3 must fire: want 2 total, got %d", len(sink.Sent))
	}
}

// TestPollOnce_DisabledStreakRule never fires even after many failures.
func TestPollOnce_DisabledStreakRule(t *testing.T) {
	fa := &failAdapter{err: fmt.Errorf("boom")}
	rules := []alert.Rule{{Name: "streak2", Type: alert.RulePollFailureStreak, Threshold: 2, Enabled: false}}
	sched, st, sink := newFailSched(t, fa, rules)
	defer st.Close()
	for i := 0; i < 5; i++ {
		_ = sched.PollOnce(context.Background(), "s1")
	}
	if len(sink.Sent) != 0 {
		t.Errorf("disabled streak rule must not fire, got %d", len(sink.Sent))
	}
}

// TestPollOnce_EndpointAuthFailed fires once on the OK→failed auth flip,
// not on every failed poll, and is suppressed by cooldown on repeats.
func TestPollOnce_EndpointAuthFailed(t *testing.T) {
	fa := &failAdapter{}
	rules := []alert.Rule{{Name: "authfail", Type: alert.RuleEndpointAuthFail, Enabled: true}}
	sched, st, sink := newFailSched(t, fa, rules)
	defer st.Close()
	ctx := context.Background()

	// Establish auth-OK state via a successful poll first.
	sched.Adapters["s1"] = &mockAdapter{obs: []domain.RatioObservation{{StationID: "s1", GroupName: "default", ModelName: "m", InputUSDPer1M: 2.0}}}
	if err := sched.PollOnce(ctx, "s1"); err != nil {
		t.Fatalf("setup success poll: %v", err)
	}

	// Now flip to an auth failure.
	fa.err = fmt.Errorf("pricing: status 401: %w", domain.ErrAuthFailed)
	sched.Adapters["s1"] = fa
	if err := sched.PollOnce(ctx, "s1"); err == nil {
		t.Fatal("want auth-fail error")
	}
	if len(sink.Sent) != 1 {
		t.Fatalf("OK→failed flip: want 1 auth alert, got %d", len(sink.Sent))
	}
	if sink.Sent[0].Rule != "告警汇总" {
		t.Errorf("want digest rule, got %s", sink.Sent[0].Rule)
	}
	if digestHasRule(sink, "authfail") != 1 {
		t.Errorf("digest should mention authfail, got %d", digestHasRule(sink, "authfail"))
	}

	// A second auth failure does NOT refire (still-failed, not a fresh flip).
	if err := sched.PollOnce(ctx, "s1"); err == nil {
		t.Fatal("want auth-fail error #2")
	}
	if len(sink.Sent) != 1 {
		t.Errorf("still-failed must not refire: want 1, got %d", len(sink.Sent))
	}

	// Recover, then fail again → fresh flip fires once more.
	sched.Adapters["s1"] = &mockAdapter{obs: []domain.RatioObservation{{StationID: "s1", GroupName: "default", ModelName: "m", InputUSDPer1M: 2.0}}}
	if err := sched.PollOnce(ctx, "s1"); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	sched.Adapters["s1"] = fa
	if err := sched.PollOnce(ctx, "s1"); err == nil {
		t.Fatal("want auth-fail error #3")
	}
	if len(sink.Sent) != 2 {
		t.Errorf("fresh flip after recovery should fire again: want 2, got %d", len(sink.Sent))
	}
}
