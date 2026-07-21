// Package e2e runs an in-process end-to-end self-test: it stands up mock
// new-api and sub2api HTTP stations (served by httptest), drives the REAL
// adapters through the scheduler (scrape→normalize→store→diff→alert), flips a
// ratio, and asserts that ChangeEvents are recorded and alerts fire.
//
// It is invoked by `transitmonitor -selftest` and by `go test ./internal/e2e`.
package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"transitmonitor/internal/adapter"
	"transitmonitor/internal/alert"
	"transitmonitor/internal/domain"
	"transitmonitor/internal/probe"
	"transitmonitor/internal/scheduler"
	"transitmonitor/internal/store"
)

type mockState struct {
	mu          sync.Mutex
	newapiRatio float64
	newapiCR    float64
	subEff      float64
	subInput    float64
	subOutput   float64
}

func (m *mockState) snapshot() (nr, cr, eff, in, out float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.newapiRatio, m.newapiCR, m.subEff, m.subInput, m.subOutput
}

func mockNewAPI(s *mockState) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"success":true,"data":{"quota_per_unit":500000,"self_use_mode_enabled":false,"usd_exchange_rate":7.3}}`)
	})
	mux.HandleFunc("/api/ratio_config", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		fmt.Fprint(w, `{"success":false,"message":"倍率配置接口未启用"}`)
	})
	mux.HandleFunc("/api/pricing", func(w http.ResponseWriter, _ *http.Request) {
		nr, cr, _, _, _ := s.snapshot()
		fmt.Fprintf(w, `{"success":true,"data":[{"model_name":"gpt-4o","quota_type":0,"model_ratio":%v,"completion_ratio":%v}],"group_ratio":{"default":1.0}}`, nr, cr)
	})
	// Probe endpoints: usage returns 0 on first call, 0.003 after (delta_quota=15 → markup 0).
	var usageCalls int32
	mux.HandleFunc("/v1/dashboard/billing/usage", func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&usageCalls, 1)
		val := 0.0
		if n > 1 {
			val = 0.003
		}
		fmt.Fprintf(w, `{"object":"list","total_usage":%v}`, val)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"id":"x","object":"chat.completion","choices":[],"usage":{"prompt_tokens":8,"completion_tokens":1}}`)
	})
	return httptest.NewServer(mux)
}

func mockSub2API(s *mockState) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sub2api/billing", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(401)
			return
		}
		_, _, eff, _, _ := s.snapshot()
		fmt.Fprintf(w, `{"object":"sub2api.key_billing","effective_rate_multiplier":%v,"group_rate_multiplier":%v,"resolved_rate_multiplier":%v,"peak_rate_enabled":false}`, eff, eff, eff)
	})
	mux.HandleFunc("/api/v1/channels/available", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer jwt-test" {
			w.WriteHeader(401)
			return
		}
		_, _, _, in, out := s.snapshot()
		fmt.Fprintf(w, `{"success":true,"data":[{"name":"c1","platforms":[{"platform":"anthropic","supported_models":[{"name":"gpt-4o-mini","pricing":{"input_price":%v,"output_price":%v}}]}]}]}`, in, out)
	})
	return httptest.NewServer(mux)
}

// Run executes the end-to-end self-test and returns nil on success.
func Run() error {
	state := &mockState{newapiRatio: 1.25, newapiCR: 4, subEff: 0.25, subInput: 1.5e-7, subOutput: 6e-7}
	newSrv := mockNewAPI(state)
	subSrv := mockSub2API(state)
	defer newSrv.Close()
	defer subSrv.Close()

	dir, err := os.MkdirTemp("", "tm-e2e-*")
	if err != nil {
		return fmt.Errorf("tempdir: %w", err)
	}
	st, err := store.Open(filepath.Join(dir, "tm.db"))
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	stations := []domain.Station{
		{ID: "na", Name: "NewAPI mock", Kind: domain.KindNewAPI, BaseURL: newSrv.URL,
			Auth:         domain.AuthConfig{APIKey: "sk-test", Group: "default"},
			Enabled:      true,
			PollInterval: domain.Duration(2 * time.Minute),
			Probe: domain.ProbeConfig{Enabled: true, Model: "gpt-4o",
				MaxInputTokens: 8, MaxOutputTokens: 1, DryRun: false}},
		{ID: "sb", Name: "Sub2API mock", Kind: domain.KindSub2API, BaseURL: subSrv.URL,
			Auth:         domain.AuthConfig{APIKey: "sk-test", JWT: "jwt-test", Group: "default"},
			Enabled:      true,
			PollInterval: domain.Duration(2 * time.Minute)},
	}
	adapters := map[string]adapter.Adapter{}
	for _, s := range stations {
		a, err := adapter.NewAdapter(s, http.DefaultClient)
		if err != nil {
			return err
		}
		adapters[s.ID] = a
	}
	sink := &alert.SinkNotifier{}
	rules := []alert.Rule{{Name: "delta5", Type: alert.RuleDeltaPct, Threshold: 5, Enabled: true}}
	sched := scheduler.New(stations, adapters, st, rules, sink)
	sched.Prober = probe.NewProber(http.DefaultClient)
	ctx := context.Background()

	// First poll: baseline.
	if err := sched.PollOnce(ctx, "na"); err != nil {
		return fmt.Errorf("na poll1: %w", err)
	}
	if err := sched.PollOnce(ctx, "sb"); err != nil {
		return fmt.Errorf("sb poll1: %w", err)
	}
	naObs, _ := st.LatestRatioObservations(ctx, "na")
	if got := findIn(naObs, "gpt-4o"); got == nil || got.InputUSDPer1M != 2.5 {
		return fmt.Errorf("na baseline: want gpt-4o input 2.5 (1.25×2×1), got %+v", naObs)
	}
	sbObs, _ := st.LatestRatioObservations(ctx, "sb")
	if got := findIn(sbObs, "gpt-4o-mini"); got == nil || got.InputUSDPer1M != 0.0375 {
		return fmt.Errorf("sb baseline: want gpt-4o-mini input 0.0375 (1.5e-7×1e6×0.25), got %+v", sbObs)
	}

	// Flip ratios: new-api 1.25→1.5 (in 2.5→3.0, +20%); sub2api eff 0.25→0.5 (in 0.0375→0.075, +100%).
	state.mu.Lock()
	state.newapiRatio = 1.5
	state.subEff = 0.5
	state.mu.Unlock()

	if err := sched.PollOnce(ctx, "na"); err != nil {
		return fmt.Errorf("na poll2: %w", err)
	}
	if err := sched.PollOnce(ctx, "sb"); err != nil {
		return fmt.Errorf("sb poll2: %w", err)
	}

	naEv, _ := st.ListChangeEvents(ctx, "na", 10)
	sbEv, _ := st.ListChangeEvents(ctx, "sb", 10)
	if len(naEv) == 0 {
		return fmt.Errorf("na: expected change events after flip, got 0")
	}
	if len(sbEv) == 0 {
		return fmt.Errorf("sb: expected change events after flip, got 0")
	}
	if len(sink.Sent) < 2 {
		return fmt.Errorf("expected ≥2 alerts fired (na+sb), got %d", len(sink.Sent))
	}

	// Real-cost probe ran on the new-api station (gpt-4o) and recorded a result.
	prs, _ := st.ListProbeResults(ctx, "na", 10)
	if len(prs) == 0 {
		return fmt.Errorf("expected probe results for station na, got 0")
	}
	// declared gpt-4o ratio 1.25 → input 2.5; mock delta_quota=15 → measured_RG=1.25 → markup 0
	if d := prs[0].MarkupPct; d < -1e-6 || d > 1e-6 {
		return fmt.Errorf("probe markup: want ~0 (no hidden markup in mock), got %v", prs[0].MarkupPct)
	}

	fmt.Printf("self-test OK: na events=%d, sb events=%d, alerts fired=%d, probe results=%d (markup=%.2f%%)\n",
		len(naEv), len(sbEv), len(sink.Sent), len(prs), prs[0].MarkupPct)
	return nil
}

func findIn(obs []domain.RatioObservation, model string) *domain.RatioObservation {
	for i := range obs {
		if obs[i].ModelName == model {
			return &obs[i]
		}
	}
	return nil
}
