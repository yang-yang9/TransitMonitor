package sub2api

import (
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"transitmonitor/internal/normalize"
)

//go:embed litellm.json
var litellmJSON []byte

// litellmModel is the subset of the LiteLLM model_prices entry we use.
type litellmModel struct {
	InputCostPerToken  float64 `json:"input_cost_per_token"`
	OutputCostPerToken float64 `json:"output_cost_per_token"`
}

// vendoredPrices is the small embedded fallback (loaded at init). Used when the
// runtime fetch hasn't completed yet or fails (offline first run).
var vendoredPrices = func() map[string]litellmModel {
	m := map[string]litellmModel{}
	_ = json.Unmarshal(litellmJSON, &m)
	return m
}()

// runtimePrices is the full public LiteLLM price table (~3000 models),
// refreshed ~24h by StartLiteLLMRefresher. RWMutex-protected.
var (
	runtimeMu     sync.RWMutex
	runtimePrices map[string]litellmModel
)

// litellmURL is the public LiteLLM price table. The jsDelivr CDN mirror is used
// (faster/more reliable than raw.githubusercontent in practice); it tracks the
// BerriAI/litellm main branch.
const litellmURL = "https://cdn.jsdelivr.net/gh/BerriAI/litellm@main/model_prices_and_context_window.json"

// litellmPrice looks up a model's per-token USD base price: the runtime table
// (full public LiteLLM) first, then the vendored fallback.
func litellmPrice(name string) (litellmModel, bool) {
	runtimeMu.RLock()
	rt := runtimePrices
	runtimeMu.RUnlock()
	if rt != nil {
		if m, ok := rt[name]; ok {
			return m, true
		}
	}
	m, ok := vendoredPrices[name]
	return m, ok
}

// StartLiteLLMRefresher downloads the full public LiteLLM price table and
// refreshes it every refreshInterval (default 24h). On fetch failure, keeps the
// previous table (or vendored fallback). Runs until ctx cancelled. Safe to
// call once (it mutates the package-level runtimePrices singleton).
func StartLiteLLMRefresher(ctx context.Context, client *http.Client, refreshInterval time.Duration) {
	if client == nil {
		client = http.DefaultClient
	}
	if refreshInterval <= 0 {
		refreshInterval = 24 * time.Hour
	}
	log := slog.Default()
	refresh := func() {
		n, err := refreshLiteLLMTable(client)
		if err != nil {
			log.Warn("litellm table refresh failed; keeping previous", "err", err, "url", litellmURL)
			return
		}
		log.Info("litellm price table refreshed", "models", n)
	}
	refresh()
	t := time.NewTicker(refreshInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			refresh()
		}
	}
}

// refreshLiteLLMTable fetches + parses the LiteLLM table into runtimePrices.
// Returns the number of models loaded. Robust to non-conforming entries
// (sample_spec schema doc, image-only models): parsed per-entry, skipped on
// unmarshal failure or zero cost.
func refreshLiteLLMTable(client *http.Client) (int, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, litellmURL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	out, err := parseLiteLLMBody(body)
	if err != nil {
		return 0, err
	}
	runtimeMu.Lock()
	runtimePrices = out
	runtimeMu.Unlock()
	return len(out), nil
}

// parseLiteLLMBody parses the LiteLLM JSON into a name→price map, skipping
// non-conforming entries (sample_spec schema doc, image-only models, zero-cost).
func parseLiteLLMBody(body []byte) (map[string]litellmModel, error) {
	// Parse as map[string]RawMessage first, then per-entry into litellmModel,
	// skipping entries that don't conform (e.g. the "sample_spec" schema doc).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]litellmModel, len(raw))
	for name, rb := range raw {
		var lm litellmModel
		if json.Unmarshal(rb, &lm) != nil {
			continue
		}
		if lm.InputCostPerToken <= 0 && lm.OutputCostPerToken <= 0 {
			continue
		}
		out[name] = lm
	}
	if len(out) == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	return out, nil
}

type modelEntry struct {
	ID string `json:"id"`
}

// modelsListResp is the OpenAI-compatible GET /v1/models shape.
type modelsListResp struct {
	Object string       `json:"object"`
	Data   []modelEntry `json:"data"`
}

// modelsFromLiteLLM builds sub2api models from a /v1/models name list, using
// the LiteLLM per-token USD prices as the base (runtime table → vendored
// fallback). Used when channels/available yields no per-model prices.
func (a *Adapter) modelsFromLiteLLM(items []modelEntry, effective float64, peakInfo string) []normalize.Sub2APIModel {
	out := make([]normalize.Sub2APIModel, 0, len(items))
	for _, it := range items {
		name := it.ID
		if name == "" {
			continue
		}
		m := normalize.Sub2APIModel{
			Name: name, Group: a.Group,
			ResolvedRateMultiplier: effective, AppliedPeakMultiplier: 1.0,
			PeakInfo: peakInfo,
		}
		if lp, ok := litellmPrice(name); ok {
			m.InputCostPerToken = lp.InputCostPerToken
			m.OutputCostPerToken = lp.OutputCostPerToken
			m.BasePriceKnown = true
		}
		out = append(out, m)
	}
	return out
}
