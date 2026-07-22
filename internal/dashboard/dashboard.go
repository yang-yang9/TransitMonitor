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
	mux      *chi.Mux
	httpSrv  *http.Server
}

// New constructs a dashboard server. token=="" means localhost-only.
func New(stations []domain.Station, st *store.Store, token string) *Server {
	s := &Server{stations: stations, store: st, token: token}
	r := chi.NewRouter()
	r.Use(s.authMiddleware)
	r.Get("/healthz", s.healthz)
	r.Get("/metrics", s.metricsHandler)
	r.Get("/", s.overviewHTML)
	r.Get("/changes", s.changesHTML)
	r.Get("/probes", s.probesHTML)
	r.Get("/matrix", s.matrixHTML)
	r.Get("/audit", s.auditHTML)
	r.Get("/api/stations", s.stationsJSON)
	r.Get("/api/ratios", s.ratiosJSON)
	r.Get("/api/changes", s.changesJSON)
	r.Get("/api/probes", s.probesJSON)
	r.Get("/api/matrix", s.matrixJSON)
	r.Get("/api/audit", s.auditJSON)
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
		if r.URL.Path == "/healthz" || r.URL.Path == "/metrics" {
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
	for _, st := range s.stations {
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
	StationID      string  `json:"station_id"`
	Model          string  `json:"model"`
	InputUSDPer1M  float64 `json:"input_usd_per_1m"`
	OutputUSDPer1M float64 `json:"output_usd_per_1m"`
	Sentinel       string  `json:"sentinel,omitempty"`
}

func (s *Server) matrixJSON(w http.ResponseWriter, r *http.Request) {
	model := r.URL.Query().Get("model")
	var cells []matrixCell
	for _, st := range s.stations {
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
				Sentinel: o.Sentinel,
			})
		}
	}
	writeJSON(w, 200, cells)
}

// overviewHTML renders station cards + quick links.
func (s *Server) overviewHTML(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	ctx := r.Context()
	var b strings.Builder
	b.WriteString(`<h1>` + t(lang, "title.overview") + `</h1><p class="sub">` + t(lang, "sub.overview") +
		`<a class="btn" href="/matrix">` + t(lang, "btn.matrix") + `</a></p>`)
	b.WriteString(`<h2>` + t(lang, "section.stations") + `</h2><div class="grid">`)
	for _, st := range s.stations {
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
		b.WriteString(fmt.Sprintf(
			`<div class="card stcard"><div class="kpi-label"><span class="dot-s %s"></span>%s</div>`+
				`<div class="kpi">%d</div>`+
				`<div class="meta"><span class="tag tag-pri">%s</span> %s<br>%s: %s · %s: %d</div></div>`,
			dot, esc(st.ID), n, esc(string(st.Kind)), esc(st.BaseURL), t(lang, "meta.lastscrape"), lastStr, t(lang, "meta.models"), n))
	}
	b.WriteString(`</div>`)
	b.WriteString(`<div class="card"><h2>` + t(lang, "section.explore") + `</h2><div class="kvs">`)
	for _, it := range navItems {
		b.WriteString(fmt.Sprintf(`<a class="btn" href="%s">%s</a>`, it.H, t(lang, "nav."+it.Key)))
	}
	b.WriteString(`</div></div>`)
	writeHTMLShell(w, lang, t(lang, "title.overview"), "overview", b.String())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
