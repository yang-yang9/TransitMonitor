package dashboard

import (
	"context"
	"fmt"
	"html"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"transitmonitor/internal/domain"
)

// metricsHandler exposes Prometheus-format metrics. Bypasses auth (for scrapers).
func (s *Server) metricsHandler(w http.ResponseWriter, r *http.Request) {
	// For Prometheus scrapers (Accept: text/plain), serve raw. For browsers, wrap in HTML.
	isBrowser := strings.Contains(r.Header.Get("Accept"), "text/html")
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
	b.WriteString("# HELP transitmonitor_balance_remaining_usd Remaining account balance in USD (sub2api wallet / new-api quota → USD)\n")
	b.WriteString("# TYPE transitmonitor_balance_remaining_usd gauge\n")
	for _, st := range s.stationsList() {
		ob, err := s.store.LatestBalance(ctx, st.ID)
		if err != nil {
			continue
		}
		// Skip unlimited stations: their remaining has no "low" meaning and the
		// alert path (evaluateBalanceRules) skips them too. Emitting 0 would read
		// as a depleted wallet to a `balance_remaining_usd < 1` alerter.
		if ob.Unlimited {
			continue
		}
		fmt.Fprintf(&b, "transitmonitor_balance_remaining_usd{station=%q} %v\n", st.ID, ob.RemainingUSD)
	}
	if isBrowser {
		lang := s.lang(w, r)
		body := `<h1>` + t(lang, "nav.metrics") + `</h1><p class="sub">Prometheus exposition format — scrape at <code>` + r.Host + `/metrics</code></p><div class="card"><pre style="font-size:.8rem;white-space:pre-wrap;overflow-x:auto">` + b.String() + `</pre></div>`
		writeHTMLShell(w, lang, t(lang, "nav.metrics"), "metrics", body)
		return
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

func (s *Server) stationName(id string) string {
	if st, ok := s.findStation(id); ok && st.Name != "" {
		return st.Name
	}
	return id
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
	tab := r.URL.Query().Get("tab")
	if tab == "" {
		tab = "all"
	}
	evs, _ := s.store.ListChangeEvents(r.Context(), station, paginationCap)
	rows := make([][]string, 0, len(evs))
	timestamps := make([]time.Time, 0, len(evs))
	severities := make([]string, 0, len(evs))
	fields := make([]string, 0, len(evs))
	for _, e := range evs {
		isGroup := e.Field == domain.FieldGroupRatio
		switch tab {
		case "group":
			if !isGroup {
				continue
			}
		case "model":
			if isGroup {
				continue
			}
		}
		grpCell := esc(e.Group)
		if isGroup {
			grpCell = `<span class="grp-tag">` + esc(e.Group) + `</span>`
		}
		rows = append(rows, []string{
			`<span class="mono">` + fmtTime(e.ObservedAt) + `</span>`,
			grpCell,
			`<span class="mono">` + esc(e.Model) + `</span>`,
			`<span class="tag">` + fmtField(lang, e.Field) + `</span>`,
			`<span class="mono">` + esc(fmtChangeVal(lang, e.Old)) + `</span>`,
			`<span class="mono b-strong">` + esc(fmtChangeVal(lang, e.New)) + `</span>`,
			`<span class="num">` + fmtPct(e.DeltaPct) + `</span>`,
			severityBadge(lang, e.Severity),
		})
		timestamps = append(timestamps, e.ObservedAt)
		severities = append(severities, e.Severity)
		fields = append(fields, e.Field)
	}
	stName := s.stationName(station)
	stag := `<span class="tag tag-pri">` + esc(stName) + `</span>`
	tabBtn := func(key, labelKey string) string {
		cls := "btn btn-sm btn-outline"
		if key == tab {
			cls = "btn btn-sm"
		}
		return fmt.Sprintf(`<a class="%s" href="/changes?station=%s&tab=%s&_=%s">%s</a> `, cls, esc(station), key, matrixVer, t(lang, labelKey))
	}
	tabs := `<div class="field-sel">` + tabBtn("all", "btn.taball") + tabBtn("group", "btn.tabgroup") + tabBtn("model", "btn.tabmodel") + `</div>`
	pageRows, pg := paginateRows(lang, "/changes", "page", r.URL.Query(), rows)
	// slice timestamps/severities in sync with paginateRows
	total := len(rows)
	pages := (total + pageSize - 1) / pageSize
	if pages < 1 {
		pages = 1
	}
	page := atoiDefault(r.URL.Query().Get("page"), 1)
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	pageTS := timestamps[start:end]
	pageSev := severities[start:end]
	pageFields := fields[start:end]

	cols := []string{
		t(lang, "col.time"), t(lang, "col.group"), t(lang, "col.model"), t(lang, "col.field"),
		t(lang, "col.old"), t(lang, "col.new"), t(lang, "col.deltapct"), t(lang, "col.severity"),
	}
	body := `<div class="page-hdr"><h1>` + t(lang, "title.changes") + `</h1><p class="sub">` +
		fmt.Sprintf(t(lang, "sub.changes"), stag) + `</p></div>` +
		tabs + renderGroupedChangeTable(lang, cols, pageRows, pageTS, pageSev, pageFields) + pg
	writeHTMLShell(w, lang, t(lang, "title.changes")+" · "+stName, "changes", body)
}

func (s *Server) probesHTML(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	station := r.URL.Query().Get("station")
	if station == "" {
		station = s.firstStation()
	}
	prs, _ := s.store.ListProbeResults(r.Context(), station, paginationCap)
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
	stName := s.stationName(station)
	stag := `<span class="tag tag-pri">` + esc(stName) + `</span>`
	pageRows, pg := paginateRows(lang, "/probes", "page", r.URL.Query(), rows)
	body := `<div class="page-hdr"><h1>` + t(lang, "title.probes") + `</h1><p class="sub">` +
		fmt.Sprintf(t(lang, "sub.probes"), stag) + `</p></div>` +
		renderTable(lang, []string{
			t(lang, "col.time"), t(lang, "col.model"), t(lang, "col.tokinout"),
			t(lang, "col.declared"), t(lang, "col.measured"), t(lang, "col.markup"),
			t(lang, "col.cost"), t(lang, "col.status"),
		}, pageRows) + pg
	writeHTMLShell(w, lang, t(lang, "title.probes")+" · "+stName, "probes", body)
}

func (s *Server) matrixHTML(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "group"
	}
	model := r.URL.Query().Get("model")
	field := r.URL.Query().Get("field")
	if field == "" {
		field = "eff_in"
	}
	sortMode := r.URL.Query().Get("sort")
	if sortMode == "" {
		sortMode = "ratio"
	}
	group := r.URL.Query().Get("group")
	sts := s.stationsList() // cache once — avoids index-out-of-range if the list changes between calls

	// mode toggle (group × station is the default; model × station is the drill-down)
	toggle := `<div class="field-sel">` + t(lang, "col.field") + `: `
	toggle += modeBtn("group", mode, lang, "btn.matrixgroup")
	toggle += modeBtn("model", mode, lang, "btn.matrixmodel")
	toggle += `</div>`

	var tableHTML, subKey string
	if mode == "model" {
		tableHTML = s.matrixModelTable(lang, sts, field, model, group)
		subKey = "sub.matrixmodel"
	} else {
		tableHTML = s.matrixGroupTable(lang, sts, sortMode)
		subKey = "sub.matrix"
	}
	body := `<div class="page-hdr"><h1>` + t(lang, "title.matrix") + `</h1><p class="sub">` + t(lang, subKey) + `</p></div>` +
		toggle + tableHTML
	writeHTMLShell(w, lang, t(lang, "title.matrix"), "matrix", body)
}

// modeBtn renders a matrix mode toggle button (active when key==mode).
func modeBtn(key, mode, lang, labelKey string) string {
	cls := "btn btn-sm btn-outline"
	if key == mode {
		cls = "btn btn-sm"
	}
	return fmt.Sprintf(`<a class="%s" href="/matrix?mode=%s&_=%s">%s</a> `, cls, key, matrixVer, t(lang, labelKey))
}

// matrixGroupTable renders the group × station matrix: rows = union of groups,
// columns = stations, cells = that group's ratio at that station. Rows are
// ordered by sortMode: "ratio" = median ratio across stations (cheapest first,
// the default), "name" = alphabetical, "cov" = number of stations carrying the
// group (most coverage first).
func (s *Server) matrixGroupTable(lang string, sts []domain.Station, sortMode string) string {
	type stGR struct {
		name string
		gr   map[string]float64
	}
	rows := make([]stGR, len(sts))
	groupSet := map[string]bool{}
	for i, st := range sts {
		gr, _ := s.store.LatestGroupRatios(context.Background(), st.ID)
		rows[i] = stGR{name: st.Name, gr: gr}
		for g := range gr {
			groupSet[g] = true
		}
	}
	groups := make([]string, 0, len(groupSet))
	for g := range groupSet {
		groups = append(groups, g)
	}
	// per-group stats: median ratio + coverage (how many stations carry it).
	// Median (not mean) ignores outliers so a single wild station doesn't drag
	// a group to the top/bottom — it reflects the group's typical discount.
	type gStat struct {
		median float64
		cov    int
	}
	stats := map[string]gStat{}
	for _, g := range groups {
		vals := make([]float64, 0)
		for _, r := range rows {
			if v, ok := r.gr[g]; ok {
				vals = append(vals, v)
			}
		}
		sort.Float64s(vals)
		var med float64
		if n := len(vals); n > 0 {
			if n%2 == 1 {
				med = vals[n/2]
			} else {
				med = (vals[n/2-1] + vals[n/2]) / 2
			}
		}
		stats[g] = gStat{median: med, cov: len(vals)}
	}
	switch sortMode {
	case "name":
		sort.Strings(groups)
	case "cov":
		sort.Slice(groups, func(i, j int) bool {
			if stats[groups[i]].cov != stats[groups[j]].cov {
				return stats[groups[i]].cov > stats[groups[j]].cov
			}
			return groups[i] < groups[j]
		})
	default: // "ratio" — median ascending; tie-break by name for a stable, predictable order
		sort.SliceStable(groups, func(i, j int) bool {
			if stats[groups[i]].median != stats[groups[j]].median {
				return stats[groups[i]].median < stats[groups[j]].median
			}
			return groups[i] < groups[j]
		})
	}
	// compute lo/hi across all present ratios for coloring
	lo, hi := math.MaxFloat64, -math.MaxFloat64
	for _, r := range rows {
		for _, v := range r.gr {
			if v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
	}
	cols := []string{t(lang, "col.group")}
	for _, st := range sts {
		cols = append(cols, esc(st.Name))
	}
	dataRows := make([][]string, 0, len(groups))
	for _, g := range groups {
		st := stats[g]
		var tag string
		if st.cov > 0 {
			tag = ` <span class="cell-grp">` + fmt.Sprintf(t(lang, "meta.median_cov"), st.median, st.cov) + `</span>`
		}
		row := []string{`<span class="mono">` + esc(g) + `</span>` + tag}
		for _, r := range rows {
			v, ok := r.gr[g]
			if !ok {
				row = append(row, `<span class="gcell p-na">—</span>`)
			} else {
				row = append(row, fmt.Sprintf(`<span class="gcell %s">%.2fx</span>`, groupColorClass(v, lo, hi), v))
			}
		}
		dataRows = append(dataRows, row)
	}
	// sort-mode selector (group mode only)
	sortSel := `<div class="field-sel">` + t(lang, "col.sort") + `: `
	sortModes := []struct{ key, label string }{
		{"ratio", t(lang, "sort.ratio")},
		{"name", t(lang, "sort.name")},
		{"cov", t(lang, "sort.cov")},
	}
	for _, sm := range sortModes {
		cls := "btn btn-sm btn-outline"
		if sm.key == sortMode {
			cls = "btn btn-sm"
		}
		sortSel += fmt.Sprintf(`<a class="%s" href="/matrix?mode=group&sort=%s&_=%s">%s</a> `, cls, sm.key, matrixVer, sm.label)
	}
	sortSel += `</div>`
	return sortSel + renderTable(lang, cols, dataRows)
}

// groupColorClass colors a group-ratio cell: low = cheap (green), high = expensive (orange).
func groupColorClass(v, lo, hi float64) string {
	if hi <= lo {
		return "p-mid"
	}
	pos := (v - lo) / (hi - lo) // 0..1
	switch {
	case pos <= 0.33:
		return "p-cheap"
	case pos >= 0.66:
		return "p-high"
	default:
		return "p-mid"
	}
}

// matrixModelTable renders the model × station matrix. Each cell is one
// (station, model) price. With no group selected (groupFilter=="") it shows each
// station's cheapest non-sentinel group for that model and annotates the cell
// with the source group name; with a specific group selected it shows that
// group's price per station (— where the station lacks the model in that group).
func (s *Server) matrixModelTable(lang string, sts []domain.Station, field, modelFilter, groupFilter string) string {
	ctx := context.Background()
	type cell struct {
		val      float64
		sentinel string
		group    string
		has      bool
	}
	stCells := make([]map[string]cell, len(sts))
	modelSet := map[string]bool{}
	groupSet := map[string]bool{}
	for i, st := range sts {
		obs, _ := s.store.LatestRatioObservations(ctx, st.ID)
		m := map[string]cell{}
		for _, o := range obs {
			if modelFilter != "" && o.ModelName != modelFilter {
				continue
			}
			groupSet[o.GroupName] = true
			if groupFilter != "" && o.GroupName != groupFilter {
				continue
			}
			v := fieldVal(field, o)
			cur, ok := m[o.ModelName]
			if !ok {
				m[o.ModelName] = cell{val: v, sentinel: o.Sentinel, group: o.GroupName, has: true}
				modelSet[o.ModelName] = true
				continue
			}
			// Collapse multiple groups to the cheapest non-sentinel. When a
			// specific group is selected this branch is a no-op: LatestRatioObservations
			// returns one row per (group, model), so there's nothing to displace.
			if o.Sentinel == "" && (cur.sentinel != "" || v < cur.val) {
				m[o.ModelName] = cell{val: v, sentinel: o.Sentinel, group: o.GroupName, has: true}
			}
		}
		stCells[i] = m
	}
	models := sortedModels(modelSet)
	cols := []string{t(lang, "col.model")}
	for _, st := range sts {
		cols = append(cols, esc(st.Name))
	}
	rows := make([][]string, 0, len(models))
	for _, m := range models {
		lo, hi := math.MaxFloat64, -math.MaxFloat64
		for i := range sts {
			c := stCells[i][m]
			if c.has && c.sentinel == "" {
				if c.val < lo {
					lo = c.val
				}
				if c.val > hi {
					hi = c.val
				}
			}
		}
		row := []string{`<span class="mono">` + esc(m) + `</span>`}
		for i := range sts {
			c := stCells[i][m]
			switch {
			case !c.has:
				row = append(row, `<span class="pcell p-na">—</span>`)
			case c.sentinel != "":
				row = append(row, statusBadge(lang, c.sentinel))
			default:
				cellHTML := fmt.Sprintf(`<span class="pcell %s">%s</span>`, priceColorClass(c.val, lo, hi), fmtCell(field, c.val))
				// In cheapest mode, label the source group so the price is attributable.
				// (In specific-group mode the selector already states the group.)
				if groupFilter == "" {
					cellHTML += `<span class="cell-grp">` + esc(c.group) + `</span>`
				}
				row = append(row, cellHTML)
			}
		}
		rows = append(rows, row)
	}
	// field selector (model mode only) — preserves model + group across switches
	fields := []struct{ key, label string }{
		{"eff_in", t(lang, "col.effratio")},
		{"eff_out", t(lang, "col.effout")},
		{"ratio", t(lang, "col.modelratio")},
		{"input", t(lang, "col.inputusd")},
		{"output", t(lang, "col.outputusd")},
		{"cache_read", t(lang, "col.cacheread")},
		{"cache_write", t(lang, "col.cachewrite")},
	}
	selector := `<div class="field-sel">` + t(lang, "col.field") + `: <span class="cur-field">` + fieldLabel(field, lang) + `</span> · `
	for _, f := range fields {
		cls := "btn btn-sm btn-outline"
		if f.key == field {
			cls = "btn btn-sm"
		}
		q := "?mode=model&field=" + f.key + "&_=" + matrixVer
		if modelFilter != "" {
			q += "&model=" + esc(modelFilter)
		}
		if groupFilter != "" {
			q += "&group=" + esc(groupFilter)
		}
		selector += fmt.Sprintf(`<a class="%s" href="/matrix%s">%s</a> `, cls, q, f.label)
	}
	selector += `</div>`
	// group selector (model mode only) — "All (cheapest)" + one chip per group,
	// preserving field + model. Lets the user compare one group's prices cross-station.
	groups := make([]string, 0, len(groupSet))
	for g := range groupSet {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	groupSel := `<div class="field-sel">` + t(lang, "col.group") + `: `
	allCls := "btn btn-sm btn-outline"
	if groupFilter == "" {
		allCls = "btn btn-sm"
	}
	allQ := "?mode=model&field=" + field + "&_=" + matrixVer
	if modelFilter != "" {
		allQ += "&model=" + esc(modelFilter)
	}
	groupSel += fmt.Sprintf(`<a class="%s" href="/matrix%s">%s</a> `, allCls, allQ, t(lang, "btn.matrixallgroups"))
	for _, g := range groups {
		cls := "btn btn-sm btn-outline"
		if g == groupFilter {
			cls = "btn btn-sm"
		}
		q := "?mode=model&field=" + field + "&group=" + esc(g) + "&_=" + matrixVer
		if modelFilter != "" {
			q += "&model=" + esc(modelFilter)
		}
		groupSel += fmt.Sprintf(`<a class="%s" href="/matrix%s">%s</a> `, cls, q, esc(g))
	}
	groupSel += `</div>`
	return groupSel + selector + renderTable(lang, cols, rows)
}

// fmtCell formats a matrix cell value according to the active field:
// ratio fields use a "x" suffix; price fields use USD.
func fmtCell(field string, v float64) string {
	switch field {
	case "ratio", "eff_in", "eff_out":
		return fmt.Sprintf("%.4fx", v)
	default:
		return fmtUSD(v)
	}
}

// matrixVer is a cache-busting version for matrix field-selector links.
// Bump it whenever the matrix rendering changes so cached proxies/browsers
// re-fetch instead of serving a stale ?field=… response.
const matrixVer = "4"

func fieldLabel(field, lang string) string {
	switch field {
	case "eff_in":
		return t(lang, "col.effratio")
	case "eff_out":
		return t(lang, "col.effout")
	case "ratio":
		return t(lang, "col.modelratio")
	case "input":
		return t(lang, "col.inputusd")
	case "output":
		return t(lang, "col.outputusd")
	case "cache_read":
		return t(lang, "col.cacheread")
	case "cache_write":
		return t(lang, "col.cachewrite")
	}
	return field
}

func (s *Server) auditHTML(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	entries, _ := s.store.ListAuditLogs(r.Context(), paginationCap)
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
	pageRows, pg := paginateRows(lang, "/audit", "page", r.URL.Query(), rows)
	body := `<div class="page-hdr"><h1>` + t(lang, "title.audit") + `</h1><p class="sub">` + t(lang, "sub.audit") + `</p></div>` +
		renderTable(lang, []string{t(lang, "col.time"), t(lang, "col.actor"), t(lang, "col.action"), t(lang, "col.target"), t(lang, "col.detail")}, pageRows) + pg
	writeHTMLShell(w, lang, t(lang, "title.audit"), "audit", body)
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	// Render in the process timezone (set from config.timezone, default
	// Asia/Shanghai) so operators see wall-clock Beijing time, not UTC.
	return t.Local().Format("2006-01-02 15:04:05")
}

func fmtField(lang, field string) string {
	if v := t(lang, "field."+field); v != "field."+field {
		return v
	}
	return field
}

func fmtChangeVal(lang, val string) string {
	if v := t(lang, "val."+val); v != "val."+val {
		return v
	}
	return val
}

// renderGroupedChangeTable groups rows by ObservedAt timestamp. Batches with
// ≤3 rows render inline; larger batches are wrapped in a collapsible
// <details class="sec"> whose summary shows the timestamp, count and the
// highest severity badge in that batch. group_ratio rows are ALWAYS rendered
// inline (never collapsed) — ratio changes are the project's most important
// data and must stay visible.
func renderGroupedChangeTable(lang string, cols []string, rows [][]string, ts []time.Time, sevs []string, fields []string) string {
	if len(rows) == 0 {
		return renderTable(lang, cols, rows)
	}
	type batch struct {
		rows   [][]string
		fields []string
		sevs   []string
		sev    string
		ts     time.Time
	}
	var batches []batch
	cur := batch{ts: ts[0]}
	for i, row := range rows {
		if ts[i] != cur.ts && len(cur.rows) > 0 {
			batches = append(batches, cur)
			cur = batch{ts: ts[i]}
		}
		cur.rows = append(cur.rows, row)
		cur.fields = append(cur.fields, fields[i])
		cur.sevs = append(cur.sevs, sevs[i])
		if sevRank(sevs[i]) > sevRank(cur.sev) {
			cur.sev = sevs[i]
		}
	}
	batches = append(batches, cur)

	var b strings.Builder
	for _, bt := range batches {
		// Split: group_ratio rows always inline, rest may collapse.
		var ratioRows, otherRows [][]string
		otherSev := ""
		for i, row := range bt.rows {
			if bt.fields[i] == domain.FieldGroupRatio {
				ratioRows = append(ratioRows, row)
			} else {
				otherRows = append(otherRows, row)
				if sevRank(bt.sevs[i]) > sevRank(otherSev) {
					otherSev = bt.sevs[i]
				}
			}
		}
		hasRatio := len(ratioRows) > 0
		hasOther := len(otherRows) > 0
		// Wrap ratio + other in a batch container so they look connected.
		if hasRatio && hasOther {
			b.WriteString(`<div class="change-batch">`)
		}
		// Ratio rows: always flat, highlighted.
		if hasRatio {
			b.WriteString(renderHighlightedTable(lang, cols, ratioRows, "ratio-row"))
		}
		// Other rows: collapse if >3, otherwise flat.
		if hasOther {
			if len(otherRows) <= 3 {
				b.WriteString(renderTable(lang, cols, otherRows))
			} else {
				summary := fmt.Sprintf(t(lang, "batch.summary"), fmtTime(bt.ts), len(otherRows))
				b.WriteString(`<details class="sec"><summary>` + summary + ` ` + severityBadge(lang, otherSev) + `</summary>`)
				b.WriteString(renderTable(lang, cols, otherRows))
				b.WriteString(`</details>`)
			}
		}
		if hasRatio && hasOther {
			b.WriteString(`</div>`)
		}
	}
	return b.String()
}

// renderHighlightedTable is like renderTable but adds a CSS class to every <tr>.
func renderHighlightedTable(lang string, cols []string, rows [][]string, rowClass string) string {
	var b strings.Builder
	b.WriteString(`<div class="tbl-wrap"><table><thead><tr>`)
	for _, c := range cols {
		b.WriteString("<th>")
		b.WriteString(html.EscapeString(c))
		b.WriteString("</th>")
	}
	b.WriteString("</tr></thead><tbody>")
	for _, row := range rows {
		b.WriteString(`<tr class="` + rowClass + `">`)
		for _, cell := range row {
			b.WriteString("<td>")
			b.WriteString(cell)
			b.WriteString("</td>")
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table></div>")
	return b.String()
}

func sevRank(s string) int {
	switch s {
	case "critical":
		return 3
	case "warning":
		return 2
	case "info":
		return 1
	default:
		return 0
	}
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
	case "ratio":
		// native multiplier (new-api model_ratio / sub2api rate_multiplier)
		return o.NativeRatio
	case "eff_in":
		// effective input ratio = InputUSDPer1M / ($2 per ratio unit per 1M)
		return o.InputUSDPer1M / 2.0
	case "eff_out":
		return o.OutputUSDPer1M / 2.0
	default:
		return o.InputUSDPer1M
	}
}

func (s *Server) alertsHTML(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	rows_data, _ := s.store.ListAlertEvents(r.Context(), paginationCap)
	rows := make([][]string, 0, len(rows_data))
	for _, a := range rows_data {
		sentBadge := `<span class="badge b-ok">✓</span>`
		if !a.Sent {
			sentBadge = `<span class="badge b-crit">✗</span>`
		}
		rows = append(rows, []string{
			`<span class="mono">` + fmtTime(a.Ts) + `</span>`,
			`<span class="tag">` + esc(a.Rule) + `</span>`,
			`<span class="mono">` + esc(s.stationName(a.StationID)) + `</span>`,
			`<span class="mono">` + esc(a.Model) + `</span>`,
			`<span style="font-size:.8rem;color:var(--muted)">` + esc(truncate(a.Payload, 80)) + `</span>`,
			sentBadge,
			esc(a.Error),
		})
	}
	pageRows, pg := paginateRows(lang, "/alerts", "page", r.URL.Query(), rows)
	body := `<h1>` + t(lang, "title.alerts") + `</h1><p class="sub">` + t(lang, "sub.alerts") + `</p>` +
		renderTable(lang, []string{t(lang, "col.time"), "rule", "station", "model", "payload", "sent", "error"}, pageRows) + pg
	writeHTMLShell(w, lang, t(lang, "title.alerts"), "alerts", body)
}
func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// balanceHTML renders the per-station balance overview: a table of the latest
// reading (remaining/used/total USD) plus a sparkline trend per station.
func (s *Server) balanceHTML(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	ctx := r.Context()
	sts := s.stationsList()
	type row struct {
		station, name string
		ob            domain.BalanceObservation
		has           bool
		spark         string
	}
	rows := make([]row, 0, len(sts))
	for _, st := range sts {
		ob, err := s.store.LatestBalance(ctx, st.ID)
		if err != nil {
			rows = append(rows, row{station: st.ID, name: st.Name})
			continue
		}
		spark := ""
		if hist, err := s.store.BalanceHistory(ctx, st.ID, 24); err == nil && len(hist) >= 2 {
			vals := make([]float64, 0, len(hist))
			for _, h := range hist {
				vals = append(vals, h.RemainingUSD)
			}
			spark = sparklineSVG(vals, 120, 32, "$%.2f")
		}
		rows = append(rows, row{station: st.ID, name: st.Name, ob: ob, has: true, spark: spark})
	}
	// Sort: stations with data first, then by remaining USD ascending (lowest on top).
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].has != rows[j].has {
			return rows[i].has
		}
		if !rows[i].has {
			return rows[i].name < rows[j].name
		}
		return rows[i].ob.RemainingUSD < rows[j].ob.RemainingUSD
	})
	cells := make([][]string, 0, len(rows))
	for _, rw := range rows {
		if !rw.has {
			cells = append(cells, []string{
				`<a class="mono" href="/stations/` + esc(rw.station) + `">` + esc(rw.name) + `</a>`,
				`<span class="gcell p-na">—</span>`, ``, ``, ``, ``, ``,
			})
			continue
		}
		cls := "b-ok"
		switch {
		case rw.ob.Unlimited:
			cls = "b-ok"
		case rw.ob.RemainingUSD < 1:
			cls = "b-crit"
		case rw.ob.TotalUSD > 0 && rw.ob.RemainingUSD/rw.ob.TotalUSD < 0.2:
			cls = "b-warn"
		}
		remCell := fmt.Sprintf(`<span class="badge-sm %s">$%.2f</span>`, cls, rw.ob.RemainingUSD)
		if rw.ob.Unlimited {
			remCell = fmt.Sprintf(`<span class="badge-sm b-ok">∞</span> %s`, t(lang, "balance.unlimited"))
		}
		usedCell := fmt.Sprintf(`<span class="num mono">$%.2f</span>`, rw.ob.UsedUSD)
		totalCell := "—"
		if rw.ob.TotalUSD > 0 {
			totalCell = fmt.Sprintf(`<span class="num mono">$%.2f</span>`, rw.ob.TotalUSD)
		} else if rw.ob.Unlimited {
			totalCell = t(lang, "balance.unlimited")
		}
		cells = append(cells, []string{
			`<a class="mono" href="/stations/` + esc(rw.station) + `">` + esc(rw.name) + `</a>`,
			remCell,
			usedCell,
			totalCell,
			`<span class="mono">` + fmtTime(rw.ob.ObservedAt) + `</span>`,
			`<span class="tag">` + esc(rw.ob.SourceEndpoint) + `</span>`,
			rw.spark,
		})
	}
	body := `<div class="page-hdr"><h1>` + t(lang, "title.balance") + `</h1><p class="sub">` + t(lang, "sub.balance") + `</p></div>` +
		renderTable(lang, []string{
			t(lang, "col.station"), t(lang, "col.remaining"), t(lang, "col.used"),
			t(lang, "col.total"), t(lang, "col.lastupdate"), t(lang, "col.source"), t(lang, "balance.trend"),
		}, cells)
	writeHTMLShell(w, lang, t(lang, "title.balance"), "balance", body)
}
