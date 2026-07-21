// Package probe reconciles a real-cost probe's charged-quota delta against the
// station's declared ratios to derive the TRUE effective price and the markup
// (hidden surcharge). The math is pure and test-driven from
// openspec/.../specs/real-cost-probe/spec.md.
//
// new-api (per-token):  measured_RG = deltaQuota / (P + C*cr)
//
//	measured_input = measured_RG * 2   (1 ratio = $2/1M)
//	markup_pct = (measured_RG - mr*gr) / (mr*gr) * 100
//
// new-api (fixed):      measured_per_call = deltaQuota / QuotaPerUnit
//
//	markup_pct = (measured - modelPrice*gr) / (modelPrice*gr) * 100
//
// sub2api:              measured_eff = deltaActualCost / (P*baseIn + C*baseOut)
//
//	measured_input = baseIn * 1e6 * measured_eff
//	markup_pct = (measured_eff - effM) / effM * 100   (when effM known)
package probe

import "transitmonitor/internal/normalize"

// ReconcileNewAPI reconciles a per-token new-api probe.
func ReconcileNewAPI(deltaQuota, P, C, cr, mr, gr float64) (measuredRG, measuredInputUSDPer1M, markupPct float64) {
	denom := P + C*cr
	if denom == 0 {
		return 0, 0, 0
	}
	measuredRG = deltaQuota / denom
	measuredInputUSDPer1M = measuredRG * normalize.NewAPIRatioUnitUSDPer1M
	if declared := mr * gr; declared != 0 {
		markupPct = (measuredRG - declared) / declared * 100
	}
	return
}

// ReconcileNewAPIFixed reconciles a fixed per-call new-api probe.
func ReconcileNewAPIFixed(deltaQuota, modelPrice, gr float64) (measuredPerCall, markupPct float64) {
	measuredPerCall = deltaQuota / normalize.NewAPIDefaultQuotaPerUnit
	if declared := modelPrice * gr; declared != 0 {
		markupPct = (measuredPerCall - declared) / declared * 100
	}
	return
}

// ReconcileSub2API reconciles a sub2api probe. markupDerivable is false when
// effM is unknown (simple mode); measured_eff is still computed from actual_cost.
func ReconcileSub2API(deltaActualCost, P, C, baseIn, baseOut, effM float64) (measuredEffM, measuredInputUSDPer1M, markupPct float64, markupDerivable bool) {
	baseSum := P*baseIn + C*baseOut
	if baseSum == 0 {
		return 0, 0, 0, false
	}
	measuredEffM = deltaActualCost / baseSum
	measuredInputUSDPer1M = baseIn * normalize.Sub2APITokensPerMillion * measuredEffM
	markupDerivable = effM != 0
	if markupDerivable {
		markupPct = (measuredEffM - effM) / effM * 100
	}
	return
}
