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

func sortedModels(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

func (s *Server) changesHTML(w http.ResponseWriter, r *http.Request) {
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
			severityBadge(e.Severity),
		})
	}
	body := `<h1>变更</h1><p class="sub">站点 <span class="tag tag-pri">` + esc(station) +
		`</span> 的倍率/有效价变更（严重=红, 警告=黄）</p>` +
		renderTable([]string{"时间", "分组", "模型", "字段", "旧值", "新值", "变化%", "严重度"}, rows)
	writeHTMLShell(w, "变更 · "+station, "changes", body)
}

func (s *Server) probesHTML(w http.ResponseWriter, r *http.Request) {
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
		errCell := esc(p.Error)
		if p.Error == "" {
			errCell = `<span class="badge b-ok">ok</span>`
		}
		rows = append(rows, []string{
			`<span class="mono">` + fmtTime(p.ObservedAt) + `</span>`,
			`<span class="mono">` + esc(p.Model) + `</span>`,
			`<span class="num">` + fmt.Sprintf("%d/%d", p.TokensIn, p.TokensOut) + `</span>`,
			`<span class="num mono">` + fmtUSD(p.DeclaredEffectiveUSDPer1M) + `</span>`,
			`<span class="num mono">` + fmtUSD(p.MeasuredUSDPer1M) + `</span>`,
			fmt.Sprintf(`<span class="num %s">%s</span>`, mcls, fmtPct(p.MarkupPct)),
			`<span class="num mono">` + fmtUSD(p.CostUSD) + `</span>`,
			errCell,
		})
	}
	body := `<h1>真实成本探测</h1><p class="sub">站点 <span class="tag tag-pri">` + esc(station) +
		`</span> · 加价% = 真实(探测) vs 声明有效价（<span class="pcell p-high">红=暗中加价</span> / <span class="pcell p-cheap">绿=折扣</span>）</p>` +
		renderTable([]string{"时间", "模型", "token 入/出", "声明 $/M", "实测 $/M", "加价%", "成本 $", "状态"}, rows)
	writeHTMLShell(w, "探测 · "+station, "probes", body)
}

func (s *Server) matrixHTML(w http.ResponseWriter, r *http.Request) {
	model := r.URL.Query().Get("model")
	ctx := r.Context()
	type cell struct {
		input, output float64
		sentinel      string
		has           bool
	}
	stCells := make([]map[string]cell, len(s.stations))
	modelSet := map[string]bool{}
	for i, st := range s.stations {
		obs, _ := s.store.LatestRatioObservations(ctx, st.ID)
		m := map[string]cell{}
		for _, o := range obs {
			if model != "" && o.ModelName != model {
				continue
			}
			if _, ok := m[o.ModelName]; !ok {
				m[o.ModelName] = cell{o.InputUSDPer1M, o.OutputUSDPer1M, o.Sentinel, true}
				modelSet[o.ModelName] = true
			}
		}
		stCells[i] = m
	}
	models := sortedModels(modelSet)
	cols := []string{"模型"}
	for _, st := range s.stations {
		cols = append(cols, esc(st.ID))
	}
	rows := make([][]string, 0, len(models))
	for _, m := range models {
		lo, hi := math.MaxFloat64, -math.MaxFloat64
		for i := range s.stations {
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
		for i := range s.stations {
			c := stCells[i][m]
			switch {
			case !c.has:
				row = append(row, `<span class="pcell p-na">—</span>`)
			case c.sentinel != "":
				row = append(row, statusBadge(c.sentinel))
			default:
				row = append(row, fmt.Sprintf(`<span class="pcell %s">%s</span>`, priceColorClass(c.input, lo, hi), fmtUSD(c.input)))
			}
		}
		rows = append(rows, row)
	}
	sub := `<p class="sub">有效 USD/1M token 跨站对比 · <span class="pcell p-cheap">绿=最便宜</span> · <span class="pcell p-high">红=最贵</span> · 徽章=不可派生</p>`
	body := `<h1>跨站对比矩阵</h1>` + sub + renderTable(cols, rows)
	writeHTMLShell(w, "矩阵", "matrix", body)
}

func (s *Server) auditHTML(w http.ResponseWriter, r *http.Request) {
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
	body := `<h1>审计日志</h1><p class="sub">启动、探测、凭据持久化等动作记录</p>` +
		renderTable([]string{"时间", "角色", "动作", "目标", "详情"}, rows)
	writeHTMLShell(w, "审计", "audit", body)
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}

func fmtUSD(v float64) string { return fmt.Sprintf("%.4f", v) }
func fmtPct(v float64) string { return fmt.Sprintf("%.2f%%", v) }
