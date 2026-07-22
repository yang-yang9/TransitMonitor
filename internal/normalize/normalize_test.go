package normalize

import (
	"math"
	"testing"

	"transitmonitor/internal/domain"
)

// These tests encode every Scenario in
// openspec/changes/add-ratio-monitor-core/specs/normalization/spec.md.
// TDD: written before/alongside the implementation; each case is a golden
// expected value derived directly from the spec's WHEN/THEN.

func approxEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
func ptr(f float64) *float64     { return &f }

type expect struct {
	in, out, cacheRead, cacheWrite, fixed, native float64
	sentinel, note, kind                          string
}

func checkObs(t *testing.T, o domain.RatioObservation, e expect) {
	t.Helper()
	chk := func(label string, got, want float64) {
		if !approxEq(got, want) {
			t.Errorf("%s: want %v got %v", label, want, got)
		}
	}
	chk("input_usd_per_1m", o.InputUSDPer1M, e.in)
	chk("output_usd_per_1m", o.OutputUSDPer1M, e.out)
	chk("cache_read_usd_per_1m", o.CacheReadUSDPer1M, e.cacheRead)
	chk("cache_write_usd_per_1m", o.CacheWriteUSDPer1M, e.cacheWrite)
	chk("fixed_price_usd", o.FixedPriceUSD, e.fixed)
	chk("native_ratio", o.NativeRatio, e.native)
	if o.Sentinel != e.sentinel {
		t.Errorf("sentinel: want %q got %q", e.sentinel, o.Sentinel)
	}
	if o.Note != e.note {
		t.Errorf("note: want %q got %q", e.note, o.Note)
	}
	if o.NativeRatioKind != e.kind {
		t.Errorf("native_kind: want %q got %q", e.kind, o.NativeRatioKind)
	}
}

func TestNewAPINormalize(t *testing.T) {
	topDefault := map[string]float64{"default": 1.0}

	cases := []struct {
		name string
		data NewAPIRatioData
		want expect
	}{
		{
			name: "per-token gpt-4o ratio1.25 cr4 gr1",
			data: NewAPIRatioData{QuotaPerUnit: 500000, TopGroupRatio: topDefault, Models: []NewAPIModel{{
				Name: "gpt-4o", QuotaType: 0, ModelRatio: 1.25,
				CompletionRatio: ptr(4), Group: "default", KnownRatio: true,
			}}},
			want: expect{in: 2.5, out: 10, cacheRead: 2.5, native: 1.25, kind: "newapi_model_ratio"},
		},
		{
			name: "cache_ratio present uses formula",
			data: NewAPIRatioData{QuotaPerUnit: 500000, TopGroupRatio: topDefault, Models: []NewAPIModel{{
				Name: "gpt-4o", QuotaType: 0, ModelRatio: 1.25,
				CompletionRatio: ptr(4), CacheRatio: ptr(0.5), Group: "default", KnownRatio: true,
			}}},
			want: expect{in: 2.5, out: 10, cacheRead: 1.25, native: 1.25, kind: "newapi_model_ratio"},
		},
		{
			name: "user group ratio overrides top (vip 0.8)",
			data: NewAPIRatioData{
				QuotaPerUnit:   500000,
				UserGroupRatio: map[string]float64{"vip": 0.8},
				TopGroupRatio:  map[string]float64{"vip": 1.0},
				Models: []NewAPIModel{{
					Name: "gpt-4o", QuotaType: 0, ModelRatio: 1.25,
					CompletionRatio: ptr(4), Group: "vip", KnownRatio: true,
				}},
			},
			want: expect{in: 2.0, out: 8.0, cacheRead: 2.0, native: 1.25, kind: "newapi_model_ratio"},
		},
		{
			name: "no user map, top present",
			data: NewAPIRatioData{QuotaPerUnit: 500000, TopGroupRatio: topDefault, Models: []NewAPIModel{{
				Name: "gpt-4o", QuotaType: 0, ModelRatio: 1.25,
				CompletionRatio: ptr(4), Group: "default", KnownRatio: true,
			}}},
			want: expect{in: 2.5, out: 10, cacheRead: 2.5, native: 1.25, kind: "newapi_model_ratio"},
		},
		{
			name: "no user and no top map defaults to 1.0",
			data: NewAPIRatioData{QuotaPerUnit: 500000, Models: []NewAPIModel{{
				Name: "gpt-4o", QuotaType: 0, ModelRatio: 1.25,
				CompletionRatio: ptr(4), Group: "default", KnownRatio: true,
			}}},
			want: expect{in: 2.5, out: 10, cacheRead: 2.5, native: 1.25, kind: "newapi_model_ratio"},
		},
		{
			name: "completion_ratio missing inferred 1.0",
			data: NewAPIRatioData{QuotaPerUnit: 500000, TopGroupRatio: topDefault, Models: []NewAPIModel{{
				Name: "m", QuotaType: 0, ModelRatio: 1.25,
				CompletionRatio: nil, Group: "default", KnownRatio: true,
			}}},
			want: expect{in: 2.5, out: 2.5, cacheRead: 2.5, native: 1.25,
				note: "completion_ratio=inferred(1.0)", kind: "newapi_model_ratio"},
		},
		{
			name: "fixed-price dall-e-3 quota_type1",
			data: NewAPIRatioData{QuotaPerUnit: 500000, TopGroupRatio: topDefault, Models: []NewAPIModel{{
				Name: "dall-e-3", QuotaType: 1, ModelPrice: 0.04,
				Group: "default", KnownRatio: true,
			}}},
			want: expect{fixed: 0.04, native: 0.04, sentinel: "fixed-price (per-call)", kind: "newapi_model_price"},
		},
		{
			name: "37.5 sentinel under self-use on, unknown model",
			data: NewAPIRatioData{QuotaPerUnit: 500000, TopGroupRatio: topDefault, SelfUseMode: true, Models: []NewAPIModel{{
				Name: "unknown-model", QuotaType: 0, ModelRatio: 37.5,
				Group: "default", KnownRatio: false,
			}}},
			want: expect{native: 37.5, sentinel: "unconfigured-37.5", kind: "newapi_model_ratio"},
		},
		{
			name: "37.5 real configured under self-use on, known model",
			data: NewAPIRatioData{QuotaPerUnit: 500000, TopGroupRatio: topDefault, SelfUseMode: true, Models: []NewAPIModel{{
				Name: "pricey", QuotaType: 0, ModelRatio: 37.5,
				CompletionRatio: ptr(1), Group: "default", KnownRatio: true,
			}}},
			want: expect{in: 75, out: 75, cacheRead: 75, native: 37.5, kind: "newapi_model_ratio"},
		},
		{
			name: "37.5 under self-use off is normal",
			data: NewAPIRatioData{QuotaPerUnit: 500000, TopGroupRatio: topDefault, SelfUseMode: false, Models: []NewAPIModel{{
				Name: "pricey", QuotaType: 0, ModelRatio: 37.5,
				CompletionRatio: ptr(1), Group: "default", KnownRatio: false,
			}}},
			want: expect{in: 75, out: 75, cacheRead: 75, native: 37.5, kind: "newapi_model_ratio"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NewAPINormalize(c.data)
			if len(got) != 1 {
				t.Fatalf("want 1 observation, got %d", len(got))
			}
			if got[0].ModelName == "" {
				t.Errorf("model_name empty")
			}
			if got[0].GroupName == "" {
				t.Errorf("group_name empty")
			}
			checkObs(t, got[0], c.want)
		})
	}
}

func TestSub2APINormalize(t *testing.T) {
	cases := []struct {
		name string
		data Sub2APIRatioData
		want expect
		// extra sub2api-specific expectations
		peak      string
		declUna   bool
		quotaType int
	}{
		{
			name: "normal model eff0.25",
			data: Sub2APIRatioData{Models: []Sub2APIModel{{
				Name: "gpt-4o-mini", Group: "default",
				ResolvedRateMultiplier: 0.25, AppliedPeakMultiplier: 1.0,
				InputCostPerToken: 1.5e-7, OutputCostPerToken: 6e-7,
				BasePriceKnown: true,
			}}},
			want:      expect{in: 0.0375, out: 0.15, native: 0.25, kind: "sub2api_rate_multiplier"},
			quotaType: -1,
		},
		{
			name: "peak multiplier resolved0.25 applied1.5 -> eff0.375",
			data: Sub2APIRatioData{Models: []Sub2APIModel{{
				Name: "gpt-4o-mini", Group: "default",
				ResolvedRateMultiplier: 0.25, AppliedPeakMultiplier: 1.5,
				PeakInfo:          "peak 09:00-12:00 x1.5",
				InputCostPerToken: 1.5e-7, OutputCostPerToken: 6e-7,
				BasePriceKnown: true,
			}}},
			want:      expect{in: 0.05625, out: 0.225, native: 0.375, kind: "sub2api_rate_multiplier"},
			peak:      "peak 09:00-12:00 x1.5",
			quotaType: -1,
		},
		{
			name: "simple mode billing 404",
			data: Sub2APIRatioData{SimpleMode: true, Models: []Sub2APIModel{{
				Name: "gpt-4o-mini", Group: "default",
				ResolvedRateMultiplier: 0.25, AppliedPeakMultiplier: 1.0,
				InputCostPerToken: 1.5e-7, OutputCostPerToken: 6e-7,
				BasePriceKnown: true,
			}}},
			want:      expect{sentinel: "declared-unavailable (simple mode)", kind: "sub2api_rate_multiplier"},
			declUna:   true,
			quotaType: -1,
		},
		{
			name: "missing base price",
			data: Sub2APIRatioData{Models: []Sub2APIModel{{
				Name: "obscure-model", Group: "default",
				ResolvedRateMultiplier: 0.25, AppliedPeakMultiplier: 1.0,
				BasePriceKnown: false,
			}}},
			want:      expect{native: 0.25, sentinel: "missing-base-price", kind: "sub2api_rate_multiplier"},
			quotaType: -1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Sub2APINormalize(c.data)
			if len(got) != 1 {
				t.Fatalf("want 1 observation, got %d", len(got))
			}
			o := got[0]
			checkObs(t, o, c.want)
			if o.PeakInfo != c.peak {
				t.Errorf("peak_info: want %q got %q", c.peak, o.PeakInfo)
			}
			if o.DeclaredUnavailable != c.declUna {
				t.Errorf("declared_unavailable: want %v got %v", c.declUna, o.DeclaredUnavailable)
			}
			if o.QuotaType != c.quotaType {
				t.Errorf("quota_type: want %d got %d", c.quotaType, o.QuotaType)
			}
		})
	}
}

func BenchmarkNewAPINormalize(b *testing.B) {
	data := NewAPIRatioData{
		QuotaPerUnit:  500000,
		TopGroupRatio: map[string]float64{"default": 1.0, "vip": 0.8},
		Models:        make([]NewAPIModel, 100),
	}
	for i := range data.Models {
		data.Models[i] = NewAPIModel{Name: "model-" + string(rune('a'+i%26)), ModelRatio: 1.25, CompletionRatio: ptr(4), Group: "default", KnownRatio: true}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		NewAPINormalize(data)
	}
}
