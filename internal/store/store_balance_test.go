package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"transitmonitor/internal/domain"
)

func TestBalanceObservationCRUD(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	t0 := time.Unix(1_700_000_000, 0)
	t1 := t0.Add(3 * time.Minute)
	t2 := t1.Add(3 * time.Minute)

	// new-api style: raw quota units, QuotaPerUnit=500000 → 1 unit-USD per 500000.
	ob0 := domain.BalanceObservation{
		StationID: "st-a", ObservedAt: t0, Remaining: 5_000_000, Used: 1_000_000, Total: 6_000_000,
		RemainingUSD: 10, UsedUSD: 2, TotalUSD: 12, Currency: "quota", QuotaPerUnit: 500000,
		SourceEndpoint: "/api/user/self",
	}
	ob1 := ob0
	ob1.ObservedAt = t1
	ob1.Remaining, ob1.RemainingUSD = 2_000_000, 4 // balance dropped to $4
	if err := s.InsertBalanceObservation(ctx, ob0); err != nil {
		t.Fatalf("insert ob0: %v", err)
	}
	if err := s.InsertBalanceObservation(ctx, ob1); err != nil {
		t.Fatalf("insert ob1: %v", err)
	}

	// LatestBalance returns the newest (ob1 @ t1).
	got, err := s.LatestBalance(ctx, "st-a")
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if got.RemainingUSD != 4 || got.TotalUSD != 12 || got.Currency != "quota" {
		t.Errorf("latest fields wrong: %+v", got)
	}
	if !got.ObservedAt.Equal(t1) {
		t.Errorf("latest observed_at: want %v got %v", t1, got.ObservedAt)
	}

	// PrevBalance(before=t2) returns ob1; (before=t1) returns ob0.
	prev, err := s.PrevBalance(ctx, "st-a", t2)
	if err != nil {
		t.Fatalf("prev: %v", err)
	}
	if prev.RemainingUSD != 4 {
		t.Errorf("prev before t2: want 4 got %v", prev.RemainingUSD)
	}
	prev0, err := s.PrevBalance(ctx, "st-a", t1)
	if err != nil {
		t.Fatalf("prev0: %v", err)
	}
	if prev0.RemainingUSD != 10 {
		t.Errorf("prev before t1: want 10 got %v", prev0.RemainingUSD)
	}
	// No reading before t0 → sql.ErrNoRows.
	if _, err := s.PrevBalance(ctx, "st-a", t0); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("prev before t0: want sql.ErrNoRows got %v", err)
	}

	// BalanceHistory returns oldest-first.
	hist, err := s.BalanceHistory(ctx, "st-a", 10)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("history len: want 2 got %d", len(hist))
	}
	if !hist[0].ObservedAt.Equal(t0) || !hist[1].ObservedAt.Equal(t1) {
		t.Errorf("history order wrong: %v then %v", hist[0].ObservedAt, hist[1].ObservedAt)
	}

	// LatestBalances: one row per station (newest).
	alls, err := s.LatestBalances(ctx)
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if len(alls) != 1 || alls[0].RemainingUSD != 4 {
		t.Errorf("latest all: %+v", alls)
	}

	// Unlimited flag round-trips (sub2api quota=0 → unlimited).
	unlim := domain.BalanceObservation{
		StationID: "st-b", ObservedAt: t0, Remaining: 5, Used: 0, Total: 0,
		RemainingUSD: 5, UsedUSD: 0, TotalUSD: 0, Unlimited: true, Currency: "USD",
		SourceEndpoint: "/api/v1/user/profile",
	}
	if err := s.InsertBalanceObservation(ctx, unlim); err != nil {
		t.Fatalf("insert unlim: %v", err)
	}
	got2, err := s.LatestBalance(ctx, "st-b")
	if err != nil {
		t.Fatalf("latest st-b: %v", err)
	}
	if !got2.Unlimited || got2.RemainingUSD != 5 {
		t.Errorf("unlimited round-trip wrong: %+v", got2)
	}
}

func TestBalanceRetention(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	now := time.Unix(2_000_000_000, 0)
	old := now.AddDate(0, 0, -30) // older than 7-day snapshot retention
	recent := now.AddDate(0, 0, -1)

	ins := func(when time.Time) {
		if err := s.InsertBalanceObservation(ctx, domain.BalanceObservation{
			StationID: "st-a", ObservedAt: when, Remaining: 1, RemainingUSD: 1, Currency: "USD",
		}); err != nil {
			t.Fatalf("insert %v: %v", when, err)
		}
	}
	ins(old)
	ins(recent)
	if err := s.DownsampleAndRetain(ctx, now, 7, 30); err != nil {
		t.Fatalf("retain: %v", err)
	}
	hist, err := s.BalanceHistory(ctx, "st-a", 10)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 1 || !hist[0].ObservedAt.Equal(recent) {
		t.Errorf("retention should keep only the recent row, got %+v", hist)
	}
}
