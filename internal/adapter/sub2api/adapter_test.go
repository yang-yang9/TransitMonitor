package sub2api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"transitmonitor/internal/domain"
)

// White-box tests for Sub2APIAdapter. Each case mirrors a Scenario in
// openspec/.../specs/ratio-collection-sub2api/spec.md.

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func ptrF(f float64) *float64 { return &f }
func ptrS(s string) *string   { return &s }

type mockCfg struct {
	billing        *billingResp // nil + billingStatus=404 → simple; nil + 401 → no billing
	billingStatus  int          // 0 → 200 (if billing!=nil) ; override for 404/401
	channels       []availableChannel
	channelsStatus int
	models         []string // /v1/models response (sk-key)
	apiKey         string   // expected bearer for billing
	jwt            string   // expected bearer for channels
	group          string
}

func startMock(t *testing.T, cfg mockCfg) (*httptest.Server, *Adapter) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sub2api/billing", func(w http.ResponseWriter, r *http.Request) {
		if cfg.apiKey == "" || r.Header.Get("Authorization") != "Bearer "+cfg.apiKey {
			writeJSON(w, 401, map[string]any{"error": "auth"})
			return
		}
		if cfg.billing == nil && cfg.billingStatus == 404 {
			writeJSON(w, 404, map[string]any{"error": "simple mode"})
			return
		}
		writeJSON(w, 200, *cfg.billing)
	})
	mux.HandleFunc("/api/v1/channels/available", func(w http.ResponseWriter, r *http.Request) {
		if cfg.jwt == "" || r.Header.Get("Authorization") != "Bearer "+cfg.jwt {
			writeJSON(w, 401, map[string]any{"error": "auth"})
			return
		}
		st := cfg.channelsStatus
		if st == 0 {
			st = 200
		}
		if st != 200 {
			writeJSON(w, st, map[string]any{"success": false})
			return
		}
		writeJSON(w, 200, channelsAvailableResp{Success: true, Data: cfg.channels})
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if cfg.apiKey == "" || r.Header.Get("Authorization") != "Bearer "+cfg.apiKey {
			writeJSON(w, 401, map[string]any{"error": "auth"})
			return
		}
		data := make([]modelEntry, 0, len(cfg.models))
		for _, id := range cfg.models {
			data = append(data, modelEntry{ID: id})
		}
		writeJSON(w, 200, modelsListResp{Object: "list", Data: data})
	})
	srv := httptest.NewServer(mux)
	a := New("s1", srv.URL, cfg.apiKey, cfg.jwt, "", "", "", cfg.group, srv.Client())
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

func findObsGroup(obs []domain.RatioObservation, model, group string) (domain.RatioObservation, bool) {
	for _, o := range obs {
		if o.ModelName == model && o.GroupName == group {
			return o, true
		}
	}
	return domain.RatioObservation{}, false
}

func eq(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

func oneModel(name string, in, out float64) supportedModel {
	return supportedModel{
		Name: name,
		Pricing: &modelPricing{
			InputPrice:  ptrF(in),
			OutputPrice: ptrF(out),
		},
	}
}

func oneGroup(name string, rate float64) availableGroupRef {
	return availableGroupRef{Name: name, RateMultiplier: rate}
}

func TestProbeCapabilities(t *testing.T) {
	_, a := startMock(t, mockCfg{
		apiKey: "sk-1", jwt: "jwt-1", group: "default",
		billing:  &billingResp{EffectiveRateMultiplier: 0.25},
		channels: []availableChannel{{Name: "c1", Platforms: []platformSection{{Platform: "anthropic", SupportedModels: []supportedModel{oneModel("gpt-4o-mini", 1.5e-7, 6e-7)}}}}},
	})
	caps, err := a.ProbeCapabilities(context.Background())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !caps.HasBilling {
		t.Error("HasBilling should be true")
	}
	if !caps.HasUserChannels {
		t.Error("HasUserChannels should be true")
	}
	if caps.SimpleMode {
		t.Error("SimpleMode should be false")
	}
}

func TestFetchRatios_Normal(t *testing.T) {
	_, a := startMock(t, mockCfg{
		apiKey: "sk-1", jwt: "jwt-1", group: "default",
		billing:  &billingResp{EffectiveRateMultiplier: 0.25},
		channels: []availableChannel{{Name: "c1", Platforms: []platformSection{{Platform: "anthropic", SupportedModels: []supportedModel{oneModel("gpt-4o-mini", 1.5e-7, 6e-7)}}}}},
	})
	caps, _ := a.ProbeCapabilities(context.Background())
	_, obs, err := a.FetchRatios(context.Background(), caps)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	m, ok := findObs(obs, "gpt-4o-mini")
	if !ok {
		t.Fatal("gpt-4o-mini missing")
	}
	// 1.5e-7 × 1e6 × 0.25 = 0.0375 ; 6e-7 × 1e6 × 0.25 = 0.15
	if !eq(m.InputUSDPer1M, 0.0375) {
		t.Errorf("input: want 0.0375 got %v", m.InputUSDPer1M)
	}
	if !eq(m.OutputUSDPer1M, 0.15) {
		t.Errorf("output: want 0.15 got %v", m.OutputUSDPer1M)
	}
	if m.Sentinel != "" {
		t.Errorf("sentinel should be empty, got %q", m.Sentinel)
	}
}

func TestFetchRatios_SimpleMode(t *testing.T) {
	_, a := startMock(t, mockCfg{
		apiKey: "sk-1", jwt: "jwt-1", group: "default",
		billingStatus: 404, // billing nil → 404 simple
		channels:      []availableChannel{{Name: "c1", Platforms: []platformSection{{Platform: "anthropic", SupportedModels: []supportedModel{oneModel("gpt-4o-mini", 1.5e-7, 6e-7)}}}}},
	})
	caps, _ := a.ProbeCapabilities(context.Background())
	if !caps.SimpleMode || caps.HasBilling {
		t.Error("expected SimpleMode=true, HasBilling=false")
	}
	_, obs, err := a.FetchRatios(context.Background(), caps)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	m, ok := findObs(obs, "gpt-4o-mini")
	if !ok {
		t.Fatal("gpt-4o-mini missing")
	}
	if m.Sentinel != "declared-unavailable (simple mode)" {
		t.Errorf("sentinel: got %q", m.Sentinel)
	}
	if !m.DeclaredUnavailable {
		t.Error("DeclaredUnavailable should be true in simple mode")
	}
	if m.InputUSDPer1M != 0 {
		t.Errorf("input should be 0 in simple mode, got %v", m.InputUSDPer1M)
	}
}

func TestFetchRatios_Peak(t *testing.T) {
	_, a := startMock(t, mockCfg{
		apiKey: "sk-1", jwt: "jwt-1", group: "default",
		billing: &billingResp{
			EffectiveRateMultiplier: 0.375,
			PeakRateEnabled:         true,
			PeakStart:               ptrS("09:00"),
			PeakEnd:                 ptrS("12:00"),
			PeakRateMultiplier:      ptrF(1.5),
		},
		channels: []availableChannel{{Name: "c1", Platforms: []platformSection{{Platform: "anthropic", SupportedModels: []supportedModel{oneModel("gpt-4o-mini", 1.5e-7, 6e-7)}}}}},
	})
	caps, _ := a.ProbeCapabilities(context.Background())
	_, obs, err := a.FetchRatios(context.Background(), caps)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	m, _ := findObs(obs, "gpt-4o-mini")
	// 1.5e-7 × 1e6 × 0.375 = 0.05625
	if !eq(m.InputUSDPer1M, 0.05625) {
		t.Errorf("input: want 0.05625 got %v", m.InputUSDPer1M)
	}
	if m.PeakInfo == "" {
		t.Error("peak_info should be non-empty")
	}
}

func TestFetchRatios_ChannelsEmpty(t *testing.T) {
	_, a := startMock(t, mockCfg{
		apiKey: "sk-1", jwt: "jwt-1", group: "default",
		billing:  &billingResp{EffectiveRateMultiplier: 0.25},
		channels: []availableChannel{}, // feature off or no models
	})
	caps, _ := a.ProbeCapabilities(context.Background())
	_, obs, err := a.FetchRatios(context.Background(), caps)
	if err != nil {
		t.Fatalf("empty channels should not error: %v", err)
	}
	if len(obs) != 0 {
		t.Errorf("want 0 obs without base prices, got %d", len(obs))
	}
}

func TestFetchRatios_NoSource(t *testing.T) {
	// No sk-key and no JWT → no endpoints attempted → no ratio source.
	_, a := startMock(t, mockCfg{})
	caps, _ := a.ProbeCapabilities(context.Background())
	if caps.HasBilling || caps.HasUserChannels {
		t.Error("expected no sources")
	}
	_, _, err := a.FetchRatios(context.Background(), caps)
	if err == nil {
		t.Fatal("FetchRatios should error when no ratio source")
	}
}

// TestFetchRatios_PerGroupDiscount is the core discount-adaptation test: the
// monitoring sk-key sits in a 1.0 (full-price) group, but the station exposes a
// 0.25 discount group. Each model must be priced per-group at that group's own
// rate_multiplier — NOT at the sk-key's billing effective (1.0) — so the
// discount group surfaces a discounted USD/1M instead of the original price.
func TestFetchRatios_PerGroupDiscount(t *testing.T) {
	_, a := startMock(t, mockCfg{
		apiKey: "sk-1", jwt: "jwt-1", group: "default",
		billing: &billingResp{EffectiveRateMultiplier: 1.0, GroupRateMultiplier: 1.0},
		channels: []availableChannel{{Name: "c1", Platforms: []platformSection{{
			Platform: "anthropic",
			Groups: []availableGroupRef{
				oneGroup("default", 1.0),
				oneGroup("discount", 0.25),
			},
			SupportedModels: []supportedModel{oneModel("gpt-4o-mini", 1.5e-7, 6e-7)},
		}}}},
	})
	caps, _ := a.ProbeCapabilities(context.Background())
	_, obs, err := a.FetchRatios(context.Background(), caps)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	disc, ok := findObsGroup(obs, "gpt-4o-mini", "discount")
	if !ok {
		t.Fatal("discount-group row missing")
	}
	// 1.5e-7 × 1e6 × 0.25 = 0.0375 — discount applied, NOT 1.0 (original).
	if !eq(disc.InputUSDPer1M, 0.0375) {
		t.Errorf("discount input: want 0.0375 got %v", disc.InputUSDPer1M)
	}
	if !eq(disc.NativeRatio, 0.25) {
		t.Errorf("discount native ratio: want 0.25 got %v", disc.NativeRatio)
	}
	full, ok := findObsGroup(obs, "gpt-4o-mini", "default")
	if !ok {
		t.Fatal("default-group row missing")
	}
	if !eq(full.InputUSDPer1M, 0.15) { // 1.5e-7 × 1e6 × 1.0
		t.Errorf("default input: want 0.15 got %v", full.InputUSDPer1M)
	}
}

// TestFetchRatios_PerGroupPeak: a subscription group with peak 09:00-12:00 ×1.5,
// clock 10:00 UTC (in-window, station tz UTC) → eff = 0.25 × 1.5 = 0.375. Peak
// is computed from the per-group config, not the sk-key's billing effective.
func TestFetchRatios_PerGroupPeak(t *testing.T) {
	_, a := startMock(t, mockCfg{
		apiKey: "sk-1", jwt: "jwt-1", group: "sub",
		billing: &billingResp{EffectiveRateMultiplier: 1.0, GroupRateMultiplier: 1.0, Timezone: ptrS("UTC")},
		channels: []availableChannel{{Name: "c1", Platforms: []platformSection{{
			Platform: "anthropic",
			Groups: []availableGroupRef{{
				Name: "sub", SubscriptionType: "subscription", RateMultiplier: 0.25,
				PeakRateEnabled: true, PeakStart: "09:00", PeakEnd: "12:00", PeakRateMultiplier: 1.5,
			}},
			SupportedModels: []supportedModel{oneModel("gpt-4o-mini", 1.5e-7, 6e-7)},
		}}}},
	})
	a.SetClock(func() time.Time { return time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC) })
	caps, _ := a.ProbeCapabilities(context.Background())
	_, obs, err := a.FetchRatios(context.Background(), caps)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	m, _ := findObsGroup(obs, "gpt-4o-mini", "sub")
	// 1.5e-7 × 1e6 × 0.25 × 1.5 = 0.05625
	if !eq(m.InputUSDPer1M, 0.05625) {
		t.Errorf("peak input: want 0.05625 got %v", m.InputUSDPer1M)
	}
	if !eq(m.NativeRatio, 0.375) {
		t.Errorf("native ratio (rate×peak): want 0.375 got %v", m.NativeRatio)
	}
	if m.PeakInfo == "" {
		t.Error("peak_info should be non-empty")
	}
}

// TestFetchRatios_PerGroupPeakOutsideWindow: same peak config, clock 12:00 UTC
// (exclusive end) → peak NOT applied → eff = 0.25 (group rate only).
func TestFetchRatios_PerGroupPeakOutsideWindow(t *testing.T) {
	_, a := startMock(t, mockCfg{
		apiKey: "sk-1", jwt: "jwt-1", group: "sub",
		billing: &billingResp{EffectiveRateMultiplier: 1.0, GroupRateMultiplier: 1.0, Timezone: ptrS("UTC")},
		channels: []availableChannel{{Name: "c1", Platforms: []platformSection{{
			Platform: "anthropic",
			Groups: []availableGroupRef{{
				Name: "sub", SubscriptionType: "subscription", RateMultiplier: 0.25,
				PeakRateEnabled: true, PeakStart: "09:00", PeakEnd: "12:00", PeakRateMultiplier: 1.5,
			}},
			SupportedModels: []supportedModel{oneModel("gpt-4o-mini", 1.5e-7, 6e-7)},
		}}}},
	})
	// 12:00 is the exclusive window end → outside [09:00,12:00).
	a.SetClock(func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) })
	caps, _ := a.ProbeCapabilities(context.Background())
	_, obs, err := a.FetchRatios(context.Background(), caps)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	m, _ := findObsGroup(obs, "gpt-4o-mini", "sub")
	// 1.5e-7 × 1e6 × 0.25 = 0.0375 (no peak factor).
	if !eq(m.InputUSDPer1M, 0.0375) {
		t.Errorf("out-of-window input: want 0.0375 got %v", m.InputUSDPer1M)
	}
	if !eq(m.NativeRatio, 0.25) {
		t.Errorf("native ratio: want 0.25 got %v", m.NativeRatio)
	}
}

// TestFetchRatios_ChannelsOnlyNoSkKey: a JWT-only station (no sk-key, no
// billing). Previously `effective` defaulted to 0 → InputUSDPer1M = base × 1e6 ×
// 0 = 0 (price lost entirely). With per-group rates from channels/available the
// model is priced at the group's rate, recovering non-zero USD/1M.
func TestFetchRatios_ChannelsOnlyNoSkKey(t *testing.T) {
	_, a := startMock(t, mockCfg{
		jwt: "jwt-1", group: "discount", // no apiKey → no billing
		channels: []availableChannel{{Name: "c1", Platforms: []platformSection{{
			Platform:        "anthropic",
			Groups:          []availableGroupRef{oneGroup("discount", 0.25)},
			SupportedModels: []supportedModel{oneModel("gpt-4o-mini", 1.5e-7, 6e-7)},
		}}}},
	})
	caps, _ := a.ProbeCapabilities(context.Background())
	if caps.HasBilling {
		t.Error("HasBilling should be false without sk-key")
	}
	_, obs, err := a.FetchRatios(context.Background(), caps)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	m, ok := findObsGroup(obs, "gpt-4o-mini", "discount")
	if !ok {
		t.Fatal("discount-group row missing")
	}
	if !eq(m.InputUSDPer1M, 0.0375) {
		t.Errorf("input: want 0.0375 got %v (channels-only path must not zero out)", m.InputUSDPer1M)
	}
}

// TestReactiveJWTRetry: a fingerprint-stale JWT (401 on channels/available)
// must trigger a re-login and retry, so per-model data is recovered without
// waiting for the 24h exp-based refresh.
func TestReactiveJWTRetry(t *testing.T) {
	const staleJWT, freshJWT = "jwt-stale", "jwt-fresh"
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sub2api/billing", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-1" {
			writeJSON(w, 401, map[string]any{"error": "auth"})
			return
		}
		writeJSON(w, 200, billingResp{EffectiveRateMultiplier: 0.25, GroupRateMultiplier: 0.25})
	})
	loginCount := 0
	mux.HandleFunc("/api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		loginCount++
		writeJSON(w, 200, map[string]any{"code": 0, "message": "success", "data": map[string]string{"access_token": freshJWT}})
	})
	mux.HandleFunc("/api/v1/channels/available", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "Bearer "+freshJWT {
			w.Header().Set("Content-Type", "application/json")
			// input 1.5e-7, output 6e-7 per-token USD
			fmt.Fprint(w, `{"success":true,"data":[{"name":"c1","platforms":[{"platform":"anthropic","supported_models":[{"name":"gpt-4o-mini","pricing":{"input_price":1.5e-7,"output_price":6e-7}}]}]}]}`)
			return
		}
		writeJSON(w, 401, map[string]any{"code": 401, "message": "Session network fingerprint changed"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := New("s1", srv.URL, "sk-1", staleJWT, "", "admin@test.com", "pass123", "default", srv.Client())
	persisted := ""
	a.SetJWTPersistFn(func(jwt string) { persisted = jwt })

	caps, err := a.ProbeCapabilities(context.Background())
	if err != nil {
		t.Fatalf("ProbeCapabilities: %v", err)
	}
	if !caps.HasUserChannels {
		t.Fatal("HasUserChannels should be true after reactive re-login+retry")
	}
	if a.JWT != freshJWT {
		t.Fatalf("adapter JWT should be refreshed to %q, got %q", freshJWT, a.JWT)
	}
	if loginCount != 1 {
		t.Fatalf("expected 1 re-login, got %d", loginCount)
	}
	if persisted != freshJWT {
		t.Fatalf("persist callback should have received %q, got %q", freshJWT, persisted)
	}

	snap, obs, err := a.FetchRatios(context.Background(), caps)
	if err != nil {
		t.Fatalf("FetchRatios: %v", err)
	}
	if len(obs) == 0 {
		t.Fatal("expected per-model observations after reactive refresh")
	}
	_ = snap
}

// TestGroupsAvailablePopulatesGroupRatios: /api/v1/groups/available (user JWT)
// is the per-group ratio source for non-admin customers. It must populate
// snap.GroupRatios with every group's rate_multiplier.
func TestGroupsAvailablePopulatesGroupRatios(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sub2api/billing", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, billingResp{EffectiveRateMultiplier: 0.15, GroupRateMultiplier: 0.15})
	})
	mux.HandleFunc("/api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"code": 0, "data": map[string]string{"access_token": "jwt-1"}})
	})
	mux.HandleFunc("/api/v1/groups/available", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer jwt-1" {
			writeJSON(w, 401, map[string]any{"message": "auth"})
			return
		}
		fmt.Fprint(w, `{"code":0,"data":[{"name":"default","rate_multiplier":0.15,"status":"active"},{"name":"pro","rate_multiplier":0.3,"status":"active"}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := New("s1", srv.URL, "sk-1", "jwt-1", "", "a@b.com", "pw", "default", srv.Client())
	caps, _ := a.ProbeCapabilities(context.Background())
	if !caps.HasUserGroups {
		t.Fatal("HasUserGroups should be true")
	}
	snap, _, err := a.FetchRatios(context.Background(), caps)
	if err != nil {
		t.Fatalf("FetchRatios: %v", err)
	}
	if len(snap.GroupRatios) != 2 {
		t.Fatalf("expected 2 group ratios, got %d (%v)", len(snap.GroupRatios), snap.GroupRatios)
	}
	if snap.GroupRatios["default"] != 0.15 || snap.GroupRatios["pro"] != 0.3 {
		t.Fatalf("group ratios wrong: %v", snap.GroupRatios)
	}
}

// TestUserProfilePopulatesBalance: /api/v1/user/profile (user JWT) is the
// sub2api balance source. balance/frozen_balance are USD and must populate
// caps.{HasQuota,QuotaRemaining,QuotaUsed} without a QuotaPerUnit conversion.
func TestUserProfilePopulatesBalance(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sub2api/billing", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, billingResp{EffectiveRateMultiplier: 0.15, GroupRateMultiplier: 0.15})
	})
	mux.HandleFunc("/api/v1/groups/available", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":0,"data":[{"name":"default","rate_multiplier":0.15}]}`)
	})
	mux.HandleFunc("/api/v1/channels/available", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, channelsAvailableResp{Success: true})
	})
	mux.HandleFunc("/api/v1/user/profile", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer jwt-1" {
			writeJSON(w, 401, map[string]any{"message": "auth"})
			return
		}
		fmt.Fprint(w, `{"code":0,"message":"","data":{"id":1,"balance":42.50,"frozen_balance":3.25}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := New("s1", srv.URL, "sk-1", "jwt-1", "", "", "", "default", srv.Client())
	caps, err := a.ProbeCapabilities(context.Background())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !caps.HasQuota {
		t.Fatal("HasQuota should be true")
	}
	if !eq(caps.QuotaRemaining, 42.50) {
		t.Errorf("QuotaRemaining: want 42.50 got %v", caps.QuotaRemaining)
	}
	// frozen_balance (3.25) is reserved credit, NOT consumed usage — must NOT be
	// mapped into QuotaUsed, which would mislabel reserved funds as spent.
	if caps.QuotaUsed != 0 {
		t.Errorf("QuotaUsed: want 0 (frozen_balance is reserved, not used), got %v", caps.QuotaUsed)
	}
	// NewBalanceFromCaps must keep sub2api values in USD (no /QuotaPerUnit math).
	ob := domain.NewBalanceFromCaps(caps, time.Time{}, "/api/v1/user/profile")
	if ob.Currency != "USD" || !eq(ob.RemainingUSD, 42.50) || ob.UsedUSD != 0 {
		t.Errorf("balance observation wrong: %+v", ob)
	}
}
