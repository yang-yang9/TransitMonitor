// Package dashboard serves the HTTP API + a server-rendered HTML overview.
// Spec: openspec/.../specs/dashboard/spec.md and cross-station-comparison.
package dashboard

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"strings"

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
	tmpl     *template.Template
	httpSrv  *http.Server
}

// New constructs a dashboard server. token=="" means localhost-only.
func New(stations []domain.Station, st *store.Store, token string) *Server {
	s := &Server{stations: stations, store: st, token: token}
	s.tmpl = template.Must(template.New("overview").Parse(overviewTpl))
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
		if r.URL.Path == "/healthz" || r.URL.Path == "/metrics" { // healthz + metrics bypass auth (for healthchecks / prom scrape)
			next.ServeHTTP(w, r)
			return
		}
		if s.token == "" {
			if !isLocal(r.RemoteAddr) {
				http.Error(w, "localhost only (set dashboard.token to allow remote)", http.StatusUnauthorized)
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

const overviewTpl = `<!doctype html><html><head><meta charset="utf-8">
<title>TransitMonitor</title>
<style>body{font:14px/1.5 -apple-system,system-ui,sans-serif;margin:2rem;color:#222}table{border-collapse:collapse}td,th{border:1px solid #ddd;padding:4px 8px}a{color:#0366d6}</style>
</head><body>
<h1>TransitMonitor</h1>
<p>中转站倍率监控 · pages: <a href="/matrix">matrix</a> · <a href="/changes">changes</a> · <a href="/probes">probes</a> · <a href="/audit">audit</a> · API: <a href="/api/stations">/api/*</a> · <a href="/metrics">/metrics</a> (Prometheus) · <a href="/healthz">/healthz</a></p>
<h2>Stations</h2>
<table><tr><th>id</th><th>kind</th><th>base_url</th></tr>{{range .Stations}}
<tr><td>{{.ID}}</td><td>{{.Kind}}</td><td>{{.BaseURL}}</td></tr>{{end}}</table>
</body></html>`

func (s *Server) overviewHTML(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	type row struct{ ID, Kind, BaseURL string }
	data := struct{ Stations []row }{}
	for _, st := range s.stations {
		data.Stations = append(data.Stations, row{ID: st.ID, Kind: string(st.Kind), BaseURL: st.BaseURL})
	}
	_ = s.tmpl.Execute(w, data)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
