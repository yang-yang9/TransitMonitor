package newapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"transitmonitor/internal/domain"
)

// White-box tests for NewAPIAdapter. Each case mirrors a Scenario in
// openspec/.../specs/ratio-collection-newapi/spec.md and ties the adapter's
// HTTP scraping to the (already green) normalize math.

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type mockCfg struct {
	selfUse       bool
	quotaPerUnit  float64
	pricingItems  []pricingItem
	groupRatio    map[string]float64
	pricingStatus int // 0 → 200
	ratioConfigOK bool
	ratioConfig   ratioConfigData
	userGroups    map[string]float64 // nil + pat check → 401
	pat           string             // expected bearer for /api/user/self/groups
	group         string             // adapter observed group (default "default")
}

func startMock(t *testing.T, cfg mockCfg) (*httptest.Server, *Adapter) {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		var sr statusResp
		sr.Success = true
		sr.Data.QuotaPerUnit = cfg.quotaPerUnit
		if sr.Data.QuotaPerUnit == 0 {
			sr.Data.QuotaPerUnit = 500000
		}
		sr.Data.SelfUseModeEnabled = cfg.selfUse
		sr.Data.USDExchangeRate = 7.3
		writeJSON(w, 200, sr)
	})
	mux.HandleFunc("/api/ratio_config", func(w http.ResponseWriter, r *http.Request) {
		if cfg.ratioConfigOK {
			writeJSON(w, 200, ratioConfigResp{Success: true, Data: cfg.ratioConfig})
			return
		}
		writeJSON(w, 403, map[string]any{"success": false, "message": "倍率配置接口未启用"})
	})
	mux.HandleFunc("/api/pricing", func(w http.ResponseWriter, r *http.Request) {
		st := cfg.pricingStatus
		if st == 0 {
			st = 200
		}
		if st != 200 {
			writeJSON(w, st, map[string]any{"success": false})
			return
		}
		writeJSON(w, 200, pricingResp{Success: true, Data: cfg.pricingItems, GroupRatio: cfg.groupRatio})
	})
	mux.HandleFunc("/api/user/self/groups", func(w http.ResponseWriter, r *http.Request) {
		if cfg.pat == "" || r.Header.Get("Authorization") != "Bearer "+cfg.pat {
			writeJSON(w, 401, map[string]any{"success": false, "message": "unauthorized"})
			return
		}
		data := map[string]any{}
		for g, v := range cfg.userGroups {
			data[g] = map[string]any{"ratio": v}
		}
		writeJSON(w, 200, map[string]any{"success": true, "data": data})
	})

	srv := httptest.NewServer(mux)
	a := New("s1", srv.URL, cfg.pat, "sk-test", cfg.group, srv.Client())
	clock := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	a.SetClock(func() time.Time { return clock })
	t.Cleanup(srv.Close)
	return srv, a
}

func findObs(obs []domain.RatioObservation, model string) (domain.RatioObservation, bool) {
	for _, o := range obs {
		if o.ModelName == model {
			return o, true
		}
	}
	return domain.RatioObservation{}, false
}

func approxEq(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

func TestProbeCapabilities(t *testing.T) {
	_, a := startMock(t, mockCfg{
		pricingItems: []pricingItem{{ModelName: "gpt-4o", ModelRatio: 1.25, CompletionRatio: 4}},
		groupRatio:   map[string]float64{"default": 1.0},
	})
	caps, err := a.ProbeCapabilities(context.Background())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !caps.HasStatus {
		t.Error("HasStatus should be true")
	}
	if caps.HasRatioConfig {
		t.Error("ratio_config should be 403 (off) → HasRatioConfig false")
	}
	if !caps.HasPricing {
		t.Error("HasPricing should be true")
	}
	if caps.SelfUseMode {
		t.Error("SelfUseMode should be false")
	}
	if caps.QuotaPerUnit != 500000 {
		t.Errorf("QuotaPerUnit: want 500000 got %v", caps.QuotaPerUnit)
	}
}

func TestFetchRatios_PricingFallback(t *testing.T) {
	_, a := startMock(t, mockCfg{
		pricingItems: []pricingItem{
			{ModelName: "gpt-4o", QuotaType: 0, ModelRatio: 1.25, CompletionRatio: 4},
			{ModelName: "dall-e-3", QuotaType: 1, ModelPrice: 0.04},
		},
		groupRatio: map[string]float64{"default": 1.0},
	})
	caps, _ := a.ProbeCapabilities(context.Background())
	snap, obs, err := a.FetchRatios(context.Background(), caps)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if snap.ObservedAt.IsZero() {
		t.Error("ObservedAt not set")
	}
	if len(obs) != 2 {
		t.Fatalf("want 2 obs, got %d", len(obs))
	}
	gpt, ok := findObs(obs, "gpt-4o")
	if !ok {
		t.Fatal("gpt-4o missing")
	}
	if gpt.SourceEndpoint != "/api/pricing" {
		t.Errorf("source: want /api/pricing got %q", gpt.SourceEndpoint)
	}
	if !approxEq(gpt.InputUSDPer1M, 2.5) {
		t.Errorf("gpt-4o input: want 2.5 got %v", gpt.InputUSDPer1M)
	}
	if !approxEq(gpt.OutputUSDPer1M, 10) {
		t.Errorf("gpt-4o output: want 10 got %v", gpt.OutputUSDPer1M)
	}
	dalle, ok := findObs(obs, "dall-e-3")
	if !ok {
		t.Fatal("dall-e-3 missing")
	}
	if !approxEq(dalle.FixedPriceUSD, 0.04) {
		t.Errorf("dall-e-3 fixed: want 0.04 got %v", dalle.FixedPriceUSD)
	}
	if dalle.Sentinel != "fixed-price (per-call)" {
		t.Errorf("dall-e-3 sentinel: got %q", dalle.Sentinel)
	}
}

func TestFetchRatios_SelfUseSentinel(t *testing.T) {
	_, a := startMock(t, mockCfg{
		selfUse:      true,
		pricingItems: []pricingItem{{ModelName: "unknown-model", ModelRatio: 37.5}},
		groupRatio:   map[string]float64{"default": 1.0},
	})
	caps, _ := a.ProbeCapabilities(context.Background())
	if !caps.SelfUseMode {
		t.Error("SelfUseMode should be true")
	}
	_, obs, err := a.FetchRatios(context.Background(), caps)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	u, ok := findObs(obs, "unknown-model")
	if !ok {
		t.Fatal("unknown-model missing")
	}
	if u.Sentinel != "unconfigured-37.5" {
		t.Errorf("sentinel: want unconfigured-37.5 got %q", u.Sentinel)
	}
	if u.InputUSDPer1M != 0 {
		t.Errorf("sentinel input should be 0, got %v", u.InputUSDPer1M)
	}
}

func TestFetchRatios_PricingEmpty(t *testing.T) {
	_, a := startMock(t, mockCfg{}) // empty pricing data
	caps, _ := a.ProbeCapabilities(context.Background())
	_, obs, err := a.FetchRatios(context.Background(), caps)
	if err != nil {
		t.Fatalf("empty pricing should not error: %v", err)
	}
	if len(obs) != 0 {
		t.Errorf("want 0 obs, got %d", len(obs))
	}
}

func TestFetchRatios_UserGroupsOverride(t *testing.T) {
	_, a := startMock(t, mockCfg{
		pat:          "tok",
		userGroups:   map[string]float64{"vip": 0.8},
		group:        "vip",
		pricingItems: []pricingItem{{ModelName: "gpt-4o", ModelRatio: 1.25, CompletionRatio: 4}},
		groupRatio:   map[string]float64{"vip": 1.0, "default": 1.0},
	})
	caps, _ := a.ProbeCapabilities(context.Background())
	if !caps.HasUserGroups {
		t.Error("HasUserGroups should be true (PAT configured + 200)")
	}
	_, obs, err := a.FetchRatios(context.Background(), caps)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	gpt, ok := findObs(obs, "gpt-4o")
	if !ok {
		t.Fatal("gpt-4o missing")
	}
	// user group vip=0.8 overrides top vip=1.0 → 1.25×2×0.8 = 2.0
	if !approxEq(gpt.InputUSDPer1M, 2.0) {
		t.Errorf("input with user-group override: want 2.0 got %v", gpt.InputUSDPer1M)
	}
}

func TestFetchRatios_RatioConfigRich(t *testing.T) {
	_, a := startMock(t, mockCfg{
		ratioConfigOK: true,
		ratioConfig: ratioConfigData{
			ModelRatio:      map[string]float64{"gpt-4o": 1.25},
			CompletionRatio: map[string]float64{"gpt-4o": 4},
			GroupRatio:      map[string]float64{"default": 1.0},
		},
	})
	caps, _ := a.ProbeCapabilities(context.Background())
	if !caps.HasRatioConfig {
		t.Error("HasRatioConfig should be true")
	}
	_, obs, err := a.FetchRatios(context.Background(), caps)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	gpt, ok := findObs(obs, "gpt-4o")
	if !ok {
		t.Fatal("gpt-4o missing")
	}
	if gpt.SourceEndpoint != "/api/ratio_config" {
		t.Errorf("source: want /api/ratio_config got %q", gpt.SourceEndpoint)
	}
	if !approxEq(gpt.InputUSDPer1M, 2.5) {
		t.Errorf("gpt-4o input: want 2.5 got %v", gpt.InputUSDPer1M)
	}
}

func TestFetchRatios_NoSource(t *testing.T) {
	// pricing auth-gated (401) + ratio_config off (403) → no ratio source.
	_, a := startMock(t, mockCfg{pricingStatus: 401})
	caps, _ := a.ProbeCapabilities(context.Background())
	if caps.HasPricing || caps.HasRatioConfig {
		t.Error("expected no ratio source available")
	}
	_, _, err := a.FetchRatios(context.Background(), caps)
	if err == nil {
		t.Fatal("FetchRatios should error when no ratio source is available")
	}
}
