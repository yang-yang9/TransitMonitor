package dashboard

import (
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"transitmonitor/internal/domain"
)

// metricsHandler exposes Prometheus-format metrics. Bypasses auth (for scrapers).
//
//	transitmonitor_input_usd_per_1m{station,group,model}   gauge
//	transitmonitor_output_usd_per_1m{station,group,model}  gauge
//	transitmonitor_probe_markup_pct{station,model}         gauge
//
// Excluded/non-derivable rows (sentinel set) are skipped (no numeric value).
func (s *Server) metricsHandler(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	b.WriteString("# HELP transitmonitor_input_usd_per_1m Effective input USD per 1M tokens (normalized)\n")
	b.WriteString("# TYPE transitmonitor_input_usd_per_1m gauge\n")
	b.WriteString("# HELP transitmonitor_output_usd_per_1m Effective output USD per 1M tokens (normalized)\n")
	b.WriteString("# TYPE transitmonitor_output_usd_per_1m gauge\n")
	ctx := r.Context()
	for _, st := range s.stations {
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
	for _, st := range s.stations {
		prs, err := s.store.ListProbeResults(ctx, st.ID, 50)
		if err != nil {
			continue
		}
		// prs is DESC by observed_at; keep the first (latest) per model.
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

// --- server-rendered HTML pages ---

func (s *Server) firstStation() string {
	if len(s.stations) > 0 {
		return s.stations[0].ID
	}
	return ""
}

func (s *Server) changesHTML(w http.ResponseWriter, r *http.Request) {
	station := r.URL.Query().Get("station")
	if station == "" {
		station = s.firstStation()
	}
	evs, _ := s.store.ListChangeEvents(r.Context(), station, 100)
	rows := make([][]string, 0, len(evs))
	for _, e := range evs {
		rows = append(rows, []string{fmtTime(e.ObservedAt), e.StationID, e.Group, e.Model, e.Field, e.Old, e.New, fmtPct(e.DeltaPct), e.Severity})
	}
	writeHTML(w, "Changes · "+station, []string{"time", "station", "group", "model", "field", "old", "new", "delta%", "severity"}, rows)
}

func (s *Server) probesHTML(w http.ResponseWriter, r *http.Request) {
	station := r.URL.Query().Get("station")
	if station == "" {
		station = s.firstStation()
	}
	prs, _ := s.store.ListProbeResults(r.Context(), station, 100)
	rows := make([][]string, 0, len(prs))
	for _, p := range prs {
		rows = append(rows, []string{fmtTime(p.ObservedAt), p.StationID, p.Model,
			fmt.Sprintf("%d/%d", p.TokensIn, p.TokensOut),
			fmtUSD(p.DeclaredEffectiveUSDPer1M), fmtUSD(p.MeasuredUSDPer1M), fmtPct(p.MarkupPct), fmtUSD(p.CostUSD), p.Error})
	}
	writeHTML(w, "Probes · "+station, []string{"time", "station", "model", "tokens in/out", "declared $/M", "measured $/M", "markup%", "cost $", "error"}, rows)
}

func (s *Server) matrixHTML(w http.ResponseWriter, r *http.Request) {
	model := r.URL.Query().Get("model")
	var rows [][]string
	ctx := r.Context()
	for _, st := range s.stations {
		obs, err := s.store.LatestRatioObservations(ctx, st.ID)
		if err != nil {
			continue
		}
		for _, o := range obs {
			if model != "" && o.ModelName != model {
				continue
			}
			in, out, status := "-", "-", o.Sentinel
			if o.Sentinel == "" {
				in, out, status = fmtUSD(o.InputUSDPer1M), fmtUSD(o.OutputUSDPer1M), "ok"
			}
			rows = append(rows, []string{st.ID, o.GroupName, o.ModelName, in, out, status})
		}
	}
	writeHTML(w, "Cross-station matrix", []string{"station", "group", "model", "input $/M", "output $/M", "status"}, rows)
}

func (s *Server) auditHTML(w http.ResponseWriter, r *http.Request) {
	entries, _ := s.store.ListAuditLogs(r.Context(), 100)
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []string{fmtTime(e.Ts), e.Actor, e.Action, e.Target, e.Detail})
	}
	writeHTML(w, "Audit log", []string{"time", "actor", "action", "target", "detail"}, rows)
}

func writeHTML(w http.ResponseWriter, title string, cols []string, rows [][]string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(tableHTML(title, cols, rows)))
}

func tableHTML(title string, cols []string, rows [][]string) string {
	var b strings.Builder
	b.WriteString("<!doctype html><html><head><meta charset=utf-8><title>")
	b.WriteString(html.EscapeString(title))
	b.WriteString(`</title><style>body{font:14px/1.5 -apple-system,system-ui,sans-serif;margin:2rem;color:#222}table{border-collapse:collapse}td,th{border:1px solid #ddd;padding:4px 8px;text-align:left}a{color:#0366d6}</style></head><body>`)
	b.WriteString("<h1>")
	b.WriteString(html.EscapeString(title))
	b.WriteString("</h1><p><a href=\"/\">← overview</a></p><table><thead><tr>")
	for _, c := range cols {
		b.WriteString("<th>")
		b.WriteString(html.EscapeString(c))
		b.WriteString("</th>")
	}
	b.WriteString("</tr></thead><tbody>")
	for _, row := range rows {
		b.WriteString("<tr>")
		for _, cell := range row {
			b.WriteString("<td>")
			b.WriteString(html.EscapeString(cell))
			b.WriteString("</td>")
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table></body></html>")
	return b.String()
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}

func fmtUSD(v float64) string { return fmt.Sprintf("%.4f", v) }
func fmtPct(v float64) string { return fmt.Sprintf("%.2f%%", v) }
