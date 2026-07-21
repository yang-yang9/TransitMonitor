package probe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"transitmonitor/internal/domain"
)

// mockNewAPIProbe serves /v1/dashboard/billing/usage (U0 then U1) + chat.
func mockNewAPIProbe(t *testing.T, u0, u1 float64) (*httptest.Server, *int32) {
	t.Helper()
	var usageCalls int32
	var chatCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/dashboard/billing/usage", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&usageCalls, 1)
		val := u0
		if n > 1 {
			val = u1
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"total_usage": val})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&chatCalls, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"usage": map[string]int{"prompt_tokens": 8, "completion_tokens": 1},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &chatCalls
}

func declared(model string, input, mr, cr float64) []domain.RatioObservation {
	return []domain.RatioObservation{{
		StationID: "na", GroupName: "default", ModelName: model,
		InputUSDPer1M: input, NativeRatio: mr, CompletionRatio: cr,
	}}
}

func probeStation(srvURL string, dryRun bool, maxCostCents int) domain.Station {
	return domain.Station{
		ID: "na", BaseURL: srvURL, Kind: domain.KindNewAPI,
		Auth: domain.AuthConfig{APIKey: "sk-test", Group: "default"},
		Probe: domain.ProbeConfig{Enabled: true, Model: "gpt-4o-mini",
			MaxInputTokens: 8, MaxOutputTokens: 1, MaxCostCentsPerRun: maxCostCents, DryRun: dryRun},
	}
}

func fixedProber(t *testing.T, srv *httptest.Server) *Prober {
	t.Helper()
	p := NewProber(srv.Client())
	p.Now = func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) }
	return p
}

func TestProber_MarkupZero(t *testing.T) {
	srv, chatCalls := mockNewAPIProbe(t, 0, 0.003) // delta_quota=15 → measured_RG=1.25=declared → markup 0
	p := fixedProber(t, srv)
	res, err := p.Run(context.Background(), probeStation(srv.URL, false, 0), declared("gpt-4o-mini", 2.5, 1.25, 4))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	// measured_RG = 15/(8+1*4)=1.25; measured_input=2.5; markup=0
	if d := res.MeasuredUSDPer1M - 2.5; d < 0 || d > 1e-6 {
		t.Errorf("measured: want 2.5 got %v", res.MeasuredUSDPer1M)
	}
	if d := res.MarkupPct; d < -1e-6 || d > 1e-6 {
		t.Errorf("markup: want 0 got %v", res.MarkupPct)
	}
	if *chatCalls != 1 {
		t.Errorf("chat calls: want 1 got %d", *chatCalls)
	}
}

func TestProber_Markup100(t *testing.T) {
	srv, _ := mockNewAPIProbe(t, 0, 0.006) // delta_quota=30 → measured_RG=2.5 → markup 100%
	p := fixedProber(t, srv)
	res, _ := p.Run(context.Background(), probeStation(srv.URL, false, 0), declared("gpt-4o-mini", 2.5, 1.25, 4))
	if d := res.MeasuredUSDPer1M - 5.0; d < 0 || d > 1e-6 {
		t.Errorf("measured: want 5.0 got %v", res.MeasuredUSDPer1M)
	}
	if d := res.MarkupPct - 100; d < -1e-6 || d > 1e-6 {
		t.Errorf("markup: want 100 got %v", res.MarkupPct)
	}
}

func TestProber_DryRun(t *testing.T) {
	srv, chatCalls := mockNewAPIProbe(t, 0, 0.003)
	p := fixedProber(t, srv)
	st := probeStation(srv.URL, true, 0) // DryRun=true
	res, _ := p.Run(context.Background(), st, declared("gpt-4o-mini", 2.5, 1.25, 4))
	if res.Error != "" {
		t.Errorf("dry-run error: %s", res.Error)
	}
	if *chatCalls != 0 {
		t.Errorf("dry-run must not call chat, got %d", *chatCalls)
	}
	if res.CostUSD <= 0 {
		t.Errorf("dry-run should record declared cost, got %v", res.CostUSD)
	}
}

func TestProber_CostGuardrail(t *testing.T) {
	srv, chatCalls := mockNewAPIProbe(t, 0, 0.003)
	p := fixedProber(t, srv)
	// declared input 100000 USD/1M × 8 tokens / 1e6 = 0.8 USD = 80 cents > 1 cent guardrail.
	st := probeStation(srv.URL, false, 1)
	res, _ := p.Run(context.Background(), st, declared("gpt-4o-mini", 100000, 50000, 4))
	if res.Error != "cost-guardrail-exceeded" {
		t.Errorf("want cost-guardrail-exceeded got %q", res.Error)
	}
	if *chatCalls != 0 {
		t.Errorf("guardrail must not call chat, got %d", *chatCalls)
	}
}

func TestProber_ModelNotAvailable(t *testing.T) {
	srv, _ := mockNewAPIProbe(t, 0, 0.003)
	p := fixedProber(t, srv)
	res, _ := p.Run(context.Background(), probeStation(srv.URL, false, 0), declared("other-model", 2.5, 1.25, 4))
	if res.Error != "model-not-available" {
		t.Errorf("want model-not-available got %q", res.Error)
	}
}
