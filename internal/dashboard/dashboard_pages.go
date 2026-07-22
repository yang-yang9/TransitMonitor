package dashboard

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"transitmonitor/internal/domain"
)

// metricsHandler exposes Prometheus-format metrics. Bypasses auth (for scrapers).
func (s *Server) metricsHandler(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	b.WriteString("# HELP transitmonitor_input_usd_per_1m Effective input USD per 1M tokens (normalized)\n")
	b.WriteString("# TYPE transitmonitor_input_usd_per_1m gauge\n")
	b.WriteString("# HELP transitmonitor_output_usd_per_1m Effective output USD per 1M tokens (normalized)\n")
	b.WriteString("# TYPE transitmonitor_output_usd_per_1m gauge\n")
	ctx := r.Context()
	for _, st := range s.stationsList() {
		obs, err := s.store.LatestRatioObservations(ctx, st.ID)
		if err != nil {
			continue
		}
		for _, o := range obs {
			if o.Sentinel != "" {
				continue
			}
			lbl := fmt.Sprintf("station=%q,group=%q,model=%q", st.ID, o.GroupName, o.ModelName)
			fmt.Fprintf(&b, "transitmonitor_input_usd_per_1m{%s} %v\n", lbl, o.InputUSDPer1M)
			fmt.Fprintf(&b, "transitmonitor_output_usd_per_1m{%s} %v\n", lbl, o.OutputUSDPer1M)
		}
	}
	b.WriteString("# HELP transitmonitor_probe_markup_pct Hidden markup (%) reconciled by the real-cost probe\n")
	b.WriteString("# TYPE transitmonitor_probe_markup_pct gauge\n")
	for _, st := range s.stationsList() {
		prs, err := s.store.ListProbeResults(ctx, st.ID, 50)
		if err != nil {
			continue
		}
		latest := map[string]domain.ProbeResult{}
		for _, p := range prs {
			if _, ok := latest[p.Model]; !ok {
				latest[p.Model] = p
			}
		}
		for model, p := range latest {
			fmt.Fprintf(&b, "transitmonitor_probe_markup_pct{station=%q,model=%q} %v\n", st.ID, model, p.MarkupPct)
		}
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

// --- server-rendered HTML pages (i18n: zh / en via ?lang= or tm-lang cookie) ---

func (s *Server) firstStation() string {
	if len(s.stations) > 0 {
		return s.stations[0].ID
	}
	return ""
}

func sortedModels(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

func (s *Server) changesHTML(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	station := r.URL.Query().Get("station")
	if station == "" {
		station = s.firstStation()
	}
	evs, _ := s.store.ListChangeEvents(r.Context(), station, 100)
	rows := make([][]string, 0, len(evs))
	for _, e := range evs {
		rows = append(rows, []string{
			`<span class="mono">` + fmtTime(e.ObservedAt) + `</span>`,
			esc(e.Group),
			`<span class="mono">` + esc(e.Model) + `</span>`,
			`<span class="tag">` + esc(e.Field) + `</span>`,
			`<span class="mono">` + esc(e.Old) + `</span>`,
			`<span class="mono">` + esc(e.New) + `</span>`,
			`<span class="num">` + fmtPct(e.DeltaPct) + `</span>`,
			severityBadge(lang, e.Severity),
		})
	}
	stag := `<span class="tag tag-pri">` + esc(station) + `</span>`
	body := `<div class="page-hdr"><h1>` + t(lang, "title.changes") + `</h1><p class="sub">` +
		fmt.Sprintf(t(lang, "sub.changes"), stag) + `</p></div>` +
		renderTable(lang, []string{
			t(lang, "col.time"), t(lang, "col.group"), t(lang, "col.model"), t(lang, "col.field"),
			t(lang, "col.old"), t(lang, "col.new"), t(lang, "col.deltapct"), t(lang, "col.severity"),
		}, rows)
	writeHTMLShell(w, lang, t(lang, "title.changes")+" · "+station, "changes", body)
}

func (s *Server) probesHTML(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	station := r.URL.Query().Get("station")
	if station == "" {
		station = s.firstStation()
	}
	prs, _ := s.store.ListProbeResults(r.Context(), station, 100)
	rows := make([][]string, 0, len(prs))
	for _, p := range prs {
		mcls := "p-mid"
		if p.MarkupPct > 0 {
			mcls = "p-high"
		} else if p.MarkupPct < 0 {
			mcls = "p-cheap"
		}
		rows = append(rows, []string{
			`<span class="mono">` + fmtTime(p.ObservedAt) + `</span>`,
			`<span class="mono">` + esc(p.Model) + `</span>`,
			`<span class="num">` + fmt.Sprintf("%d/%d", p.TokensIn, p.TokensOut) + `</span>`,
			`<span class="num mono">` + fmtUSD(p.DeclaredEffectiveUSDPer1M) + `</span>`,
			`<span class="num mono">` + fmtUSD(p.MeasuredUSDPer1M) + `</span>`,
			fmt.Sprintf(`<span class="num %s">%s</span>`, mcls, fmtPct(p.MarkupPct)),
			`<span class="num mono">` + fmtUSD(p.CostUSD) + `</span>`,
			statusBadge(lang, p.Error),
		})
	}
	stag := `<span class="tag tag-pri">` + esc(station) + `</span>`
	body := `<div class="page-hdr"><h1>` + t(lang, "title.probes") + `</h1><p class="sub">` +
		fmt.Sprintf(t(lang, "sub.probes"), stag) + `</p></div>` +
		renderTable(lang, []string{
			t(lang, "col.time"), t(lang, "col.model"), t(lang, "col.tokinout"),
			t(lang, "col.declared"), t(lang, "col.measured"), t(lang, "col.markup"),
			t(lang, "col.cost"), t(lang, "col.status"),
		}, rows)
	writeHTMLShell(w, lang, t(lang, "title.probes")+" · "+station, "probes", body)
}

func (s *Server) matrixHTML(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	model := r.URL.Query().Get("model")
	field := r.URL.Query().Get("field")
	if field == "" {
		field = "input"
	}
	ctx := r.Context()
	type cell struct {
		input, output float64
		sentinel      string
		has           bool
	}
	stCells := make([]map[string]cell, len(s.stations))
	modelSet := map[string]bool{}
	for i, st := range s.stationsList() {
		obs, _ := s.store.LatestRatioObservations(ctx, st.ID)
		m := map[string]cell{}
		for _, o := range obs {
			if model != "" && o.ModelName != model {
				continue
			}
			if _, ok := m[o.ModelName]; !ok {
				m[o.ModelName] = cell{fieldVal(field, o), 0, o.Sentinel, true}
				modelSet[o.ModelName] = true
			}
		}
		stCells[i] = m
	}
	models := sortedModels(modelSet)
	cols := []string{t(lang, "col.model")}
	for _, st := range s.stationsList() {
		cols = append(cols, esc(st.ID))
	}
	rows := make([][]string, 0, len(models))
	for _, m := range models {
		lo, hi := math.MaxFloat64, -math.MaxFloat64
		for i := range s.stationsList() {
			c := stCells[i][m]
			if c.has && c.sentinel == "" {
				if c.input < lo {
					lo = c.input
				}
				if c.input > hi {
					hi = c.input
				}
			}
		}
		row := []string{`<span class="mono">` + esc(m) + `</span>`}
		for i := range s.stationsList() {
			c := stCells[i][m]
			switch {
			case !c.has:
				row = append(row, `<span class="pcell p-na">—</span>`)
			case c.sentinel != "":
				row = append(row, statusBadge(lang, c.sentinel))
			default:
				row = append(row, fmt.Sprintf(`<span class="pcell %s">%s</span>`, priceColorClass(c.input, lo, hi), fmtUSD(c.input)))
			}
		}
		rows = append(rows, row)
	}
	body := `<div class="page-hdr"><h1>` + t(lang, "title.matrix") + `</h1><p class="sub">` + t(lang, "sub.matrix") + `</p></div>` +
		renderTable(lang, cols, rows)
	writeHTMLShell(w, lang, t(lang, "title.matrix"), "matrix", body)
}

func (s *Server) auditHTML(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	entries, _ := s.store.ListAuditLogs(r.Context(), 100)
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []string{
			`<span class="mono">` + fmtTime(e.Ts) + `</span>`,
			`<span class="tag">` + esc(e.Actor) + `</span>`,
			`<span class="tag tag-pri">` + esc(e.Action) + `</span>`,
			esc(e.Target),
			esc(e.Detail),
		})
	}
	body := `<div class="page-hdr"><h1>` + t(lang, "title.audit") + `</h1><p class="sub">` + t(lang, "sub.audit") + `</p></div>` +
		renderTable(lang, []string{t(lang, "col.time"), t(lang, "col.actor"), t(lang, "col.action"), t(lang, "col.target"), t(lang, "col.detail")}, rows)
	writeHTMLShell(w, lang, t(lang, "title.audit"), "audit", body)
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}

func fmtUSD(v float64) string { return fmt.Sprintf("%.4f", v) }
func fmtPct(v float64) string { return fmt.Sprintf("%.2f%%", v) }

func fieldVal(field string, o domain.RatioObservation) float64 {
	switch field {
	case "output":
		return o.OutputUSDPer1M
	case "cache_read":
		return o.CacheReadUSDPer1M
	case "cache_write":
		return o.CacheWriteUSDPer1M
	default:
		return o.InputUSDPer1M
	}
}

func (s *Server) alertsHTML(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	rows_data, _ := s.store.ListAlertEvents(r.Context(), 100)
	rows := make([][]string, 0, len(rows_data))
	for _, a := range rows_data {
		sentBadge := `<span class="badge b-ok">✓</span>`
		if !a.Sent {
			sentBadge = `<span class="badge b-crit">✗</span>`
		}
		rows = append(rows, []string{
			`<span class="mono">` + fmtTime(a.Ts) + `</span>`,
			`<span class="tag">` + esc(a.Rule) + `</span>`,
			`<span class="mono">` + esc(a.StationID) + `</span>`,
			`<span class="mono">` + esc(a.Model) + `</span>`,
			`<span style="font-size:.8rem;color:var(--muted)">` + esc(truncate(a.Payload, 80)) + `</span>`,
			sentBadge,
			esc(a.Error),
		})
	}
	body := `<h1>` + t(lang, "title.alerts") + `</h1><p class="sub">` + t(lang, "sub.alerts") + `</p>` +
		renderTable(lang, []string{t(lang, "col.time"), "rule", "station", "model", "payload", "sent", "error"}, rows)
	writeHTMLShell(w, lang, t(lang, "title.alerts"), "alerts", body)
}
func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
