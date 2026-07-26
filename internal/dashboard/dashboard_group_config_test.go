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

// helper: save a station's group config (visible + hidden) directly via the store.
func saveGroupCfg(t *testing.T, srv *Server, stationID string, cfgs []domain.StationGroupConfig) {
	t.Helper()
	if err := srv.store.SaveStationGroupConfigs(context.Background(), stationID, cfgs); err != nil {
		t.Fatalf("save group cfg: %v", err)
	}
}

func TestOverviewHidesConfiguredHiddenGroups(t *testing.T) {
	srv, st, cleanup := newDash(t, "")
	defer cleanup()
	ctx := context.Background()
	// newDash seeds stations in-memory only; persist s1 so the
	// station_group_config FK (and snapshot FK) can resolve.
	if err := st.UpsertStation(ctx, domain.Station{
		ID: "s1", Name: "S1", Kind: domain.KindNewAPI, BaseURL: "https://a.example", Enabled: true,
	}, nil); err != nil {
		t.Fatalf("upsert station: %v", err)
	}
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	_ = st.InsertSnapshot(ctx, domain.RawSnapshot{
		StationID: "s1", ObservedAt: now,
		GroupRatios: map[string]float64{"vip": 0.5, "internal": 2.0},
	})
	saveGroupCfg(t, srv, "s1", []domain.StationGroupConfig{
		{StationID: "s1", GroupName: "vip", Visible: true, SortOrder: 0},
		{StationID: "s1", GroupName: "internal", Visible: false, SortOrder: 0},
	})

	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, localReq(http.MethodGet, "/"))
	if r.Code != 200 {
		t.Fatalf("want 200 got %d", r.Code)
	}
	body := r.Body.String()
	if !strings.Contains(body, "vip") || !strings.Contains(body, "0.50x") {
		t.Errorf("visible group vip should be rendered:\n%s", body)
	}
	if !strings.Contains(body, "已隐藏") {
		t.Errorf("hidden expander marker missing:\n%s", body)
	}
}
