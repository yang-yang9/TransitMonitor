package dashboard

import (
	"testing"

	"transitmonitor/internal/domain"
	"transitmonitor/internal/normalize"
)

// Regression: a base_url typed with a trailing slash (e.g. "https://r.example/")
// must be normalized to no trailing slash, otherwise every polling request is
// built as base + "/v1/..." and the double slash 404s the upstream relay.
func TestStationInputNormalizesBaseURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://r.example/", "https://r.example"},
		{"https://r.example//", "https://r.example"},
		{"  https://r.example/  ", "https://r.example"},
		{"https://r.example", "https://r.example"},          // already clean
		{"https://r.example/sub/", "https://r.example/sub"}, // path kept, trailing slash dropped
	}
	for _, c := range cases {
		in := stationInput{ID: "x", Name: "x", Kind: "newapi", BaseURL: c.in, PollInterval: "30s"}
		in.Auth.APIKey = "sk-test"
		st, err := in.toStation()
		if err != nil {
			t.Fatalf("toStation(%q): %v", c.in, err)
		}
		if st.BaseURL != c.want {
			t.Errorf("toStation(%q): got %q want %q", c.in, st.BaseURL, c.want)
		}
	}
}

// effRatios must NOT re-multiply a sub2api observation's NativeRatio by the
// group ratio — sub2api's NativeRatio already IS the effective group rate ×
// peak, so ×gr would square the discount. new-api still folds gr in.
func TestEffRatiosSub2APINoDoubleCount(t *testing.T) {
	sub := domain.RatioObservation{
		NativeRatio: 0.25, NativeRatioKind: normalize.KindSub2APIRate,
		GroupName: "discount",
	}
	ei, eo := effRatios(sub, 0.25 /*gr*/, 1.0 /*cr*/)
	if ei != 0.25 || eo != 0.25 {
		t.Errorf("sub2api eff: want ei=eo=0.25 got ei=%v eo=%v (×gr would give 0.0625)", ei, eo)
	}

	na := domain.RatioObservation{
		NativeRatio: 1.5, NativeRatioKind: normalize.KindNewAPIRatio,
		CompletionRatio: 2.0, GroupName: "default",
	}
	ei, eo = effRatios(na, 0.5, 2.0)
	if ei != 0.75 { // 1.5 × 0.5
		t.Errorf("new-api ei: want 0.75 got %v", ei)
	}
	if eo != 1.5 { // 1.5 × 2.0 × 0.5
		t.Errorf("new-api eo: want 1.5 got %v", eo)
	}
}
