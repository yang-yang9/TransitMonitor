package store

import (
	"context"
	"testing"

	"transitmonitor/internal/domain"
)

func TestStationGroupConfigCRUD(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	insertStation(t, s, "s1")

	// initial: no config rows
	if got, err := s.GetStationGroupConfigs(ctx, "s1"); err != nil || len(got) != 0 {
		t.Fatalf("initial get: got=%v err=%v", got, err)
	}

	// save two visible + one hidden, with explicit ordering
	if err := s.SaveStationGroupConfigs(ctx, "s1", []domain.StationGroupConfig{
		{StationID: "s1", GroupName: "vip", Visible: true, SortOrder: 0},
		{StationID: "s1", GroupName: "svip", Visible: true, SortOrder: 1},
		{StationID: "s1", GroupName: "internal", Visible: false, SortOrder: 0},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := s.GetStationGroupConfigs(ctx, "s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 rows got %d", len(got))
	}
	byName := map[string]domain.StationGroupConfig{}
	for _, c := range got {
		byName[c.GroupName] = c
	}
	if c := byName["vip"]; !c.Visible || c.SortOrder != 0 {
		t.Errorf("vip row wrong: %+v", c)
	}
	if c := byName["internal"]; c.Visible || c.SortOrder != 0 {
		t.Errorf("internal should be hidden: %+v", c)
	}

	// replace-all: saving a different set drops the old rows
	if err := s.SaveStationGroupConfigs(ctx, "s1", []domain.StationGroupConfig{
		{StationID: "s1", GroupName: "pro", Visible: false, SortOrder: 0},
	}); err != nil {
		t.Fatalf("save2: %v", err)
	}
	got2, _ := s.GetStationGroupConfigs(ctx, "s1")
	if len(got2) != 1 || got2[0].GroupName != "pro" {
		t.Errorf("replace-all failed: %+v", got2)
	}
}

func TestStationGroupConfigCascadeDelete(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	insertStation(t, s, "s1")
	_ = s.SaveStationGroupConfigs(ctx, "s1", []domain.StationGroupConfig{
		{StationID: "s1", GroupName: "vip", Visible: true, SortOrder: 0},
	})
	if err := s.DeleteStation(ctx, "s1"); err != nil {
		t.Fatalf("delete station: %v", err)
	}
	got, _ := s.GetStationGroupConfigs(ctx, "s1")
	if len(got) != 0 {
		t.Errorf("cascade failed: still %d rows", len(got))
	}
}

func TestUpsertStationGroupConfig(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	insertStation(t, s, "s1")
	// upsert then toggle
	_ = s.UpsertStationGroupConfig(ctx, domain.StationGroupConfig{StationID: "s1", GroupName: "vip", Visible: true, SortOrder: 0})
	_ = s.UpsertStationGroupConfig(ctx, domain.StationGroupConfig{StationID: "s1", GroupName: "vip", Visible: false, SortOrder: 2})
	got, _ := s.GetStationGroupConfigs(ctx, "s1")
	if len(got) != 1 || got[0].Visible || got[0].SortOrder != 2 {
		t.Errorf("upsert did not update in place: %+v", got)
	}
}
