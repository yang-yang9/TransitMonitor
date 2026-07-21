package newapi

import (
	"context"
	"testing"
)

// Verifies per-(model × group) expansion when Group == "*".

func TestFetchRatios_AllGroups(t *testing.T) {
	_, a := startMock(t, mockCfg{
		group: "*", // expand across the model's enable_groups ∩ group_ratio
		pricingItems: []pricingItem{{
			ModelName: "gpt-4o", ModelRatio: 1.25, CompletionRatio: 4,
			EnableGroup: []string{"default", "vip"},
		}},
		groupRatio: map[string]float64{"default": 1.0, "vip": 0.8},
	})
	caps, _ := a.ProbeCapabilities(context.Background())
	_, obs, err := a.FetchRatios(context.Background(), caps)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	// one observation per group: default (gr 1.0 → 1.25×2×1=2.5), vip (gr 0.8 → 2.0)
	byGroup := map[string]float64{}
	for _, o := range obs {
		if o.ModelName != "gpt-4o" {
			t.Errorf("unexpected model %q", o.ModelName)
			continue
		}
		byGroup[o.GroupName] = o.InputUSDPer1M
	}
	if len(byGroup) != 2 {
		t.Fatalf("want 2 group observations (default+vip), got %d: %+v", len(byGroup), byGroup)
	}
	if !approxEq(byGroup["default"], 2.5) {
		t.Errorf("default input: want 2.5 got %v", byGroup["default"])
	}
	if !approxEq(byGroup["vip"], 2.0) {
		t.Errorf("vip input: want 2.0 got %v", byGroup["vip"])
	}
}

func TestFetchRatios_AllGroupsHasAll(t *testing.T) {
	// enable_groups contains "all" → expand to every group_ratio key.
	_, a := startMock(t, mockCfg{
		group: "*",
		pricingItems: []pricingItem{{
			ModelName: "gpt-4o", ModelRatio: 1.25, CompletionRatio: 4,
			EnableGroup: []string{"all"},
		}},
		groupRatio: map[string]float64{"default": 1.0, "svip": 0.5},
	})
	caps, _ := a.ProbeCapabilities(context.Background())
	_, obs, _ := a.FetchRatios(context.Background(), caps)
	byGroup := map[string]bool{}
	for _, o := range obs {
		byGroup[o.GroupName] = true
	}
	if !byGroup["default"] || !byGroup["svip"] {
		t.Errorf("enable_groups=[all] should expand to all group_ratio keys; got %v", byGroup)
	}
}
