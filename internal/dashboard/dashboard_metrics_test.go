package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"transitmonitor/internal/domain"
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

	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, localReq(http.MethodGet, "/matrix"))
	if r.Code != 200 {
		t.Fatalf("want 200 got %d", r.Code)
	}
	body := r.Body.String()
	if !strings.Contains(body, "<table") || !strings.Contains(body, "gpt-4o") || !strings.Contains(body, "2.5000") {
		t.Errorf("matrix HTML unexpected:\n%s", body)
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
