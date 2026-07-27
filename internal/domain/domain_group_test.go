package domain

import (
	"reflect"
	"testing"
)

func TestPartitionGroups(t *testing.T) {
	ratios := map[string]float64{
		"vip": 0.5, "svip": 0.8, "pro": 1.0, "default": 1.0, "trial": 1.5, "internal": 2.0,
	}
	cfgs := []StationGroupConfig{
		{StationID: "s1", GroupName: "vip", Visible: true, SortOrder: 0},
		{StationID: "s1", GroupName: "svip", Visible: true, SortOrder: 1},
		{StationID: "s1", GroupName: "pro", Visible: false, SortOrder: 0},
		{StationID: "s1", GroupName: "trial", Visible: false, SortOrder: 1},
	}
	got := PartitionGroups(ratios, nil, cfgs)

	// visible block first (by sort_order), then hidden block (by sort_order),
	// unconfigured (default, internal) default to visible and land among the
	// visible block by sort_order=0 then name.
	want := []GroupDisplay{
		{Name: "default", Ratio: 1.0, Visible: true, Order: 0}, // unconfigured, sort 0, name before vip
		{Name: "internal", Ratio: 2.0, Visible: true, Order: 0},
		{Name: "vip", Ratio: 0.5, Visible: true, Order: 0},
		{Name: "svip", Ratio: 0.8, Visible: true, Order: 1},
		{Name: "pro", Ratio: 1.0, Visible: false, Order: 0},
		{Name: "trial", Ratio: 1.5, Visible: false, Order: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PartitionGroups order wrong:\n got=%+v\nwant=%+v", got, want)
	}

	vis, hid := SplitVisible(got)
	if len(vis) != 4 || len(hid) != 2 {
		t.Fatalf("split: want 4 vis / 2 hid, got %d/%d", len(vis), len(hid))
	}
	if hid[0].Name != "pro" || hid[1].Name != "trial" {
		t.Errorf("hidden order wrong: %+v", hid)
	}
}

func TestPartitionGroupsEmptyConfigDefaultsVisible(t *testing.T) {
	ratios := map[string]float64{"a": 1.0, "b": 2.0}
	got := PartitionGroups(ratios, nil, nil)
	for _, g := range got {
		if !g.Visible {
			t.Errorf("group %s: unconfigured should default visible=true", g.Name)
		}
	}
	// no config → sort_order 0 for all → stable name asc
	if got[0].Name != "a" || got[1].Name != "b" {
		t.Errorf("name order wrong: %+v", got)
	}
}

func TestPartitionGroupsDuplicateSortOrderStableByName(t *testing.T) {
	ratios := map[string]float64{"b": 1.0, "a": 1.0, "c": 1.0}
	cfgs := []StationGroupConfig{
		{GroupName: "a", Visible: true, SortOrder: 5},
		{GroupName: "b", Visible: true, SortOrder: 5},
		{GroupName: "c", Visible: true, SortOrder: 5},
	}
	got := PartitionGroups(ratios, nil, cfgs)
	if got[0].Name != "a" || got[1].Name != "b" || got[2].Name != "c" {
		t.Errorf("duplicate sort_order should tie-break by name: %+v", got)
	}
}

// TestPartitionGroupsOverrideBadge: when a group's ratio differs from its
// recorded default (per-user override), PartitionGroups must set Overridden +
// Default so the UI can badge it; groups matching their default stay unbadged.
func TestPartitionGroupsOverrideBadge(t *testing.T) {
	ratios := map[string]float64{"kiro-低缓": 0.145, "kiro-高缓": 0.18}
	defaults := map[string]float64{"kiro-低缓": 0.15, "kiro-高缓": 0.18}
	got := PartitionGroups(ratios, defaults, nil)
	byName := map[string]GroupDisplay{}
	for _, g := range got {
		byName[g.Name] = g
	}
	if !byName["kiro-低缓"].Overridden || byName["kiro-低缓"].Default != 0.15 {
		t.Errorf("kiro-低缓: want Overridden=true Default=0.15, got %+v", byName["kiro-低缓"])
	}
	if byName["kiro-高缓"].Overridden {
		t.Errorf("kiro-高缓: rate matches default, want Overridden=false, got %+v", byName["kiro-高缓"])
	}
}
