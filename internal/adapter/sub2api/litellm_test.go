package sub2api

import (
	"context"
	"net/http"
	"testing"
)

func TestLitellmPrice_VendoredFallback(t *testing.T) {
	// runtimePrices is nil in tests (no refresher started) → vendored fallback.
	// gpt-4o-mini is in the embedded litellm.json sample.
	lp, ok := litellmPrice("gpt-4o-mini")
	if !ok {
		t.Fatal("expected gpt-4o-mini in vendored fallback")
	}
	if lp.InputCostPerToken <= 0 {
		t.Fatalf("expected positive input cost, got %v", lp.InputCostPerToken)
	}
	if _, ok := litellmPrice("definitely-not-a-real-model-xyz"); ok {
		t.Fatal("unknown model should not be found")
	}
}

func TestParseLiteLLMBody_SkipsNonConforming(t *testing.T) {
	// Simulates the real file: a "sample_spec" schema doc (no cost fields),
	// an image-only model (no output), and two real chat models.
	body := []byte(`{
		"sample_spec": {"mode":"chat","litellm_provider":"openai","supports_tool_choice":true},
		"gpt-5.6-sol": {"input_cost_per_token": 0.000005, "output_cost_per_token": 0.00003, "litellm_provider":"openai"},
		"gpt-image-1": {"input_cost_per_token": 0.000005, "litellm_provider":"openai"},
		"zero-cost-entry": {"input_cost_per_token": 0, "output_cost_per_token": 0}
	}`)
	res, err := parseLiteLLMBody(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 conforming models (gpt-5.6-sol, gpt-image-1), got %d: %v", len(res), res)
	}
	if lp, ok := res["gpt-5.6-sol"]; !ok || lp.OutputCostPerToken != 0.00003 {
		t.Fatalf("gpt-5.6-sol wrong: %v ok=%v", lp, ok)
	}
	if _, ok := res["sample_spec"]; ok {
		t.Fatal("sample_spec (no cost fields) must be skipped")
	}
	if _, ok := res["zero-cost-entry"]; ok {
		t.Fatal("zero-cost entry must be skipped")
	}
}

func TestLitellmPrice_RuntimeOverridesVendored(t *testing.T) {
	// Populate runtime table; runtime lookup wins, vendored still serves its own.
	parsed, err := parseLiteLLMBody([]byte(`{"m1":{"input_cost_per_token":0.001,"output_cost_per_token":0.002}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	runtimeMu.Lock()
	runtimePrices = parsed
	runtimeMu.Unlock()
	defer func() { runtimeMu.Lock(); runtimePrices = nil; runtimeMu.Unlock() }()

	if lp, ok := litellmPrice("m1"); !ok || lp.InputCostPerToken != 0.001 {
		t.Fatalf("runtime lookup failed: %v ok=%v", lp, ok)
	}
	// vendored model not in runtime → served by vendored fallback
	if _, ok := litellmPrice("gpt-4o-mini"); !ok {
		t.Fatal("vendored fallback should still serve gpt-4o-mini when runtime lacks it")
	}
}

func TestStartLiteLLMRefresher_ContextCancel(t *testing.T) {
	// Ensures the refresher exits cleanly on ctx cancel (no blocking).
	ctx, cancel := context.WithCancel(context.Background())
	go StartLiteLLMRefresher(ctx, &http.Client{Timeout: 1}, 1)
	cancel()
}
