package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"transitmonitor/internal/domain"
	"transitmonitor/internal/store"
)

// localReq makes a request whose RemoteAddr is localhost (httptest defaults to a
// non-local TEST-NET address, which the localhost-only guard would reject).
func localReq(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = "127.0.0.1:1234"
	return req
}

func newDash(t *testing.T, token string) (*Server, *store.Store, func()) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	srv := New([]domain.Station{
		{ID: "s1", Name: "S1", Kind: domain.KindNewAPI, BaseURL: "https://a.example", Enabled: true},
		{ID: "s2", Name: "S2", Kind: domain.KindSub2API, BaseURL: "https://b.example", Enabled: true},
	}, st, token, "")
	return srv, st, func() { _ = st.Close() }
}

func TestStationsJSON(t *testing.T) {
	srv, _, cleanup := newDash(t, "")
	defer cleanup()
	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, localReq(http.MethodGet, "/api/stations"))
	if r.Code != 200 {
		t.Fatalf("code: want 200 got %d", r.Code)
	}
	var got []map[string]any
	_ = json.Unmarshal(r.Body.Bytes(), &got)
	if len(got) != 2 {
		t.Errorf("want 2 stations got %d", len(got))
	}
	if v, _ := json.Marshal(got[0]); strings.Contains(string(v), "api_key") {
		t.Error("station JSON must not leak credentials")
	}
}

func TestRatiosAndMatrix(t *testing.T) {
	srv, st, cleanup := newDash(t, "")
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	_ = st.InsertRatioObservations(ctx, []domain.RatioObservation{
		{StationID: "s1", GroupName: "default", ModelName: "gpt-4o", InputUSDPer1M: 2.5, OutputUSDPer1M: 10, ObservedAt: now},
		{StationID: "s1", GroupName: "default", ModelName: "dall-e-3", Sentinel: "fixed-price (per-call)", ObservedAt: now},
	})

	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, localReq(http.MethodGet, "/api/ratios?station=s1"))
	if r.Code != 200 {
		t.Fatalf("ratios code: %d", r.Code)
	}
	var obs []map[string]any
	_ = json.Unmarshal(r.Body.Bytes(), &obs)
	if len(obs) != 2 {
		t.Errorf("want 2 ratios got %d", len(obs))
	}

	r2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r2, localReq(http.MethodGet, "/api/matrix?model=gpt-4o"))
	var cells []map[string]any
	_ = json.Unmarshal(r2.Body.Bytes(), &cells)
	if len(cells) != 1 || cells[0]["input_usd_per_1m"] == nil {
		t.Errorf("matrix: want 1 cell with input, got %+v", cells)
	}
}

func TestAuth_BearerRequired(t *testing.T) {
	srv, _, cleanup := newDash(t, "secret")
	defer cleanup()
	// No bearer → 401 (RemoteAddr is non-local, so even localhost bypass fails).
	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/stations", nil))
	if r.Code != 401 {
		t.Errorf("no bearer: want 401 got %d", r.Code)
	}
	r2 := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/stations", nil)
	req.Header.Set("Authorization", "Bearer secret")
	srv.Handler().ServeHTTP(r2, req)
	if r2.Code != 200 {
		t.Errorf("valid bearer: want 200 got %d", r2.Code)
	}
}

func TestOverviewHTML(t *testing.T) {
	srv, _, cleanup := newDash(t, "")
	defer cleanup()
	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, localReq(http.MethodGet, "/"))
	if r.Code != 200 {
		t.Fatalf("overview code: %d", r.Code)
	}
	if !strings.Contains(r.Body.String(), "TransitMonitor") {
		t.Error("overview should contain title")
	}
}
