package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"transitmonitor/internal/domain"
)

func newTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return s, func() { _ = s.Close() }
}

func insertStation(t *testing.T, s *Store, id string) {
	t.Helper()
	_, err := s.db.ExecContext(context.Background(),
		`INSERT INTO stations (id, name, kind, base_url, config_yaml) VALUES (?,?,?,?,?)`,
		id, id, "newapi", "https://example.com", "")
	if err != nil {
		t.Fatalf("insert station: %v", err)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	// Re-running migrations must not error.
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var version int
	if err := s.db.QueryRow("SELECT COALESCE(MAX(version),0) FROM schema_version").Scan(&version); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if version < 1 {
		t.Errorf("schema_version: want >=1 got %d", version)
	}
}

func TestInsertAndLatestRatios(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	insertStation(t, s, "s1")
	obs := []domain.RatioObservation{
		{StationID: "s1", GroupName: "default", ModelName: "gpt-4o", InputUSDPer1M: 2.0, OutputUSDPer1M: 8, NativeRatio: 1.0, ObservedAt: now.Add(-2 * time.Hour)},
		{StationID: "s1", GroupName: "default", ModelName: "gpt-4o", InputUSDPer1M: 2.5, OutputUSDPer1M: 10, NativeRatio: 1.25, ObservedAt: now},
		{StationID: "s1", GroupName: "default", ModelName: "dall-e-3", FixedPriceUSD: 0.04, Sentinel: "fixed-price (per-call)", ObservedAt: now},
	}
	if err := s.InsertRatioObservations(context.Background(), obs); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := s.LatestRatioObservations(context.Background(), "s1")
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 latest (gpt-4o, dall-e-3), got %d", len(got))
	}
	byModel := map[string]domain.RatioObservation{}
	for _, o := range got {
		byModel[o.ModelName] = o
	}
	if byModel["gpt-4o"].InputUSDPer1M != 2.5 {
		t.Errorf("gpt-4o latest input: want 2.5 got %v", byModel["gpt-4o"].InputUSDPer1M)
	}
	if !byModel["gpt-4o"].ObservedAt.Equal(now) {
		t.Errorf("gpt-4o latest time: want %v got %v", now, byModel["gpt-4o"].ObservedAt)
	}
	if byModel["dall-e-3"].Sentinel != "fixed-price (per-call)" {
		t.Errorf("dall-e-3 sentinel: got %q", byModel["dall-e-3"].Sentinel)
	}
}

func TestCredentialsRoundTrip(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	insertStation(t, s, "s1")
	key := []byte("0123456789abcdef0123456789abcdef") // 32 bytes
	if err := s.SetCredentials(context.Background(), "s1", key, "sk-abc123"); err != nil {
		t.Fatalf("set: %v", err)
	}
	var ct []byte
	if err := s.db.QueryRow("SELECT ciphertext FROM credentials WHERE station_id=?", "s1").Scan(&ct); err != nil {
		t.Fatalf("raw select: %v", err)
	}
	if string(ct) == "sk-abc123" {
		t.Error("ciphertext must not equal plaintext")
	}
	got, err := s.GetCredentials(context.Background(), "s1", key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != "sk-abc123" {
		t.Errorf("round-trip: want sk-abc123 got %s", got)
	}
}

func TestCredentialsNoPlaintextColumn(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	rows, err := s.db.Query("PRAGMA table_info(credentials)")
	if err != nil {
		t.Fatalf("pragma: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if name == "pat" || name == "api_key" || name == "admin_api_key" || name == "jwt" || name == "password" {
			t.Errorf("credentials table must not have plaintext column %q", name)
		}
	}
}

func TestDownsampleAndRetain(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	insertStation(t, s, "s1")

	oldDay := now.AddDate(0, 0, -40) // 2026-06-11
	// 3 old raw observations in the same hour (inputs 2.0/2.5/3.0 → avg 2.5) + 1 recent.
	oldObs := []domain.RatioObservation{
		{StationID: "s1", GroupName: "default", ModelName: "gpt-4o", InputUSDPer1M: 2.0, ObservedAt: oldDay},
		{StationID: "s1", GroupName: "default", ModelName: "gpt-4o", InputUSDPer1M: 2.5, ObservedAt: oldDay.Add(10 * time.Minute)},
		{StationID: "s1", GroupName: "default", ModelName: "gpt-4o", InputUSDPer1M: 3.0, ObservedAt: oldDay.Add(20 * time.Minute)},
		{StationID: "s1", GroupName: "default", ModelName: "gpt-4o", InputUSDPer1M: 2.6, ObservedAt: now.Add(-1 * time.Hour)},
	}
	if err := s.InsertRatioObservations(context.Background(), oldObs); err != nil {
		t.Fatalf("insert obs: %v", err)
	}
	// Old + recent snapshots.
	if err := s.InsertSnapshot(context.Background(), domain.RawSnapshot{StationID: "s1", ObservedAt: oldDay}); err != nil {
		t.Fatalf("insert old snapshot: %v", err)
	}
	if err := s.InsertSnapshot(context.Background(), domain.RawSnapshot{StationID: "s1", ObservedAt: now.Add(-1 * time.Hour)}); err != nil {
		t.Fatalf("insert recent snapshot: %v", err)
	}

	if err := s.DownsampleAndRetain(context.Background(), now, 7, 30); err != nil {
		t.Fatalf("downsample: %v", err)
	}

	// Old snapshot deleted, recent kept.
	var snapCount int
	s.db.QueryRow("SELECT COUNT(*) FROM snapshots WHERE station_id=?", "s1").Scan(&snapCount)
	if snapCount != 1 {
		t.Errorf("snapshots after retain: want 1 got %d", snapCount)
	}

	// Old raw observations deleted → only the 1 recent remains.
	var obsCount int
	s.db.QueryRow("SELECT COUNT(*) FROM ratio_observations WHERE station_id=?", "s1").Scan(&obsCount)
	if obsCount != 1 {
		t.Errorf("raw observations after downsample: want 1 got %d", obsCount)
	}

	// Hourly aggregate present with avg input ≈ 2.5.
	var hCount int
	var avg float64
	s.db.QueryRow("SELECT COUNT(*), COALESCE(AVG(avg_input),0) FROM ratio_observations_hourly WHERE station_id=? AND model_name='gpt-4o'", "s1").Scan(&hCount, &avg)
	if hCount != 1 {
		t.Errorf("hourly aggregates: want 1 got %d", hCount)
	}
	d := avg - 2.5
	if d < 0 {
		d = -d
	}
	if d > 1e-9 {
		t.Errorf("hourly avg_input: want 2.5 got %v", avg)
	}

	// Idempotent: running again must not duplicate or error.
	if err := s.DownsampleAndRetain(context.Background(), now, 7, 30); err != nil {
		t.Fatalf("idempotent downsample: %v", err)
	}
	s.db.QueryRow("SELECT COUNT(*) FROM ratio_observations_hourly WHERE station_id=?", "s1").Scan(&hCount)
	s.db.QueryRow("SELECT COUNT(*) FROM ratio_observations WHERE station_id=?", "s1").Scan(&obsCount)
	if hCount != 1 || obsCount != 1 {
		t.Errorf("idempotent: hourly=%d obs=%d (want 1/1)", hCount, obsCount)
	}
}
