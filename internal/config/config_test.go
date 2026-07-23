package config

import (
	"testing"
	"time"
)

// TestLoadExample verifies the shipped sample config parses (incl. Duration
// strings like "3m") and defaults are applied.
func TestLoadExample(t *testing.T) {
	f, err := Load("../../config.example.yaml")
	if err != nil {
		t.Fatalf("load example config: %v", err)
	}
	if len(f.Stations) != 2 {
		t.Fatalf("want 2 stations, got %d", len(f.Stations))
	}
	if f.Stations[0].Kind != "newapi" {
		t.Errorf("station 0 kind: want newapi got %s", f.Stations[0].Kind)
	}
	if time.Duration(f.Stations[0].PollInterval) != 3*time.Minute {
		t.Errorf("poll_interval: want 3m got %v", time.Duration(f.Stations[0].PollInterval))
	}
	if !f.Stations[0].Enabled {
		t.Error("stations should default to enabled")
	}
	if f.Dashboard.Addr != "127.0.0.1:7421" {
		t.Errorf("dashboard addr: got %s", f.Dashboard.Addr)
	}
	if f.DB.Path != "transitmonitor.db" {
		t.Errorf("db path: got %s", f.DB.Path)
	}
	if f.Timezone != "Asia/Shanghai" {
		t.Errorf("timezone: want Asia/Shanghai, got %q", f.Timezone)
	}
	if len(f.Alerts.Rules) != 2 {
		t.Errorf("want 2 alert rules, got %d", len(f.Alerts.Rules))
	}
}
