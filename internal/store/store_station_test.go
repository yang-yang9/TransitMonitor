package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"transitmonitor/internal/domain"
)

func TestStationCRUD(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	key := []byte("0123456789abcdef0123456789abcdef") // 32 bytes

	st := domain.Station{
		ID: "web-1", Name: "Web-added", BaseURL: "https://relay.example.com",
		Kind: domain.KindNewAPI, Auth: domain.AuthConfig{APIKey: "sk-secret", Group: "default"},
		PollInterval: domain.Duration(3 * time.Minute), Enabled: true, Tags: []string{"vip", "prod"},
		Probe: domain.ProbeConfig{Enabled: false, Model: "gpt-4o-mini", DryRun: true},
	}
	if err := s.UpsertStation(ctx, st, key); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := s.ListStationsDB(ctx, key)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 station got %d", len(got))
	}
	g := got[0]
	if g.ID != "web-1" || g.Kind != domain.KindNewAPI || g.BaseURL != "https://relay.example.com" {
		t.Errorf("station fields wrong: %+v", g)
	}
	if g.Auth.APIKey != "sk-secret" || g.Auth.Group != "default" {
		t.Errorf("creds not decrypted: %+v", g.Auth)
	}
	if time.Duration(g.PollInterval) != 3*time.Minute {
		t.Errorf("poll_interval: want 3m got %v", time.Duration(g.PollInterval))
	}
	if len(g.Tags) != 2 || g.Tags[0] != "vip" {
		t.Errorf("tags: %+v", g.Tags)
	}
	if !g.Enabled {
		t.Error("enabled should be true")
	}
	if g.Probe.Model != "gpt-4o-mini" {
		t.Errorf("probe not persisted: %+v", g.Probe)
	}

	// update (upsert same id)
	st.Name = "Renamed"
	if err := s.UpsertStation(ctx, st, key); err != nil {
		t.Fatalf("upsert2: %v", err)
	}
	got2, _ := s.ListStationsDB(ctx, key)
	if len(got2) != 1 || got2[0].Name != "Renamed" {
		t.Errorf("update failed: %+v", got2)
	}

	// delete
	if err := s.DeleteStation(ctx, "web-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got3, _ := s.ListStationsDB(ctx, key)
	if len(got3) != 0 {
		t.Errorf("after delete want 0 got %d", len(got3))
	}
}

// creds must never appear in the stations config blob (only in encrypted credentials).
func TestStationCRUD_NoPlaintextCreds(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	key := []byte("0123456789abcdef0123456789abcdef")
	st := domain.Station{ID: "s", Name: "s", Kind: domain.KindNewAPI, BaseURL: "https://x", Auth: domain.AuthConfig{APIKey: "sk-plaintext-secret"}}
	if err := s.UpsertStation(ctx, st, key); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	var blob string
	s.db.QueryRowContext(ctx, "SELECT config_yaml FROM stations WHERE id=?", "s").Scan(&blob)
	if strings.Contains(blob, "sk-plaintext-secret") {
		t.Errorf("plaintext creds leaked into stations.config_yaml: %s", blob)
	}
}
