package probe

import (
	"math"
	"testing"
)

func eq(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestReconcileNewAPI_PerToken(t *testing.T) {
	// delta_quota=1500, P=100, C=1, cr=4, mr=1.25, gr=1
	mRG, mIn, markup := ReconcileNewAPI(1500, 100, 1, 4, 1.25, 1)
	wantRG := 1500.0 / 104.0
	if !eq(mRG, wantRG) {
		t.Errorf("measured_RG: want %v got %v", wantRG, mRG)
	}
	if !eq(mIn, wantRG*2) {
		t.Errorf("measured_input: want %v got %v", wantRG*2, mIn)
	}
	wantMarkup := (wantRG - 1.25*1) / (1.25 * 1) * 100
	if !eq(markup, wantMarkup) {
		t.Errorf("markup_pct: want %v got %v", wantMarkup, markup)
	}
}

func TestReconcileNewAPI_Fixed(t *testing.T) {
	// delta_quota=20000, model_price=0.04, gr=1 → measured_per_call=0.04, markup=0
	perCall, markup := ReconcileNewAPIFixed(20000, 0.04, 1)
	if !eq(perCall, 0.04) {
		t.Errorf("measured_per_call: want 0.04 got %v", perCall)
	}
	if !eq(markup, 0) {
		t.Errorf("markup_pct: want 0 got %v", markup)
	}
}

func TestReconcileSub2API(t *testing.T) {
	// delta_actual_cost=4e-6, P=100, C=1, base_in=1.5e-7, base_out=6e-7, eff_m=0.25
	mEff, mIn, markup, derivable := ReconcileSub2API(4e-6, 100, 1, 1.5e-7, 6e-7, 0.25)
	baseSum := 100*1.5e-7 + 1*6e-7
	wantEff := 4e-6 / baseSum
	if !eq(mEff, wantEff) {
		t.Errorf("measured_eff: want %v got %v", wantEff, mEff)
	}
	if !eq(mIn, 1.5e-7*1e6*wantEff) {
		t.Errorf("measured_input: want %v got %v", 1.5e-7*1e6*wantEff, mIn)
	}
	if !derivable {
		t.Error("markup should be derivable when effM known")
	}
	wantMarkup := (wantEff - 0.25) / 0.25 * 100
	if !eq(markup, wantMarkup) {
		t.Errorf("markup_pct: want %v got %v", wantMarkup, markup)
	}
}

func TestReconcileSub2API_SimpleModeNotDerivable(t *testing.T) {
	// effM=0 (simple mode) → markup not derivable, but measured_eff still computed.
	mEff, _, markup, derivable := ReconcileSub2API(4e-6, 100, 1, 1.5e-7, 6e-7, 0)
	if derivable {
		t.Error("markup should NOT be derivable in simple mode (effM=0)")
	}
	if markup != 0 {
		t.Errorf("markup should be 0 when not derivable, got %v", markup)
	}
	if mEff == 0 {
		t.Error("measured_eff should still be computed in simple mode")
	}
}
