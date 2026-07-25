// Package dashboard serves the HTTP API + a server-rendered HTML overview.
// Spec: openspec/.../specs/dashboard/spec.md and cross-station-comparison.
package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"transitmonitor/internal/domain"
	"transitmonitor/internal/store"

	"github.com/go-chi/chi/v5"
)

// Server is the dashboard HTTP server.
type Server struct {
	stations []domain.Station
	store    *store.Store
	token    string
	encKey   []byte // for persisting /settings notifier secrets at rest
	mgr      StationManager
	mux      *chi.Mux
	httpSrv  *http.Server
}

// SetEncKey enables /settings notifier-secret persistence (mirrors scheduler.SetEncKey).
func (s *Server) SetEncKey(k []byte) { s.encKey = k }

// New constructs a dashboard server. token=="" means localhost-only.
func New(stations []domain.Station, st *store.Store, token string) *Server {
	s := &Server{stations: stations, store: st, token: token}
	r := chi.NewRouter()
	r.Use(s.authMiddleware)
	r.Get("/healthz", s.healthz)
	r.Get("/metrics", s.metricsHandler)
	r.Get("/readyz", s.readyz)
	r.Get("/", s.overviewHTML)
	r.Get("/balance", s.balanceHTML)
	r.Get("/changes", s.changesHTML)
	r.Get("/probes", s.probesHTML)
	r.Get("/matrix", s.matrixHTML)
	r.Get("/audit", s.auditHTML)
	r.Get("/alerts", s.alertsHTML)
	r.Get("/stations", s.stationsPage)
	r.Get("/stations/{id}", s.stationDetailHTML)
	r.Get("/stations/new", s.stationFormHTML)
	r.Get("/stations/{id}/edit", s.stationEditHTML)
	r.Get("/settings", s.settingsHTML)
	r.Get("/api/stations", s.stationsJSON)
	r.Get("/api/ratios", s.ratiosJSON)
	r.Get("/api/balance", s.balanceJSON)
	r.Get("/api/changes", s.changesJSON)
	r.Get("/api/probes", s.probesJSON)
	r.Get("/api/matrix", s.matrixJSON)
	r.Get("/api/audit", s.auditJSON)
	r.Post("/api/stations", s.stationsCreate)
	r.Put("/api/stations/{id}", s.stationsUpsert)
	r.Delete("/api/stations/{id}", s.stationsDelete)
	r.Post("/api/stations/{id}/poll", s.stationsPoll)
	r.Post("/api/stations/{id}/login", s.stationsLogin)
	r.Post("/api/settings", s.settingsSave)
	r.Post("/api/settings/test", s.settingsTest)
	s.mux = r
	return s
}

// Handler returns the HTTP handler (for httptest).
func (s *Server) Handler() http.Handler { return s.mux }

// ListenAndServe starts the server.
func (s *Server) ListenAndServe(addr string) error {
	s.httpSrv = &http.Server{Addr: addr, Handler: s.mux}
	return s.httpSrv.ListenAndServe()
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Shutdown(ctx)
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("TRANSMONITOR_DASHBOARD_PUBLIC") == "1" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/healthz" || r.URL.Path == "/metrics" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		if s.token == "" {
			if !isLocal(r.RemoteAddr) {
				http.Error(w, "localhost only (set dashboard.token or TRANSMONITOR_DASHBOARD_PUBLIC=1)", http.StatusUnauthorized)
				return
			}
		} else if r.Header.Get("Authorization") != "Bearer "+s.token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLocal(addr string) bool {
	host := addr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) stationsJSON(w http.ResponseWriter, r *http.Request) {
	type stationOut struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		BaseURL string `json:"base_url"`
		Enabled bool   `json:"enabled"`
	}
	out := make([]stationOut, 0, len(s.stations))
	for _, st := range s.stationsList() {
		out = append(out, stationOut{ID: st.ID, Name: st.Name, Kind: string(st.Kind), BaseURL: st.BaseURL, Enabled: st.Enabled})
	}
	writeJSON(w, 200, out)
}

func (s *Server) ratiosJSON(w http.ResponseWriter, r *http.Request) {
	station := r.URL.Query().Get("station")
	if station == "" {
		http.Error(w, "station required", http.StatusBadRequest)
		return
	}
	obs, err := s.store.LatestRatioObservations(r.Context(), station)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, obs)
}

// balanceJSON returns the latest balance reading per station (one row each).
func (s *Server) balanceJSON(w http.ResponseWriter, r *http.Request) {
	obs, err := s.store.LatestBalances(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type out struct {
		StationID    string  `json:"station_id"`
		RemainingUSD float64 `json:"remaining_usd"`
		UsedUSD      float64 `json:"used_usd"`
		TotalUSD     float64 `json:"total_usd"`
		Unlimited    bool    `json:"unlimited"`
		Currency     string  `json:"currency"`
		ObservedAt   int64   `json:"observed_at"`
		Source       string  `json:"source_endpoint"`
	}
	res := make([]out, 0, len(obs))
	for _, b := range obs {
		res = append(res, out{
			StationID: b.StationID, RemainingUSD: b.RemainingUSD, UsedUSD: b.UsedUSD,
			TotalUSD: b.TotalUSD, Unlimited: b.Unlimited, Currency: b.Currency,
			ObservedAt: b.ObservedAt.Unix(), Source: b.SourceEndpoint,
		})
	}
	writeJSON(w, 200, res)
}

func (s *Server) changesJSON(w http.ResponseWriter, r *http.Request) {
	station := r.URL.Query().Get("station")
	if station == "" {
		http.Error(w, "station required", http.StatusBadRequest)
		return
	}
	evs, err := s.store.ListChangeEvents(r.Context(), station, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, evs)
}

func (s *Server) probesJSON(w http.ResponseWriter, r *http.Request) {
	station := r.URL.Query().Get("station")
	if station == "" {
		http.Error(w, "station required", http.StatusBadRequest)
		return
	}
	prs, err := s.store.ListProbeResults(r.Context(), station, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, prs)
}

func (s *Server) auditJSON(w http.ResponseWriter, r *http.Request) {
	entries, err := s.store.ListAuditLogs(r.Context(), 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, entries)
}

type matrixCell struct {
	StationID          string  `json:"station_id"`
	Model              string  `json:"model"`
	InputUSDPer1M      float64 `json:"input_usd_per_1m"`
	OutputUSDPer1M     float64 `json:"output_usd_per_1m"`
	CacheReadUSDPer1M  float64 `json:"cache_read_usd_per_1m,omitempty"`
	CacheWriteUSDPer1M float64 `json:"cache_write_usd_per_1m,omitempty"`
	Sentinel           string  `json:"sentinel,omitempty"`
}

func (s *Server) matrixJSON(w http.ResponseWriter, r *http.Request) {
	model := r.URL.Query().Get("model")
	var cells []matrixCell
	for _, st := range s.stationsList() {
		obs, err := s.store.LatestRatioObservations(r.Context(), st.ID)
		if err != nil {
			continue
		}
		for _, o := range obs {
			if model != "" && o.ModelName != model {
				continue
			}
			cells = append(cells, matrixCell{
				StationID: st.ID, Model: o.ModelName,
				InputUSDPer1M: o.InputUSDPer1M, OutputUSDPer1M: o.OutputUSDPer1M,
				CacheReadUSDPer1M: o.CacheReadUSDPer1M, CacheWriteUSDPer1M: o.CacheWriteUSDPer1M,
				Sentinel: o.Sentinel,
			})
		}
	}
	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=matrix.csv")
		var b strings.Builder
		b.WriteString("station,model,input_usd_per_1m,output_usd_per_1m,cache_read,cache_write,sentinel\n")
		for _, c := range cells {
			fmt.Fprintf(&b, "%s,%s,%v,%v,%v,%v,%s\n", c.StationID, c.Model,
				c.InputUSDPer1M, c.OutputUSDPer1M, c.CacheReadUSDPer1M, c.CacheWriteUSDPer1M, c.Sentinel)
		}
		_, _ = w.Write([]byte(b.String()))
		return
	}
	writeJSON(w, 200, cells)
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	_, err := s.store.ListAuditLogs(r.Context(), 1)
	if err != nil {
		writeJSON(w, 503, map[string]string{"status": "not ready", "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ready"})
}

// overviewHTML renders station cards + quick links.
func (s *Server) overviewHTML(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	ctx := r.Context()
	var b strings.Builder
	b.WriteString(`<div class="page-hdr"><h1>` + t(lang, "title.overview") + `</h1><p class="sub">` + t(lang, "sub.overview") +
		`<a class="btn" href="/matrix">` + t(lang, "btn.matrix") + `</a></p></div>`)
	// Surface credential-decrypt failures loudly: otherwise the operator only
	// sees misleading downstream "no api_key" poll errors and chases the wrong fix.
	if df := s.decryptFailedCount(); df > 0 {
		b.WriteString(`<div class="card" style="border-left:3px solid var(--crit,#ef4444);background:var(--bg-2)"><span class="badge b-crit">⚠</span> ` +
			fmt.Sprintf(t(lang, "banner.decrypt_failed"), df) +
			` <a class="btn btn-sm btn-outline" href="/stations">` + t(lang, "title.stations") + `</a></div>`)
	}
	b.WriteString(`<h2>` + t(lang, "section.stations") + `</h2><div class="grid">`)
	for _, st := range s.stationsList() {
		obs, _ := s.store.LatestRatioObservations(ctx, st.ID)
		n := len(obs)
		var last time.Time
		for _, o := range obs {
			if o.ObservedAt.After(last) {
				last = o.ObservedAt
			}
		}
		dot := "none"
		if n > 0 {
			dot = "ok"
		}
		lastStr := "—"
		if !last.IsZero() {
			lastStr = fmtTime(last)
		}
		name := st.Name
		if name == "" {
			name = st.ID
		}
		grs, _ := s.store.LatestGroupRatios(ctx, st.ID)
		chart := groupRatioChart(grs, false)
		// recent group-ratio change hint
		changeHint := ""
		if evs, _ := s.store.ListChangeEvents(ctx, st.ID, 20); len(evs) > 0 {
			for _, e := range evs {
				if e.Field == domain.FieldGroupRatio {
					changeHint = fmt.Sprintf(`<div class="meta"><span class="badge b-warn">%s</span> %s → <b>%s</b> (%s)</div>`,
						t(lang, "recent.change"), esc(e.Group), esc(e.New), fmtPct(e.DeltaPct))
					break
				}
			}
		}
		grpCount := len(grs)
		// Balance KPI (if the station exposes one). Low balance → red.
		balStr := ""
		if bal, err := s.store.LatestBalance(ctx, st.ID); err == nil {
			cls := "b-ok"
			if !bal.Unlimited && bal.RemainingUSD < 1 {
				cls = "b-crit"
			} else if !bal.Unlimited && bal.TotalUSD > 0 && bal.RemainingUSD/bal.TotalUSD < 0.2 {
				cls = "b-warn"
			}
			val := fmt.Sprintf("$%.2f", bal.RemainingUSD)
			if bal.Unlimited {
				val = t(lang, "balance.unlimited")
			}
			balStr = fmt.Sprintf(`<div class="kpi-label">`+t(lang, "col.balance")+`</div><div class="kpi"><span class="badge-sm %s">%s</span></div>`, cls, val)
		}
		b.WriteString(fmt.Sprintf(
			`<a class="card stcard" href="/stations/%s">`+
				`<div class="st-hdr"><span class="st-name">%s</span><span class="dot-s %s"></span></div>`+
				`%s`+
				`%s`+
				`%s`+
				`<div class="meta">`+
				`<span class="tag tag-pri">%s</span> <span class="tag">%s</span> · `+
				`%d %s · %s: %s</div></a>`,
			esc(st.ID), esc(name), dot,
			balStr,
			chart,
			changeHint,
			esc(string(st.Kind)), esc(st.BaseURL),
			grpCount, t(lang, "meta.groups"), t(lang, "meta.lastscrape"), lastStr))
	}
	b.WriteString(`</div>`)
	b.WriteString(`<div class="card" style="margin-top:.5rem"><h2>` + t(lang, "section.explore") + `</h2><div class="kvs">`)
	for _, it := range navItems {
		b.WriteString(fmt.Sprintf(`<a class="btn btn-sm" href="%s">%s</a>`, it.H, t(lang, "nav."+it.Key)))
	}
	b.WriteString(`</div></div>`)
	writeHTMLShell(w, lang, t(lang, "title.overview"), "overview", b.String())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
