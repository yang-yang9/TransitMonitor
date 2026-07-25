// prober.go is the real-cost probe HTTP orchestrator. It sends a tiny real
// chat request through a station, reads the charged-quota delta, and reconciles
// it against the declared ratios (via the pure Reconcile* math) to expose
// hidden markup. Spec: openspec/.../specs/real-cost-probe/spec.md.
package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"transitmonitor/internal/domain"
)

// Prober runs real-cost probes. It is safe for concurrent use.
type Prober struct {
	Client  *http.Client
	Now     func() time.Time
	last    map[string]time.Time
	mu      sync.Mutex
	dedupe  time.Duration
	counter uint64
}

// NewProber constructs a Prober (default 10-min dedupe window per station+model).
func NewProber(client *http.Client) *Prober {
	if client == nil {
		client = http.DefaultClient
	}
	return &Prober{Client: client, Now: time.Now, last: map[string]time.Time{}, dedupe: 10 * time.Minute}
}

// SetDedupe overrides the dedupe window (for tests).
func (p *Prober) SetDedupe(d time.Duration) { p.dedupe = d }

// Run executes one probe against the station for the given model, using
// declared (the latest scraped observations) to find the model's declared
// price. Returns a ProbeResult (with .Error set on graceful failures — never a
// hard error unless the HTTP call itself fails).
func (p *Prober) Run(ctx context.Context, st domain.Station, model string, declared []domain.RatioObservation) (domain.ProbeResult, error) {
	res := domain.ProbeResult{StationID: st.ID, Model: model, ObservedAt: p.Now()}

	var dec *domain.RatioObservation
	for i := range declared {
		if declared[i].ModelName == model {
			dec = &declared[i]
			break
		}
	}
	if dec == nil {
		res.Error = "model-not-available"
		return res, nil
	}
	res.DeclaredEffectiveUSDPer1M = dec.InputUSDPer1M
	res.DeclaredNativeRatio = dec.NativeRatio
	if dec.DeclaredUnavailable || dec.Sentinel != "" {
		res.DeclaredUnavailable = dec.DeclaredUnavailable
		res.Error = "declared-unavailable"
		return res, nil
	}

	// Dedupe per (station, model).
	key := st.ID + "|" + model
	p.mu.Lock()
	if last, ok := p.last[key]; ok && p.Now().Sub(last) < p.dedupe {
		p.mu.Unlock()
		res.Error = "deduped"
		return res, nil
	}
	p.last[key] = p.Now()
	p.mu.Unlock()

	// Cost guardrail: estimate declared cost for MaxInputTokens input tokens.
	pEst := st.Probe.MaxInputTokens
	if pEst <= 0 {
		pEst = 8
	}
	declaredCostUSD := dec.InputUSDPer1M * float64(pEst) / 1e6
	if st.Probe.MaxCostCentsPerRun > 0 && declaredCostUSD*100 > float64(st.Probe.MaxCostCentsPerRun) {
		res.Error = "cost-guardrail-exceeded"
		return res, nil
	}
	if st.Probe.DryRun {
		res.CostUSD = declaredCostUSD
		return res, nil
	}

	switch st.Kind {
	case domain.KindNewAPI:
		return p.runNewAPI(ctx, st, model, dec, res, pEst)
	case domain.KindSub2API:
		return p.runSub2API(ctx, st, model, dec, res, pEst)
	default:
		res.Error = "unsupported-kind"
		return res, nil
	}
}

// --- new-api: /v1/dashboard/billing/usage delta (sk-key) ---

type newapiUsageResp struct {
	TotalUsage float64 `json:"total_usage"`
}

type chatResp struct {
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (p *Prober) runNewAPI(ctx context.Context, st domain.Station, model string, dec *domain.RatioObservation, res domain.ProbeResult, pEst int) (domain.ProbeResult, error) {
	u0, err := p.newapiUsage(ctx, st)
	if err != nil {
		return res, err
	}
	P, C, err := p.chat(ctx, st, model, pEst)
	if err != nil {
		res.Error = "chat-failed"
		return res, nil
	}
	u1, err := p.newapiUsage(ctx, st)
	if err != nil {
		return res, err
	}
	if u1 == u0 {
		time.Sleep(500 * time.Millisecond)
		u1, _ = p.newapiUsage(ctx, st)
	}
	deltaQuota := (u1 - u0) * 5000 // (U1-U0) cents × QuotaPerUnit/100
	res.TokensIn, res.TokensOut = P, C
	if deltaQuota == 0 {
		res.Error = "no-quota-delta"
		return res, nil
	}
	// Derive group_ratio from the declared effective input (input = mr×2×gr).
	gr := 1.0
	if dec.NativeRatio != 0 {
		gr = dec.InputUSDPer1M / (dec.NativeRatio * 2)
	}
	_, mIn, markup := ReconcileNewAPI(deltaQuota, float64(P), float64(C), dec.CompletionRatio, dec.NativeRatio, gr)
	res.MeasuredUSDPer1M = mIn
	res.MarkupPct = markup
	res.CostUSD = mIn * float64(P+C) / 1e6
	return res, nil
}

func (p *Prober) newapiUsage(ctx context.Context, st domain.Station) (float64, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, st.BaseURL+"/v1/dashboard/billing/usage", nil)
	req.Header.Set("Authorization", "Bearer "+st.Auth.APIKey)
	resp, err := p.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("usage: status %d", resp.StatusCode)
	}
	var u newapiUsageResp
	if err := json.Unmarshal(body, &u); err != nil {
		return 0, err
	}
	return u.TotalUsage, nil
}

// --- sub2api: /v1/usage actual_cost delta (sk-key) ---

type sub2apiUsageResp struct {
	Total struct {
		ActualCost   float64 `json:"actual_cost"`
		InputTokens  int     `json:"input_tokens"`
		OutputTokens int     `json:"output_tokens"`
	} `json:"total"`
}

func (p *Prober) runSub2API(ctx context.Context, st domain.Station, model string, dec *domain.RatioObservation, res domain.ProbeResult, pEst int) (domain.ProbeResult, error) {
	a0, err := p.sub2apiUsage(ctx, st)
	if err != nil {
		return res, err
	}
	P, C, err := p.chat(ctx, st, model, pEst)
	if err != nil {
		res.Error = "chat-failed"
		return res, nil
	}
	a1, err := p.sub2apiUsage(ctx, st)
	if err != nil {
		return res, err
	}
	delta := a1 - a0
	res.TokensIn, res.TokensOut = P, C
	eff := dec.NativeRatio
	baseIn := 0.0
	baseOut := 0.0
	if eff != 0 {
		baseIn = dec.InputUSDPer1M / (eff * 1e6)
		baseOut = dec.OutputUSDPer1M / (eff * 1e6)
	}
	_, mIn, markup, derivable := ReconcileSub2API(delta, float64(P), float64(C), baseIn, baseOut, eff)
	res.MeasuredUSDPer1M = mIn
	res.MarkupPct = markup
	res.DeclaredUnavailable = !derivable
	res.CostUSD = delta
	return res, nil
}

func (p *Prober) sub2apiUsage(ctx context.Context, st domain.Station) (float64, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, st.BaseURL+"/v1/usage", nil)
	req.Header.Set("Authorization", "Bearer "+st.Auth.APIKey)
	resp, err := p.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("usage: status %d", resp.StatusCode)
	}
	var u sub2apiUsageResp
	if err := json.Unmarshal(body, &u); err != nil {
		return 0, err
	}
	return u.Total.ActualCost, nil
}

// chat sends a tiny non-streaming chat completion and returns prompt/completion tokens.
func (p *Prober) chat(ctx context.Context, st domain.Station, model string, pEst int) (int, int, error) {
	prompt := fmt.Sprintf("probe-%d", atomic.AddUint64(&p.counter, 1))
	maxOut := st.Probe.MaxOutputTokens
	if maxOut <= 0 {
		maxOut = 1
	}
	body, _ := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": maxOut,
		"stream":     false,
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, st.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+st.Auth.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.Client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 404 || resp.StatusCode == 400 {
		return 0, 0, fmt.Errorf("model_not_found")
	}
	if resp.StatusCode != 200 {
		return 0, 0, fmt.Errorf("chat: status %d", resp.StatusCode)
	}
	var cr chatResp
	if err := json.Unmarshal(respBody, &cr); err != nil {
		return 0, 0, err
	}
	return cr.Usage.PromptTokens, cr.Usage.CompletionTokens, nil
}
