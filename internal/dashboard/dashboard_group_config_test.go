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

func TestStationGroupSettingsSaveAndRender(t *testing.T) {
	srv, st, cleanup := newDash(t, "")
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	_ = st.InsertSnapshot(ctx, domain.RawSnapshot{
		StationID: "s1", ObservedAt: now,
		GroupRatios: map[string]float64{"vip": 0.5, "pro": 1.0, "internal": 2.0},
	})
	// also seed the station row so the FK on station_group_config + snapshots is satisfied
	// (newDash seeds stations in-memory only; UpsertStation persists the row).
	_ = st.UpsertStation(ctx, domain.Station{ID: "s1", Name: "S1", Kind: domain.KindNewAPI, BaseURL: "https://a.example", Enabled: true}, nil)

	// POST the full config: vip visible(0), pro hidden(0), internal visible(1)
	body := `{"groups":[{"group_name":"vip","visible":true,"sort_order":0},` +
		`{"group_name":"pro","visible":false,"sort_order":0},` +
		`{"group_name":"internal","visible":true,"sort_order":1}]}`
	req := httptest.NewRequest(http.MethodPost, "/stations/s1/groups", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:1234"
	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, req)
	if r.Code != 200 {
		t.Fatalf("save: want 200 got %d: %s", r.Code, r.Body.String())
	}

	// persisted?
	got, _ := st.GetStationGroupConfigs(ctx, "s1")
	if len(got) != 3 {
		t.Fatalf("want 3 cfg rows got %d", len(got))
	}
	byName := map[string]domain.StationGroupConfig{}
	for _, c := range got {
		byName[c.GroupName] = c
	}
	if byName["pro"].Visible {
		t.Error("pro should be hidden after save")
	}
	if byName["internal"].SortOrder != 1 {
		t.Errorf("internal sort_order: want 1 got %d", byName["internal"].SortOrder)
	}

	// detail page renders the settings section with a checkbox per group
	r2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r2, localReq(http.MethodGet, "/stations/s1"))
	if r2.Code != 200 {
		t.Fatalf("detail: want 200 got %d", r2.Code)
	}
	html := r2.Body.String()
	if !strings.Contains(html, "分组展示设置") {
		t.Errorf("settings section missing:\n%s", html)
	}
	if !strings.Contains(html, `name="visible"`) || !strings.Contains(html, "pro") {
		t.Errorf("per-group checkbox row missing:\n%s", html)
	}
}

func TestMatrixHidesGroupsHiddenEverywhere(t *testing.T) {
	srv, st, cleanup := newDash(t, "")
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	// both stations carry vip + internal
	_ = st.UpsertStation(ctx, domain.Station{ID: "s1", Name: "S1", Kind: domain.KindNewAPI, BaseURL: "https://a.example", Enabled: true}, nil)
	_ = st.UpsertStation(ctx, domain.Station{ID: "s2", Name: "S2", Kind: domain.KindSub2API, BaseURL: "https://b.example", Enabled: true}, nil)
	_ = st.InsertSnapshot(ctx, domain.RawSnapshot{StationID: "s1", ObservedAt: now, GroupRatios: map[string]float64{"vip": 0.5, "internal": 2.0}})
	_ = st.InsertSnapshot(ctx, domain.RawSnapshot{StationID: "s2", ObservedAt: now, GroupRatios: map[string]float64{"vip": 0.6, "internal": 2.1}})
	// vip visible on s1 (hidden on s2); internal hidden on both
	saveGroupCfg(t, srv, "s1", []domain.StationGroupConfig{{StationID: "s1", GroupName: "vip", Visible: true, SortOrder: 0}, {StationID: "s1", GroupName: "internal", Visible: false, SortOrder: 0}})
	saveGroupCfg(t, srv, "s2", []domain.StationGroupConfig{{StationID: "s2", GroupName: "vip", Visible: false, SortOrder: 0}, {StationID: "s2", GroupName: "internal", Visible: false, SortOrder: 0}})

	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, localReq(http.MethodGet, "/matrix"))
	if r.Code != 200 {
		t.Fatalf("want 200 got %d", r.Code)
	}
	body := r.Body.String()
	if !strings.Contains(body, "vip") {
		t.Errorf("vip (visible on s1) should be a matrix row:\n%s", body)
	}
	if strings.Contains(body, "internal") {
		t.Errorf("internal (hidden on all stations) should NOT be a matrix row:\n%s", body)
	}
	if !strings.Contains(body, "★") {
		t.Errorf("visible station cell should carry ★ marker:\n%s", body)
	}
}
