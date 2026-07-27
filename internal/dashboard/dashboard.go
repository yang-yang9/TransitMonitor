// Package dashboard serves the HTTP API + a server-rendered HTML overview.
// Spec: openspec/.../specs/dashboard/spec.md and cross-station-comparison.
package dashboard

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"transitmonitor/internal/domain"
	"transitmonitor/internal/store"
	"transitmonitor/internal/updater"

	"github.com/go-chi/chi/v5"
)

// Updater is the in-panel update/rollback surface (implemented by
// internal/updater.Service; wired by main). Defined here so the dashboard
// package does not depend on the updater implementation, mirroring the
// StationManager decoupling used for /settings.
type Updater interface {
	CurrentVersion() string
	Mode() string
	WrapperReady() bool
	CheckUpdates(ctx context.Context, force bool) (updater.UpdateInfo, error)
	PerformUpdate(ctx context.Context) (updater.UpdateOutcome, error)
	Rollback(ctx context.Context) (updater.UpdateOutcome, error)
	RollbackToVersion(ctx context.Context, version string) (updater.UpdateOutcome, error)
	ListRollbackVersions(ctx context.Context) ([]updater.RollbackVersion, error)
	Restart(ctx context.Context) error
}

// Server is the dashboard HTTP server.
type Server struct {
	stations   []domain.Station
	store      *store.Store
	token      string
	password   string // browser login password (empty = no login page)
	sessionKey []byte // HMAC key for signing session cookies
	encKey     []byte // for persisting /settings notifier secrets at rest
	mgr        StationManager
	updater    Updater
	version    string // running build version, surfaced on /system + /api/system/version
	mux        *chi.Mux
	httpSrv    *http.Server
}

// SetEncKey enables /settings notifier-secret persistence (mirrors scheduler.SetEncKey).
func (s *Server) SetEncKey(k []byte) { s.encKey = k }

// SetVersion plumbs the running build version (from -ldflags -X main.version)
// into the dashboard so /system and /api/system/version can display it.
func (s *Server) SetVersion(v string) { s.version = v }

// SetUpdater wires the in-panel update/rollback service (enables /system).
func (s *Server) SetUpdater(u Updater) { s.updater = u }

// New constructs a dashboard server. token=="" means localhost-only.
// password enables browser login (GET /login → POST /api/login → session cookie).
func New(stations []domain.Station, st *store.Store, token, password string) *Server {
	sk := make([]byte, 32)
	_, _ = rand.Read(sk)
	s := &Server{stations: stations, store: st, token: token, password: password, sessionKey: sk}
	r := chi.NewRouter()
	r.Use(s.authMiddleware)
	r.Get("/healthz", s.healthz)
	r.Get("/metrics", s.metricsHandler)
	r.Get("/readyz", s.readyz)
	r.Get("/login", s.loginHTML)
	r.Post("/api/login", s.loginAPI)
	r.Post("/api/logout", s.logoutAPI)
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
	r.Get("/system", s.systemHTML)
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
	r.Put("/api/stations/order", s.stationsReorder)
	r.Post("/api/stations/{id}/poll", s.stationsPoll)
	r.Post("/api/stations/{id}/login", s.stationsLogin)
	r.Post("/stations/{id}/groups", s.stationGroupSettingsSave)
	r.Post("/api/settings", s.settingsSave)
	r.Post("/api/settings/test", s.settingsTest)
	r.Post("/api/settings/rules", s.settingsRulesSave)
	r.Post("/api/settings/rules/reset", s.settingsRulesReset)
	r.Post("/api/settings/behavior", s.settingsBehaviorSave)
	r.Get("/api/system/version", s.systemVersionJSON)
	r.Get("/api/system/check-updates", s.systemCheckUpdatesJSON)
	r.Get("/api/system/rollback-versions", s.systemRollbackVersionsJSON)
	r.Post("/api/system/upgrade", s.systemUpgradeJSON)
	r.Post("/api/system/rollback", s.systemRollbackJSON)
	r.Post("/api/system/restart", s.systemRestartJSON)
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
		if r.URL.Path == "/login" || r.URL.Path == "/api/login" {
			next.ServeHTTP(w, r)
			return
		}
		// Bearer token (API clients).
		if s.token != "" && r.Header.Get("Authorization") == "Bearer "+s.token {
			next.ServeHTTP(w, r)
			return
		}
		// Password-protected mode: check session cookie.
		if s.password != "" {
			if s.validSession(r) {
				next.ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
			} else {
				http.Redirect(w, r, "/login", http.StatusFound)
			}
			return
		}
		// Token-only mode (no password): require Bearer header or localhost.
		if s.token == "" {
			if !isLocal(r.RemoteAddr) {
				http.Error(w, "localhost only (set dashboard.token or TRANSMONITOR_DASHBOARD_PUBLIC=1)", http.StatusUnauthorized)
				return
			}
		} else {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

const sessionCookieName = "tm-session"
const sessionMaxAge = 7 * 24 * 3600 // 7 days

func (s *Server) signSession(ts int64) string {
	payload := fmt.Sprintf("%d", ts)
	mac := hmac.New(sha256.New, s.sessionKey)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return payload + "." + sig
}

func (s *Server) validSession(r *http.Request) bool {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		return false
	}
	payload := parts[0]
	mac := hmac.New(sha256.New, s.sessionKey)
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(parts[1]), []byte(expected)) {
		return false
	}
	var ts int64
	if _, err := fmt.Sscanf(payload, "%d", &ts); err != nil {
		return false
	}
	if time.Now().Unix()-ts > int64(sessionMaxAge) {
		return false
	}
	return true
}

func (s *Server) setSessionCookie(w http.ResponseWriter) {
	val := s.signSession(time.Now().Unix())
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    val,
		Path:     "/",
		MaxAge:   sessionMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
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

// loginHTML renders the admin login page.
func (s *Server) loginHTML(w http.ResponseWriter, r *http.Request) {
	if s.password == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if s.validSession(r) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	lang := s.lang(w, r)
	errMsg := ""
	if r.URL.Query().Get("err") == "1" {
		errMsg = t(lang, "login.error")
	}
	writeLoginPage(w, lang, errMsg)
}

// loginAPI validates the password and sets a session cookie.
func (s *Server) loginAPI(w http.ResponseWriter, r *http.Request) {
	if s.password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no password configured"})
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		_ = json.NewDecoder(r.Body).Decode(&in)
	} else {
		_ = r.ParseForm()
		in.Password = r.FormValue("password")
	}
	if !hmac.Equal([]byte(in.Password), []byte(s.password)) {
		if strings.HasPrefix(ct, "application/json") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "wrong password"})
		} else {
			http.Redirect(w, r, "/login?err=1", http.StatusFound)
		}
		return
	}
	s.setSessionCookie(w)
	if strings.HasPrefix(ct, "application/json") {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	} else {
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

// logoutAPI clears the session cookie.
func (s *Server) logoutAPI(w http.ResponseWriter, r *http.Request) {
	s.clearSessionCookie(w)
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	} else {
		http.Redirect(w, r, "/login", http.StatusFound)
	}
}

// HasPassword reports whether password-based login is enabled.
func (s *Server) HasPassword() bool { return s.password != "" }

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
	// Overview density mode: cookie tm-overview (mirrors tm-lang) lets the
	// server pick the chart variant at render time. Default "compact" = pill
	// strip (denser, ~5-6 cards/row); "detail" = per-group bar chart (legacy).
	view := "compact"
	if c, err := r.Cookie("tm-overview"); err == nil && c.Value == "detail" {
		view = "detail"
	}
	gridCls := ""
	if view == "compact" {
		gridCls = " grid-compact"
	}
	b.WriteString(`<h2>` + t(lang, "section.stations") + `</h2><div class="grid` + gridCls + `">`)
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
		grd, _ := s.store.LatestGroupRateDefaults(ctx, st.ID)
		cfgs, _ := s.store.GetStationGroupConfigs(ctx, st.ID)
		visible, hidden := domain.SplitVisible(domain.PartitionGroups(grs, grd, cfgs))
		chart := groupRatioPills(lang, visible) + renderHiddenGroupsExpander(lang, hidden)
		if view == "detail" {
			chart = groupRatioChart(lang, visible, false) + renderHiddenGroupsExpander(lang, hidden)
		}
		// recent group-ratio change hints (multi, sorted by group display order)
		changeHint := ""
		if evs, _ := s.store.ListChangeEvents(ctx, st.ID, 20); len(evs) > 0 {
			// group display order index (visible + hidden combined)
			allGroups := append(visible, hidden...)
			orderIdx := make(map[string]int, len(allGroups))
			for i, g := range allGroups {
				orderIdx[g.Name] = i
			}
			// collect per-group most recent change (dedup by group name)
			type grpChange struct {
				idx int
				ev  domain.ChangeEvent
			}
			seen := map[string]bool{}
			var changes []grpChange
			for _, e := range evs {
				if e.Field != domain.FieldGroupRatio || seen[e.Group] {
					continue
				}
				seen[e.Group] = true
				idx, ok := orderIdx[e.Group]
				if !ok {
					idx = len(allGroups)
				}
				changes = append(changes, grpChange{idx, e})
				if len(changes) >= 5 {
					break
				}
			}
			if len(changes) > 0 {
				sort.Slice(changes, func(i, j int) bool { return changes[i].idx < changes[j].idx })
				var cb strings.Builder
				cb.WriteString(`<div class="meta change-hints"><span class="badge b-warn">` + t(lang, "recent.change") + `</span>`)
				for _, c := range changes {
					oldStr := ""
					if c.ev.Old != "" {
						oldStr = fmt.Sprintf(`<span class="ch-old">%s →</span> `, esc(c.ev.Old))
					}
					cb.WriteString(fmt.Sprintf(`<span class="ch-item"><span class="ch-name">%s</span><span class="ch-val">%s<b>%s</b></span><span class="ch-ts">%s</span></span>`,
						esc(c.ev.Group), oldStr, esc(c.ev.New), fmtTimeShort(c.ev.ObservedAt)))
				}
				cb.WriteString(`</div>`)
				changeHint = cb.String()
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
			balStr = fmt.Sprintf(` <span class="badge-sm %s">%s %s</span>`, cls, t(lang, "col.balance"), val)
		}
		b.WriteString(fmt.Sprintf(
			`<a class="card stcard" href="/stations/%s">`+
				`<div class="st-hdr"><span class="st-name">%s%s</span><span class="dot-s %s"></span></div>`+
				`%s`+
				`%s`+
				`<div class="meta">`+
				`<span class="tag tag-pri">%s</span> · `+
				`%d %s · %s: %s</div></a>`,
			esc(st.ID), esc(name), balStr, dot,
			chart,
			changeHint,
			esc(string(st.Kind)),
			grpCount, t(lang, "meta.groups"), t(lang, "meta.lastscrape"), lastStr))
	}
	b.WriteString(`</div>`)
	b.WriteString(`<div class="card" style="margin-top:.5rem"><h2>` + t(lang, "section.explore") + `</h2><div class="kvs">`)
	for _, it := range navItems {
		b.WriteString(fmt.Sprintf(`<a class="btn btn-sm" href="%s">%s</a>`, it.H, t(lang, "nav."+it.Key)))
	}
	b.WriteString(`</div></div>`)
	s.writeHTMLShell(w, lang, t(lang, "title.overview"), "overview", b.String())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
