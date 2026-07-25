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

func TestBalanceEndpoints(t *testing.T) {
	srv, st, cleanup := newDash(t, "")
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	if err := st.InsertBalanceObservation(ctx, domain.BalanceObservation{
		StationID: "s1", ObservedAt: now, RemainingUSD: 12.5, UsedUSD: 3, TotalUSD: 15.5,
		Currency: "USD", SourceEndpoint: "/api/user/self",
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// /api/balance JSON
	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, localReq(http.MethodGet, "/api/balance"))
	if r.Code != 200 {
		t.Fatalf("api/balance code: want 200 got %d", r.Code)
	}
	if !strings.Contains(r.Body.String(), "12.5") {
		t.Errorf("api/balance body missing 12.5: %s", r.Body.String())
	}

	// /balance HTML page
	r2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r2, localReq(http.MethodGet, "/balance"))
	if r2.Code != 200 {
		t.Fatalf("/balance code: want 200 got %d", r2.Code)
	}
	body := r2.Body.String()
	if !strings.Contains(body, "S1") {
		t.Errorf("/balance body missing station name S1: %s", body)
	}
	if !strings.Contains(body, "$12.50") {
		t.Errorf("/balance body missing $12.50: %s", body)
	}

	// /metrics exposes the balance gauge.
	r3 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r3, localReq(http.MethodGet, "/metrics"))
	if !strings.Contains(r3.Body.String(), "transitmonitor_balance_remaining_usd{station=\"s1\"} 12.5") {
		t.Errorf("metrics missing balance gauge: %s", r3.Body.String())
	}
}
