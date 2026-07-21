package store

import (
	"context"
	"testing"
	"time"

	"transitmonitor/internal/domain"
)

// Backfills tests for the audit_log + probe_results CRUD added in the polish pass.

func TestAuditLog(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.InsertAuditLog(ctx, "probe", "probe.run", "s1", "model=gpt-4o markup=0.00%"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.InsertAuditLog(ctx, "main", "startup", "", "version=0.1 stations=2"); err != nil {
		t.Fatalf("insert2: %v", err)
	}
	got, err := s.ListAuditLogs(ctx, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries got %d", len(got))
	}
	// Both inserts land in the same second (DATETIME second-resolution), so
	// order is not guaranteed — assert set-membership, not order.
	actors := map[string]bool{}
	var probeEntry *domain.AuditEntry
	for i := range got {
		actors[got[i].Actor] = true
		if got[i].Actor == "probe" {
			probeEntry = &got[i]
		}
	}
	if !actors["probe"] || !actors["main"] {
		t.Errorf("want both probe+main actors, got %v", actors)
	}
	if probeEntry == nil || probeEntry.Action != "probe.run" || probeEntry.Target != "s1" {
		t.Errorf("probe entry: %+v", probeEntry)
	}
}

func TestProbeResults(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC).UTC()
	pr := domain.ProbeResult{
		StationID: "s1", Model: "gpt-4o", TokensIn: 8, TokensOut: 1,
		DeclaredEffectiveUSDPer1M: 2.5, MeasuredUSDPer1M: 2.5, MarkupPct: 0,
		CostUSD: 2e-5, ObservedAt: now,
	}
	if err := s.InsertProbeResult(ctx, pr); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := s.ListProbeResults(ctx, "s1", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 got %d", len(got))
	}
	if got[0].Model != "gpt-4o" || got[0].MeasuredUSDPer1M != 2.5 {
		t.Errorf("result: %+v", got[0])
	}
	if got[0].DeclaredUnavailable {
		t.Error("DeclaredUnavailable should be false")
	}
}
