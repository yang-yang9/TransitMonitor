package sub2api

import (
	"context"
	"testing"
)

// Verifies the LiteLLM fallback: with only an sk-key (no JWT → no channels) the
// adapter still derives USD/1M via /v1/models + the vendored LiteLLM prices.

func TestFetchRatios_LiteLLMFallback(t *testing.T) {
	_, a := startMock(t, mockCfg{
		apiKey:  "sk-1",
		jwt:     "", // no user JWT → no channels/available
		group:   "default",
		billing: &billingResp{EffectiveRateMultiplier: 0.25},
		models:  []string{"gpt-4o-mini", "obscure-model"},
	})
	caps, _ := a.ProbeCapabilities(context.Background())
	if !caps.HasBilling {
		t.Fatal("HasBilling should be true")
	}
	if caps.HasUserChannels {
		t.Fatal("HasUserChannels should be false (no JWT)")
	}
	_, obs, err := a.FetchRatios(context.Background(), caps)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	// gpt-4o-mini is in LiteLLM (1.5e-7) → 1.5e-7×1e6×0.25 = 0.0375
	m, ok := findObs(obs, "gpt-4o-mini")
	if !ok {
		t.Fatal("gpt-4o-mini missing")
	}
	if !eq(m.InputUSDPer1M, 0.0375) {
		t.Errorf("gpt-4o-mini input: want 0.0375 got %v", m.InputUSDPer1M)
	}
	// obscure-model not in LiteLLM → missing-base-price
	u, ok := findObs(obs, "obscure-model")
	if !ok {
		t.Fatal("obscure-model missing")
	}
	if u.Sentinel != "missing-base-price" {
		t.Errorf("obscure-model sentinel: want missing-base-price got %q", u.Sentinel)
	}
	if u.InputUSDPer1M != 0 {
		t.Errorf("missing-base-price input should be 0, got %v", u.InputUSDPer1M)
	}
}
