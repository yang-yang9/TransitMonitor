// Package changedet diffs two snapshots' RatioObservations into ChangeEvents.
//
// It is pure (no I/O) and test-driven from
// openspec/.../specs/change-detection/spec.md. Rules:
//   - value change on input_usd_per_1m / output_usd_per_1m / native_ratio
//     (only for "normal" rows whose Sentinel is empty);
//   - model_added / model_removed (presence);
//   - sentinel_flip (Sentinel string changes);
//   - idempotent: identical snapshots → 0 events;
//   - excluded rows (fixed-price / 37.5 sentinel / simple mode / missing base)
//     do NOT participate in value-change comparison.
package changedet

import (
	"fmt"
	"math"

	"transitmonitor/internal/domain"
)

// Config tunes diff thresholds and the float "no-change" tolerance.
type Config struct {
	WarningPct  float64 // default 5
	CriticalPct float64 // default 20
	RelEpsilon  float64 // relative tolerance for "no change" (default 1e-9)
}

// DefaultConfig returns standard thresholds.
func DefaultConfig() Config {
	return Config{WarningPct: 5, CriticalPct: 20, RelEpsilon: 1e-9}
}

// Field name constants.
const (
	FieldInput        = "input_usd_per_1m"
	FieldOutput       = "output_usd_per_1m"
	FieldNative       = "native_ratio"
	FieldPresence     = "presence"
	FieldSentinelFlip = "sentinel_flip"

	SentAdded   = "added"
	SentRemoved = "removed"

	SevInfo     = "info"
	SevWarning  = "warning"
	SevCritical = "critical"
)

// Diff compares prev and curr (keyed by group+model) and returns ChangeEvents.
func Diff(prev, curr []domain.RatioObservation, cfg Config) []domain.ChangeEvent {
	pm := indexObs(prev)
	cm := indexObs(curr)

	seen := make(map[string]bool, len(pm)+len(cm))
	for k := range pm {
		seen[k] = true
	}
	for k := range cm {
		seen[k] = true
	}

	var events []domain.ChangeEvent
	for k := range seen {
		p, pOK := pm[k]
		c, cOK := cm[k]
		switch {
		case !pOK:
			events = append(events, presenceEvent(c, SentAdded, cfg))
		case !cOK:
			events = append(events, presenceEvent(p, SentRemoved, cfg))
		default:
			if p.Sentinel != c.Sentinel {
				events = append(events, flipEvent(p, c))
				continue
			}
			// Excluded rows do not participate in value-change comparison.
			if p.Sentinel != "" || c.Sentinel != "" {
				continue
			}
			events = append(events, valueEvents(p, c, cfg)...)
		}
	}
	return events
}

func indexObs(obs []domain.RatioObservation) map[string]domain.RatioObservation {
	m := make(map[string]domain.RatioObservation, len(obs))
	for _, o := range obs {
		m[o.GroupName+"|"+o.ModelName] = o
	}
	return m
}

func presenceEvent(o domain.RatioObservation, status string, cfg Config) domain.ChangeEvent {
	return domain.ChangeEvent{
		StationID: o.StationID, Group: o.GroupName, Model: o.ModelName,
		Field: FieldPresence, New: status, ObservedAt: o.ObservedAt,
		Severity: severityForPresence(status, cfg),
	}
}

func severityForPresence(status string, cfg Config) string {
	switch status {
	case SentRemoved:
		return SevWarning // default; configurable later
	default:
		return SevInfo
	}
}

func flipEvent(p, c domain.RatioObservation) domain.ChangeEvent {
	return domain.ChangeEvent{
		StationID: c.StationID, Group: c.GroupName, Model: c.ModelName,
		Field: FieldSentinelFlip,
		Old:   p.Sentinel, New: c.Sentinel,
		ObservedAt: c.ObservedAt, Severity: SevWarning,
	}
}

func valueEvents(p, c domain.RatioObservation, cfg Config) []domain.ChangeEvent {
	var ev []domain.ChangeEvent
	add := func(field string, pv, cv float64) {
		if approxEq(pv, cv, cfg.RelEpsilon) {
			return
		}
		delta := cv - pv
		ev = append(ev, domain.ChangeEvent{
			StationID: c.StationID, Group: c.GroupName, Model: c.ModelName,
			Field: field, Old: fmt.Sprint(pv), New: fmt.Sprint(cv),
			DeltaAbs: delta, DeltaPct: pct(pv, delta),
			ObservedAt: c.ObservedAt, Severity: severity(pct(pv, delta), cfg),
		})
	}
	add(FieldInput, p.InputUSDPer1M, c.InputUSDPer1M)
	add(FieldOutput, p.OutputUSDPer1M, c.OutputUSDPer1M)
	add(FieldNative, p.NativeRatio, c.NativeRatio)
	return ev
}

func pct(prev float64, delta float64) float64 {
	if prev == 0 {
		return 0 // undefined; rely on DeltaAbs
	}
	return delta / prev * 100
}

func severity(p float64, cfg Config) string {
	switch {
	case p >= cfg.CriticalPct:
		return SevCritical
	case p >= cfg.WarningPct:
		return SevWarning
	default:
		return SevInfo
	}
}

func approxEq(a, b, rel float64) bool {
	d := math.Abs(a - b)
	if d <= 1e-12 {
		return true
	}
	scale := math.Abs(a)
	if scale < math.Abs(b) {
		scale = math.Abs(b)
	}
	return d <= rel*scale
}
