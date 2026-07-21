package changedet

import (
	"testing"
	"time"

	"transitmonitor/internal/domain"
)

var fixedTime = time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

func obs(group, model string, in, out, native float64, sentinel string) domain.RatioObservation {
	return domain.RatioObservation{
		StationID: "s1", GroupName: group, ModelName: model,
		InputUSDPer1M: in, OutputUSDPer1M: out, NativeRatio: native,
		Sentinel: sentinel, ObservedAt: fixedTime,
	}
}

func findEvent(ev []domain.ChangeEvent, field string) (domain.ChangeEvent, bool) {
	for _, e := range ev {
		if e.Field == field {
			return e, true
		}
	}
	return domain.ChangeEvent{}, false
}

func TestDiff_ValueChangeCritical(t *testing.T) {
	prev := []domain.RatioObservation{obs("default", "gpt-4o", 2.0, 10, 1.25, "")}
	curr := []domain.RatioObservation{obs("default", "gpt-4o", 2.5, 10, 1.25, "")}
	ev := Diff(prev, curr, DefaultConfig())
	if e, ok := findEvent(ev, FieldInput); !ok {
		t.Fatalf("want input change event, got %v", ev)
	} else {
		if !eq(e.DeltaAbs, 0.5) {
			t.Errorf("delta_abs: want 0.5 got %v", e.DeltaAbs)
		}
		if !eq(e.DeltaPct, 25) {
			t.Errorf("delta_pct: want 25 got %v", e.DeltaPct)
		}
		if e.Severity != SevCritical {
			t.Errorf("severity: want critical got %s", e.Severity)
		}
	}
	if _, ok := findEvent(ev, FieldOutput); ok {
		t.Error("output unchanged → should not produce event")
	}
}

func TestDiff_ModelAdded(t *testing.T) {
	prev := []domain.RatioObservation{obs("default", "gpt-4o", 2.5, 10, 1.25, "")}
	curr := []domain.RatioObservation{
		obs("default", "gpt-4o", 2.5, 10, 1.25, ""),
		obs("default", "new-model", 3.0, 12, 1.5, ""),
	}
	ev := Diff(prev, curr, DefaultConfig())
	e, ok := findEvent(ev, FieldPresence)
	if !ok {
		t.Fatalf("want presence event, got %v", ev)
	}
	if e.New != SentAdded {
		t.Errorf("want added got %s", e.New)
	}
	if e.Model != "new-model" {
		t.Errorf("model: want new-model got %s", e.Model)
	}
}

func TestDiff_ModelRemoved(t *testing.T) {
	prev := []domain.RatioObservation{
		obs("default", "gpt-4o", 2.5, 10, 1.25, ""),
		obs("default", "gone", 3.0, 12, 1.5, ""),
	}
	curr := []domain.RatioObservation{obs("default", "gpt-4o", 2.5, 10, 1.25, "")}
	ev := Diff(prev, curr, DefaultConfig())
	e, ok := findEvent(ev, FieldPresence)
	if !ok {
		t.Fatalf("want presence event, got %v", ev)
	}
	if e.New != SentRemoved {
		t.Errorf("want removed got %s", e.New)
	}
}

func TestDiff_SentinelFlip(t *testing.T) {
	prev := []domain.RatioObservation{obs("default", "m", 2.0, 8, 2.0, "")}
	curr := []domain.RatioObservation{obs("default", "m", 0, 0, 37.5, "unconfigured-37.5")}
	ev := Diff(prev, curr, DefaultConfig())
	e, ok := findEvent(ev, FieldSentinelFlip)
	if !ok {
		t.Fatalf("want sentinel_flip event, got %v", ev)
	}
	if e.Old != "" || e.New != "unconfigured-37.5" {
		t.Errorf("flip: old=%q new=%q", e.Old, e.New)
	}
	// A sentinel_flip must NOT also produce value-change events for the 0'd fields.
	if _, ok := findEvent(ev, FieldInput); ok {
		t.Error("sentinel_flip should suppress value-change events")
	}
}

func TestDiff_Idempotent(t *testing.T) {
	snap := []domain.RatioObservation{obs("default", "gpt-4o", 2.5, 10, 1.25, "")}
	ev := Diff(snap, snap, DefaultConfig())
	if len(ev) != 0 {
		t.Errorf("identical snapshots must produce 0 events, got %d: %v", len(ev), ev)
	}
}

func TestDiff_ExcludedRowsNoValueChange(t *testing.T) {
	prev := []domain.RatioObservation{obs("default", "dall-e-3", 0, 0, 0.04, "fixed-price (per-call)")}
	curr := []domain.RatioObservation{obs("default", "dall-e-3", 0, 0, 0.05, "fixed-price (per-call)")}
	ev := Diff(prev, curr, DefaultConfig())
	if len(ev) != 0 {
		t.Errorf("excluded (fixed-price) rows must not produce value-change events, got %v", ev)
	}
}

func TestDiff_SeverityLevels(t *testing.T) {
	// 10% → warning, 30% → critical
	prev := []domain.RatioObservation{obs("g", "a", 1.0, 0, 0, ""), obs("g", "b", 1.0, 0, 0, "")}
	curr := []domain.RatioObservation{obs("g", "a", 1.1, 0, 0, ""), obs("g", "b", 1.3, 0, 0, "")}
	ev := Diff(prev, curr, DefaultConfig())
	byModel := map[string]domain.ChangeEvent{}
	for _, e := range ev {
		if e.Field == FieldInput {
			byModel[e.Model] = e
		}
	}
	if byModel["a"].Severity != SevWarning {
		t.Errorf("10%% should be warning, got %s", byModel["a"].Severity)
	}
	if byModel["b"].Severity != SevCritical {
		t.Errorf("30%% should be critical, got %s", byModel["b"].Severity)
	}
}

func eq(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}
