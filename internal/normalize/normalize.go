// Package normalize holds the pure, I/O-free functions that convert parsed
// upstream ratio data into comparable domain.RatioObservation values
// (effective USD per 1M tokens). It is the heart of cross-station comparison.
//
// Math & sentinels are specified (and test-driven) by
// openspec/changes/add-ratio-monitor-core/specs/normalization/spec.md.
package normalize

import "transitmonitor/internal/domain"

// new-api unit conventions (verified in new-api source; see docs/upstream-contract.md).
const (
	NewAPIDefaultQuotaPerUnit = 500000.0 // $1 = 500000 quota
	NewAPIRatioUnitUSDPer1M   = 2.0      // 1 ratio unit = $2/1M tokens (1 ratio = $0.002/1K)
	SelfUseSentinelRatio      = 37.5     // new-api self-use mode returns 37.5 for unknown models

	// sub2api base prices are USD per token; ×1e6 → USD per 1M tokens.
	Sub2APITokensPerMillion = 1e6
)

// NativeRatioKind values.
const (
	KindNewAPIRatio = "newapi_model_ratio"
	KindNewAPIPrice = "newapi_model_price"
	KindSub2APIRate = "sub2api_rate_multiplier"
)

// Sentinel / label values (carried on RatioObservation.Sentinel or .Note).
const (
	SentinelUnconfigured37_5 = "unconfigured-37.5"
	LabelFixedPricePerCall   = "fixed-price (per-call)"
	LabelSimpleMode          = "declared-unavailable (simple mode)"
	LabelMissingBasePrice    = "missing-base-price"
	NoteCompletionInferred   = "completion_ratio=inferred(1.0)"
)

// NewAPIModel is a parsed new-api per-model entry (from /api/ratio_config
// raw maps or /api/pricing). Pointer fields are nil when the station did not
// report them, which the normalizer treats as "absent" (not "zero").
type NewAPIModel struct {
	Name             string
	QuotaType        int // 0 = per-token, 1 = fixed per-call
	ModelRatio       float64
	ModelPrice       float64
	CompletionRatio  *float64
	CacheRatio       *float64
	CreateCacheRatio *float64
	Group            string
	// KnownRatio is true when the model is present in the station's known
	// ratio map. Under self-use mode, a ratio of 37.5 for an unknown model is
	// a sentinel ("unconfigured"), not a real price.
	KnownRatio bool
}

// NewAPIRatioData holds parsed new-api station-level ratio data.
type NewAPIRatioData struct {
	SelfUseMode    bool
	QuotaPerUnit   float64
	UserGroupRatio map[string]float64 // from /api/user/self/groups (highest priority)
	TopGroupRatio  map[string]float64 // from /api/pricing top-level group_ratio or ratio_config
	Models         []NewAPIModel
}

// NewAPINormalize converts parsed new-api ratio data to normalized observations.
func NewAPINormalize(data NewAPIRatioData) []domain.RatioObservation {
	out := make([]domain.RatioObservation, 0, len(data.Models))
	for _, m := range data.Models {
		gr := resolveNewAPIGroup(data, m.Group)
		obs := domain.RatioObservation{
			GroupName:       m.Group,
			ModelName:       m.Name,
			QuotaType:       m.QuotaType,
			NativeRatioKind: KindNewAPIRatio,
		}
		// 37.5 self-use sentinel: exclude, never treat as a real price.
		if data.SelfUseMode && m.ModelRatio == SelfUseSentinelRatio && !m.KnownRatio {
			obs.NativeRatio = SelfUseSentinelRatio
			obs.Sentinel = SentinelUnconfigured37_5
			out = append(out, obs)
			continue
		}
		// Fixed per-call billing (quota_type=1): USD/1M is not derivable.
		if m.QuotaType == 1 {
			obs.NativeRatio = m.ModelPrice
			obs.NativeRatioKind = KindNewAPIPrice
			obs.FixedPriceUSD = m.ModelPrice * gr
			obs.Sentinel = LabelFixedPricePerCall
			out = append(out, obs)
			continue
		}
		// Per-token billing.
		cr := 1.0
		if m.CompletionRatio != nil {
			cr = *m.CompletionRatio
		} else {
			obs.Note = NoteCompletionInferred
		}
		input := m.ModelRatio * NewAPIRatioUnitUSDPer1M * gr
		output := m.ModelRatio * cr * NewAPIRatioUnitUSDPer1M * gr
		cacheRead := input // cache_ratio absent → equals input
		if m.CacheRatio != nil {
			cacheRead = m.ModelRatio * (*m.CacheRatio) * NewAPIRatioUnitUSDPer1M * gr
		}
		cacheWrite := 0.0
		if m.CreateCacheRatio != nil {
			cacheWrite = m.ModelRatio * (*m.CreateCacheRatio) * NewAPIRatioUnitUSDPer1M * gr
		}
		obs.NativeRatio = m.ModelRatio
		obs.InputUSDPer1M = input
		obs.OutputUSDPer1M = output
		obs.CacheReadUSDPer1M = cacheRead
		obs.CacheWriteUSDPer1M = cacheWrite
		obs.CompletionRatio = cr
		out = append(out, obs)
	}
	return out
}

// resolveNewAPIGroup applies the group_ratio priority: user groups > top-level > 1.0.
func resolveNewAPIGroup(data NewAPIRatioData, group string) float64 {
	if data.UserGroupRatio != nil {
		if r, ok := data.UserGroupRatio[group]; ok {
			return r
		}
	}
	if data.TopGroupRatio != nil {
		if r, ok := data.TopGroupRatio[group]; ok {
			return r
		}
	}
	return 1.0
}

// Sub2APIModel is a parsed sub2api per-(group,model) entry. The effective
// multiplier is resolved_rate × applied_peak (the latter is 1.0 outside peak).
type Sub2APIModel struct {
	Name                   string
	Group                  string
	ResolvedRateMultiplier float64
	AppliedPeakMultiplier  float64
	PeakInfo               string
	InputCostPerToken      float64
	OutputCostPerToken     float64
	CacheReadCostPerToken  float64
	CacheWriteCostPerToken float64
	// BasePriceKnown is false when the model has no channel override and is
	// absent from LiteLLM → USD/1M is not derivable (label missing-base-price).
	BasePriceKnown bool
}

// Sub2APIRatioData holds parsed sub2api station-level ratio data.
type Sub2APIRatioData struct {
	SimpleMode bool // /v1/sub2api/billing returned 404
	Models     []Sub2APIModel
}

// Sub2APINormalize converts parsed sub2api ratio data to normalized observations.
func Sub2APINormalize(data Sub2APIRatioData) []domain.RatioObservation {
	out := make([]domain.RatioObservation, 0, len(data.Models))
	for _, m := range data.Models {
		obs := domain.RatioObservation{
			GroupName:       m.Group,
			ModelName:       m.Name,
			QuotaType:       -1, // not applicable to sub2api
			NativeRatioKind: KindSub2APIRate,
			PeakInfo:        m.PeakInfo,
		}
		if data.SimpleMode {
			obs.DeclaredUnavailable = true
			obs.Sentinel = LabelSimpleMode
			out = append(out, obs)
			continue
		}
		eff := m.ResolvedRateMultiplier * m.AppliedPeakMultiplier
		obs.NativeRatio = eff
		if !m.BasePriceKnown {
			obs.Sentinel = LabelMissingBasePrice
			out = append(out, obs)
			continue
		}
		obs.InputUSDPer1M = m.InputCostPerToken * Sub2APITokensPerMillion * eff
		obs.OutputUSDPer1M = m.OutputCostPerToken * Sub2APITokensPerMillion * eff
		obs.CacheReadUSDPer1M = m.CacheReadCostPerToken * Sub2APITokensPerMillion * eff
		obs.CacheWriteUSDPer1M = m.CacheWriteCostPerToken * Sub2APITokensPerMillion * eff
		out = append(out, obs)
	}
	return out
}
