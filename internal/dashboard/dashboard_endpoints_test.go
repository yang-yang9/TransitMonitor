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

// Backfills tests for /healthz (auth bypass) + /api/audit + /api/probes.

func TestHealthz(t *testing.T) {
	srv, _, cleanup := newDash(t, "")
	defer cleanup()
	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, localReq(http.MethodGet, "/healthz"))
	if r.Code != 200 {
		t.Fatalf("want 200 got %d", r.Code)
	}
	if !strings.Contains(r.Body.String(), "ok") {
		t.Errorf("body: %s", r.Body.String())
	}
}

func TestHealthzBypassesAuth(t *testing.T) {
	srv, _, cleanup := newDash(t, "secret") // token set → non-local normally 401
	defer cleanup()
	// httptest.NewRequest defaults RemoteAddr to a non-local TEST-NET address.
	r := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	srv.Handler().ServeHTTP(r, req)
	if r.Code != 200 {
		t.Errorf("healthz must bypass auth even with token set; got %d", r.Code)
	}
}

func TestAuditAndProbesEndpoints(t *testing.T) {
	srv, st, cleanup := newDash(t, "")
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	if err := st.InsertAuditLog(ctx, "main", "startup", "", "version=0.1 stations=2"); err != nil {
		t.Fatalf("audit insert: %v", err)
	}
	if err := st.InsertProbeResult(ctx, domain.ProbeResult{
		StationID: "s1", Model: "gpt-4o", ObservedAt: now, MeasuredUSDPer1M: 2.5, MarkupPct: 0,
	}); err != nil {
		t.Fatalf("probe insert: %v", err)
	}

	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, localReq(http.MethodGet, "/api/audit"))
	if r.Code != 200 {
		t.Fatalf("audit code: %d", r.Code)
	}
	if !strings.Contains(r.Body.String(), "startup") {
		t.Errorf("audit body missing startup: %s", r.Body.String())
	}

	r2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r2, localReq(http.MethodGet, "/api/probes?station=s1"))
	if r2.Code != 200 {
		t.Fatalf("probes code: %d", r2.Code)
	}
	if !strings.Contains(r2.Body.String(), "gpt-4o") {
		t.Errorf("probes body missing gpt-4o: %s", r2.Body.String())
	}
}
