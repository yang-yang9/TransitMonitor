package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"transitmonitor/internal/domain"
	"transitmonitor/internal/store"
)

func TestMetrics(t *testing.T) {
	srv, st, cleanup := newDash(t, "")
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	_ = st.InsertRatioObservations(ctx, []domain.RatioObservation{
		{StationID: "s1", GroupName: "default", ModelName: "gpt-4o", InputUSDPer1M: 2.5, OutputUSDPer1M: 10, ObservedAt: now},
	})
	_ = st.InsertProbeResult(ctx, domain.ProbeResult{StationID: "s1", Model: "gpt-4o", MarkupPct: 5, ObservedAt: now})

	// /metrics bypasses auth (non-local RemoteAddr still allowed).
	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if r.Code != 200 {
		t.Fatalf("want 200 got %d", r.Code)
	}
	body := r.Body.String()
	if !strings.Contains(body, "transitmonitor_input_usd_per_1m{station=\"s1\",group=\"default\",model=\"gpt-4o\"}") {
		t.Errorf("missing input gauge line:\n%s", body)
	}
	if !strings.Contains(body, " 2.5\n") {
		t.Errorf("missing value 2.5:\n%s", body)
	}
	if !strings.Contains(body, "transitmonitor_probe_markup_pct{station=\"s1\",model=\"gpt-4o\"}") {
		t.Errorf("missing probe markup gauge:\n%s", body)
	}
	if !strings.Contains(body, "# TYPE transitmonitor_input_usd_per_1m gauge") {
		t.Errorf("missing TYPE line:\n%s", body)
	}
}

func TestMatrixHTML(t *testing.T) {
	srv, st, cleanup := newDash(t, "")
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	_ = st.InsertRatioObservations(ctx, []domain.RatioObservation{
		{StationID: "s1", GroupName: "default", ModelName: "gpt-4o", InputUSDPer1M: 2.5, OutputUSDPer1M: 10, ObservedAt: now},
	})

	// model mode renders the model × station matrix
	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, localReq(http.MethodGet, "/matrix?mode=model&field=input"))
	if r.Code != 200 {
		t.Fatalf("want 200 got %d", r.Code)
	}
	body := r.Body.String()
	if !strings.Contains(body, "<table") || !strings.Contains(body, "gpt-4o") || !strings.Contains(body, "2.5000") {
		t.Errorf("matrix (model) HTML unexpected:\n%s", body)
	}
}

func TestMatrixGroupHTML(t *testing.T) {
	srv, st, cleanup := newDash(t, "")
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	// store a snapshot carrying group_ratios (group mode reads from LatestGroupRatios)
	_ = st.InsertSnapshot(ctx, domain.RawSnapshot{
		StationID: "s1", ObservedAt: now, GroupRatios: map[string]float64{"vip": 0.05, "gptpro": 0.12},
	})

	// group mode is the default; renders the group × station matrix
	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, localReq(http.MethodGet, "/matrix"))
	if r.Code != 200 {
		t.Fatalf("want 200 got %d", r.Code)
	}
	body := r.Body.String()
	if !strings.Contains(body, "vip") || !strings.Contains(body, "0.05x") || !strings.Contains(body, "0.12x") {
		t.Errorf("matrix (group) HTML unexpected:\n%s", body)
	}
}

// TestMatrixModelGroupFilter covers the model-mode group behavior: by default
// each cell shows the cheapest group + its name, and ?group=… narrows to one group.
func TestMatrixModelGroupFilter(t *testing.T) {
	srv, st, cleanup := newDash(t, "")
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	_ = st.InsertRatioObservations(ctx, []domain.RatioObservation{
		{StationID: "s1", GroupName: "vip", ModelName: "gpt-4o", InputUSDPer1M: 2.0, ObservedAt: now},
		{StationID: "s1", GroupName: "std", ModelName: "gpt-4o", InputUSDPer1M: 3.0, ObservedAt: now},
		{StationID: "s2", GroupName: "std", ModelName: "gpt-4o", InputUSDPer1M: 4.0, ObservedAt: now},
	})

	// default = cheapest per station; s1 → vip 2.0 (tagged), s2 → std 4.0 (tagged)
	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, localReq(http.MethodGet, "/matrix?mode=model&field=input"))
	if r.Code != 200 {
		t.Fatalf("want 200 got %d", r.Code)
	}
	body := r.Body.String()
	for _, want := range []string{"2.0000", "4.0000"} {
		if !strings.Contains(body, want) {
			t.Errorf("cheapest mode: missing %q in body", want)
		}
	}
	// Source-group attribution: the cheapest cell must tag its group structurally
	// next to the price (not just appear somewhere via the selector chips).
	if !strings.Contains(body, `2.0000</span><span class="cell-grp">vip</span>`) {
		t.Errorf("cheapest mode: s1 cell should tag source group vip next to 2.0000:\n%s", body)
	}
	if !strings.Contains(body, `4.0000</span><span class="cell-grp">std</span>`) {
		t.Errorf("cheapest mode: s2 cell should tag source group std next to 4.0000:\n%s", body)
	}
	// s1's std price (3.0) must NOT appear — it was collapsed away by the cheapest pick
	if strings.Contains(body, "3.0000") {
		t.Errorf("cheapest mode: s1 std price 3.0000 should not be rendered:\n%s", body)
	}
	// sub.matrixmodel subtitle is rendered in model mode
	if !strings.Contains(body, "模型价格跨站对比") {
		t.Errorf("cheapest mode: sub.matrixmodel subtitle missing")
	}

	// ?group=std narrows: s1 → 3.0, s2 → 4.0; NO per-cell group tag (selector states it)
	r2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r2, localReq(http.MethodGet, "/matrix?mode=model&field=input&group=std"))
	if r2.Code != 200 {
		t.Fatalf("want 200 got %d", r2.Code)
	}
	body2 := r2.Body.String()
	for _, want := range []string{"3.0000", "4.0000"} {
		if !strings.Contains(body2, want) {
			t.Errorf("group=std: missing %q in body", want)
		}
	}
	if strings.Contains(body2, `class="cell-grp"`) {
		t.Errorf("group=std: per-cell group tag should be omitted (selector already states group):\n%s", body2)
	}
	// s1's vip price (2.0) must NOT appear under the std filter
	if strings.Contains(body2, "2.0000") {
		t.Errorf("group=std: vip price 2.0000 should not be rendered:\n%s", body2)
	}
}

// TestMatrixGroupSort verifies the group×station matrix row ordering across the
// three sort modes (median ratio / name / coverage). A 3-station server is built
// locally (newDash only registers s1/s2) so zebra carries an outlier and median
// ≠ mean — locking in the median-sort design decision. Distinctive group names
// avoid colliding with surrounding HTML, so first-match index reflects row order.
func TestMatrixGroupSort(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sort.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	srv := New([]domain.Station{
		{ID: "s1", Name: "S1", Kind: domain.KindNewAPI, BaseURL: "https://a.example", Enabled: true},
		{ID: "s2", Name: "S2", Kind: domain.KindSub2API, BaseURL: "https://b.example", Enabled: true},
		{ID: "s3", Name: "S3", Kind: domain.KindNewAPI, BaseURL: "https://c.example", Enabled: true},
	}, st, "")
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	// s1: zebra=0.05, alpha=0.20, mango=0.12 ; s2: zebra=0.10, mango=0.30 ; s3: zebra=0.90 (outlier)
	// medians: zebra=[0.05,0.10,0.90]→0.10 (≠ mean 0.35), alpha=0.20, mango=[0.12,0.30]→0.21
	// cov:     zebra=3, mango=2, alpha=1
	for sid, gr := range map[string]map[string]float64{
		"s1": {"zebra": 0.05, "alpha": 0.20, "mango": 0.12},
		"s2": {"zebra": 0.10, "mango": 0.30},
		"s3": {"zebra": 0.90},
	} {
		_ = st.InsertSnapshot(ctx, domain.RawSnapshot{
			StationID: sid, ObservedAt: now, GroupRatios: gr,
		})
	}

	order := func(body string) (int, int, int) {
		return strings.Index(body, ">zebra<"), strings.Index(body, ">alpha<"), strings.Index(body, ">mango<")
	}
	cases := []struct {
		sort   string
		labels []string
	}{
		{"ratio", []string{"zebra", "alpha", "mango"}}, // median asc: 0.10, 0.20, 0.21 (≠ mean order)
		{"name", []string{"alpha", "mango", "zebra"}},  // alphabetical
		{"cov", []string{"zebra", "mango", "alpha"}},   // coverage desc: 3, 2, 1
	}
	for _, c := range cases {
		r := httptest.NewRecorder()
		srv.Handler().ServeHTTP(r, localReq(http.MethodGet, "/matrix?mode=group&sort="+c.sort))
		if r.Code != 200 {
			t.Fatalf("sort=%s: want 200 got %d", c.sort, r.Code)
		}
		body := r.Body.String()
		z, a, m := order(body)
		idx := map[string]int{"zebra": z, "alpha": a, "mango": m}
		prev := -1
		for _, label := range c.labels {
			i := idx[label]
			if i < 0 {
				t.Fatalf("sort=%s: group %q not found in body", c.sort, label)
			}
			if i <= prev {
				t.Errorf("sort=%s: expected %q before next, got index %d (prev %d)\n%s", c.sort, label, i, prev, body)
			}
			prev = i
		}
		// sort selector + group-mode subtitle render for every sort mode
		for _, want := range []string{"倍率", "名称", "覆盖", "分组倍率跨站对比"} {
			if !strings.Contains(body, want) {
				t.Errorf("sort=%s: missing %q in body", c.sort, want)
			}
		}
	}
}

func TestChangesHTML(t *testing.T) {
	srv, st, cleanup := newDash(t, "")
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	_ = st.InsertChangeEvents(ctx, []domain.ChangeEvent{{StationID: "s1", Group: "default", Model: "gpt-4o", Field: "input_usd_per_1m", DeltaPct: 25, Severity: "critical", ObservedAt: now}})

	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, localReq(http.MethodGet, "/changes?station=s1"))
	if r.Code != 200 {
		t.Fatalf("want 200 got %d", r.Code)
	}
	if !strings.Contains(r.Body.String(), "gpt-4o") || !strings.Contains(r.Body.String(), "critical") {
		t.Errorf("changes HTML unexpected:\n%s", r.Body.String())
	}
}
