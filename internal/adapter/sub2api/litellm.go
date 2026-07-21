package sub2api

import (
	_ "embed"
	"encoding/json"

	"transitmonitor/internal/normalize"
)

//go:embed litellm.json
var litellmJSON []byte

// litellmModel is the subset of the LiteLLM model_prices entry we use.
type litellmModel struct {
	InputCostPerToken  float64 `json:"input_cost_per_token"`
	OutputCostPerToken float64 `json:"output_cost_per_token"`
}

// litellmPrices is loaded once from the embedded JSON. This is a small v1
// sample; replace internal/adapter/sub2api/litellm.json with the full LiteLLM
// model_prices_and_context_window.json for production coverage.
var litellmPrices = func() map[string]litellmModel {
	m := map[string]litellmModel{}
	_ = json.Unmarshal(litellmJSON, &m)
	return m
}()

type modelEntry struct {
	ID string `json:"id"`
}

// modelsListResp is the OpenAI-compatible GET /v1/models shape.
type modelsListResp struct {
	Object string       `json:"object"`
	Data   []modelEntry `json:"data"`
}

// modelsFromLiteLLM builds sub2api models from a /v1/models name list, using
// vendored LiteLLM per-token USD prices as the base. Used as a fallback when
// the station's channels/available yields no per-model prices.
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
		if lp, ok := litellmPrices[name]; ok {
			m.InputCostPerToken = lp.InputCostPerToken
			m.OutputCostPerToken = lp.OutputCostPerToken
			m.BasePriceKnown = true
		}
		out = append(out, m)
	}
	return out
}
