package dashboard

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"transitmonitor/internal/domain"
	"transitmonitor/internal/jwtlogin"
)

// StationManager lets the dashboard add/remove stations at runtime
// (implemented by *scheduler.Scheduler).
type StationManager interface {
	AddStation(domain.Station) error
	RemoveStation(string) error
	Stations() []domain.Station
}

// SetManager wires a runtime station manager (enables web CRUD).
func (s *Server) SetManager(m StationManager) { s.mgr = m }

// stationsList returns the live station list (manager if set, else the static list).
func (s *Server) stationsList() []domain.Station {
	if s.mgr != nil {
		return s.mgr.Stations()
	}
	return s.stations
}

// decryptFailedCount returns how many stations loaded with undecryptable creds.
func (s *Server) decryptFailedCount() int {
	n := 0
	for _, st := range s.stationsList() {
		if st.DecryptFailed {
			n++
		}
	}
	return n
}

func (s *Server) findStation(id string) (domain.Station, bool) {
	for _, st := range s.stationsList() {
		if st.ID == id {
			return st, true
		}
	}
	return domain.Station{}, false
}

// GET /stations — management page: list + add link + edit/delete buttons.
func (s *Server) stationsPage(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	sts := s.stationsList()
	rows := make([][]string, 0, len(sts))
	for _, st := range sts {
		enabled := `<span class="badge b-ok">` + t(lang, "form.enabled") + `</span>`
		if !st.Enabled {
			enabled = `<span class="badge b-muted">—</span>`
		}
		nameCell := esc(st.Name)
		if st.DecryptFailed {
			nameCell = `<a href="/stations/` + esc(st.ID) + `/edit">` + esc(st.Name) + `</a> <span class="badge b-crit" title="` + esc(t(lang, "badge.decrypt_failed")) + `">⚠ ` + esc(t(lang, "badge.decrypt_failed")) + `</span>`
		}
		edit := `<a class="btn btn-outline btn-sm" href="/stations/` + esc(st.ID) + `/edit">` + t(lang, "form.edit") + `</a> <button class="btn btn-outline btn-sm" onclick="fetch('/api/stations/` + esc(st.ID) + `/poll',{method:'POST'}).then(function(){location.reload();})">🔄 ` + t(lang, "form.poll") + `</button>`
		del := `<button class="btn btn-danger btn-sm" onclick="tmDel('` + esc(st.ID) + `')">` + t(lang, "form.delete") + `</button>`
		rows = append(rows, []string{
			`<span class="mono">` + esc(st.ID) + `</span>`, nameCell,
			`<span class="tag tag-pri">` + esc(string(st.Kind)) + `</span>`,
			`<span class="mono" style="font-size:.8rem">` + esc(st.BaseURL) + `</span>`, enabled,
			`<div style="display:flex;gap:.4rem">` + edit + ` ` + del + `</div>`,
		})
	}
	body := `<div class="page-hdr"><h1>` + t(lang, "title.stations") + `</h1>` +
		`<p class="sub"><a class="btn" href="/stations/new">+ ` + t(lang, "title.newstation") + `</a></p></div>` +
		renderTable(lang, []string{t(lang, "form.id"), t(lang, "form.name"), t(lang, "form.kind"), t(lang, "form.baseurl"), t(lang, "form.enabled"), ""}, rows) +
		`<script>function tmDel(id){tmConfirm('` + t(lang, "form.confirm") + `',function(){fetch('/api/stations/'+id,{method:'DELETE'}).then(function(){location.reload();});});}</script>`
	writeHTMLShell(w, lang, t(lang, "title.stations"), "stations", body)
}

// GET /stations/{id} — station detail: ratios + recent changes + probes.
func (s *Server) stationDetailHTML(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	id := chi.URLParam(r, "id")
	st, ok := s.findStation(id)
	if !ok {
		http.Error(w, "station not found", http.StatusNotFound)
		return
	}
	ctx := r.Context()
	// group ratios (loaded early so the ratios table can show per-row group_ratio)
	groupRatios, _ := s.store.LatestGroupRatios(ctx, id)
	// ratios — sorted by group then cheapest effective-input first; with a
	// visual bar on the effective ratio so magnitude is readable at a glance.
	obs, _ := s.store.LatestRatioObservations(ctx, id)
	type rob struct {
		o             domain.RatioObservation
		gr            float64
		cr            float64
		effIn, effOut float64
	}
	rows := make([]rob, 0, len(obs))
	maxOut := 0.0
	for _, o := range obs {
		gr, hasGR := groupRatios[o.GroupName]
		if !hasGR {
			gr = 1.0
		}
		cr := o.CompletionRatio
		if cr == 0 {
			cr = 1.0
		}
		ei := o.NativeRatio * gr
		eo := o.NativeRatio * cr * gr
		if eo > maxOut {
			maxOut = eo
		}
		rows = append(rows, rob{o, gr, cr, ei, eo})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].o.GroupName != rows[j].o.GroupName {
			return rows[i].o.GroupName < rows[j].o.GroupName
		}
		return rows[i].effIn < rows[j].effIn
	})
	ratioRows := make([][]string, 0, len(rows))
	prevGroup := ""
	for _, r := range rows {
		_ = prevGroup // grouping handled in renderRatioTable via GroupName changes
		pct := 0.0
		if maxOut > 0 {
			pct = r.effOut / maxOut * 100
		}
		effCell := fmt.Sprintf(`<div class="rat-bar"><span class="rb-fill" style="width:%.1f%%"></span></div>`, pct) +
			`<span class="num mono b-strong">` + fmt.Sprintf("%.4f", r.effIn) + ` / ` + fmt.Sprintf("%.4f", r.effOut) + `</span>`
		ratioRows = append(ratioRows, []string{
			esc(r.o.GroupName), `<span class="mono">` + esc(r.o.ModelName) + `</span>`,
			`<span class="num mono">` + fmt.Sprintf("%.4f", r.o.NativeRatio) + `</span>`,
			`<span class="num mono">` + fmt.Sprintf("%.4f", r.cr) + `</span>`,
			`<span class="num mono">` + fmt.Sprintf("%.2fx", r.gr) + `</span>`,
			effCell,
			statusBadge(lang, r.o.Sentinel),
		})
	}
	// changes
	evs, _ := s.store.ListChangeEvents(ctx, id, paginationCap)
	// probes
	prs, _ := s.store.ListProbeResults(ctx, id, paginationCap)
	probeRows := make([][]string, 0, len(prs))
	for _, p := range prs {
		mcls := "p-mid"
		if p.MarkupPct > 0 {
			mcls = "p-high"
		} else if p.MarkupPct < 0 {
			mcls = "p-cheap"
		}
		probeRows = append(probeRows, []string{
			`<span class="mono">` + fmtTime(p.ObservedAt) + `</span>`,
			`<span class="mono">` + esc(p.Model) + `</span>`,
			`<span class="num mono">` + fmtUSD(p.DeclaredEffectiveUSDPer1M) + `</span>`,
			`<span class="num mono">` + fmtUSD(p.MeasuredUSDPer1M) + `</span>`,
			fmt.Sprintf(`<span class="num %s">%s</span>`, mcls, fmtPct(p.MarkupPct)),
			statusBadge(lang, p.Error),
		})
	}
	pollErrs, _ := s.store.CountPollErrors(ctx, id)
	uptime := "100%"
	if pollErrs > 0 {
		uptime = fmt.Sprintf("%d errors", pollErrs)
	}
	// HERO: large group-ratio bar chart.
	heroChart := groupRatioChart(groupRatios, true)
	// group-ratio trend sparklines: per group, ratio over recent snapshots.
	var trendHTML string
	if hist, _ := s.store.GroupRatioHistory(ctx, id, 24); len(hist) >= 2 {
		// collect per-group series
		type series struct{ vals []float64 }
		gSer := map[string]*series{}
		order := []string{}
		for _, h := range hist {
			for g, v := range h.Ratios {
				if _, ok := gSer[g]; !ok {
					gSer[g] = &series{}
					order = append(order, g)
				}
				gSer[g].vals = append(gSer[g].vals, v)
			}
		}
		sort.Strings(order)
		trendHTML = `<h2>` + t(lang, "section.grouptrend") + `</h2><div class="spark-grid">`
		for _, g := range order {
			vals := gSer[g].vals
			cur := vals[len(vals)-1]
			// delta vs the previous snapshot's ratio for this group
			deltaStr := ""
			if len(vals) >= 2 {
				pv := vals[len(vals)-2]
				if pv != 0 {
					dp := (cur - pv) / pv * 100
					sign := "+"
					cls := "b-cheap"
					if dp < 0 {
						sign = ""
						cls = "b-cheap"
					} else if dp > 0 {
						cls = "b-warn"
					}
					_ = cls
					deltaStr = fmt.Sprintf(`<span class="sc-delta badge-sm b-warn">%s%.1f%%</span>`, sign, dp)
				}
			}
			svg := sparklineSVG(vals, 120, 32)
			trendHTML += fmt.Sprintf(`<div class="spark-cell"><div class="sc-hdr"><span class="sc-name" title="%s">%s</span><span class="sc-val">%.2fx</span></div>%s%s</div>`,
				esc(g), esc(g), cur, svg, deltaStr)
		}
		trendHTML += `</div>`
	}
	// group-ratio changes section.
	var groupChangeRows [][]string
	var modelChangeRows [][]string
	for _, e := range evs {
		if e.Field == domain.FieldGroupRatio {
			groupChangeRows = append(groupChangeRows, []string{
				`<span class="mono">` + fmtTime(e.ObservedAt) + `</span>`,
				`<span class="grp-tag">` + esc(e.Group) + `</span>`,
				`<span class="num mono">` + esc(e.Old) + `</span>`,
				`<span class="num mono b-strong">` + esc(e.New) + `</span>`,
				`<span class="num">` + fmtPct(e.DeltaPct) + `</span>`,
				severityBadge(lang, e.Severity),
			})
		} else {
			modelChangeRows = append(modelChangeRows, []string{
				`<span class="mono">` + fmtTime(e.ObservedAt) + `</span>`,
				`<span class="mono">` + esc(e.Model) + `</span>`,
				`<span class="tag">` + fmtField(lang, e.Field) + `</span>`,
				`<span class="num">` + fmtPct(e.DeltaPct) + `</span>`,
				severityBadge(lang, e.Severity),
			})
		}
	}
	// paginate the three log tables (group + model changes + probes); the
	// <details> summaries show full totals, the tables show the current page.
	q := r.URL.Query()
	groupPage, gpg := paginateRows(lang, "/stations/"+id, "cpage", q, groupChangeRows)
	modelPage, mpg := paginateRows(lang, "/stations/"+id, "mcpage", q, modelChangeRows)
	probePage, ppg := paginateRows(lang, "/stations/"+id, "ppage", q, probeRows)
	info := `<span class="tag tag-pri">` + esc(string(st.Kind)) + `</span> ` + esc(st.BaseURL) +
		` <span class="badge b-warn">⚠ ` + uptime + `</span>` +
		` <a class="btn btn-outline btn-sm" href="/stations/` + esc(st.ID) + `/edit">` + t(lang, "form.edit") + `</a>`
	body := `<div class="page-hdr"><h1>` + esc(st.Name) + `</h1><p class="sub">` + info + `</p></div>`
	if st.DecryptFailed {
		body += `<div class="card" style="border-left:3px solid var(--crit,#ef4444);background:var(--bg-2)"><span class="badge b-crit">⚠</span> ` +
			esc(t(lang, "badge.decrypt_failed")) + ` — <a href="/stations/` + esc(st.ID) + `/edit">` + t(lang, "form.edit") + `</a></div>`
	}
	body +=
		heroChart +
			trendHTML +
			`<h2>` + t(lang, "section.groupchanges") + `</h2>` + renderTable(lang, []string{t(lang, "col.time"), t(lang, "col.group"), t(lang, "col.oldratio"), t(lang, "col.newratio"), t(lang, "col.deltapct"), t(lang, "col.severity")}, groupPage) + gpg +
			`<details class="sec"><summary>` + t(lang, "expand.models") + ` (` + fmt.Sprintf("%d", len(ratioRows)) + `)</summary>` +
			renderRatioTable([]string{t(lang, "col.group"), t(lang, "col.model"), t(lang, "col.modelratio"), t(lang, "col.completionratio"), t(lang, "col.groupratio"), t(lang, "col.effratio"), t(lang, "col.status")}, ratioRows) + `</details>` +
			`<details class="sec"><summary>` + t(lang, "expand.modelchanges") + ` (` + fmt.Sprintf("%d", len(modelChangeRows)) + `)</summary>` +
			renderTable(lang, []string{t(lang, "col.time"), t(lang, "col.model"), t(lang, "col.field"), t(lang, "col.deltapct"), t(lang, "col.severity")}, modelPage) + mpg + `</details>` +
			`<details class="sec"><summary>` + t(lang, "expand.probes") + ` (` + fmt.Sprintf("%d", len(probeRows)) + `)</summary>` +
			renderTable(lang, []string{t(lang, "col.time"), t(lang, "col.model"), t(lang, "col.declared"), t(lang, "col.measured"), t(lang, "col.markup"), t(lang, "col.status")}, probePage) + ppg + `</details>`
	writeHTMLShell(w, lang, esc(st.Name), "stations", body)
}

// GET /stations/new — add-station form.
func (s *Server) stationFormHTML(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	writeHTMLShell(w, lang, t(lang, "title.newstation"), "stations", stationForm(lang, nil))
}

// GET /stations/{id}/edit — edit-station form (pre-fills non-secret fields; secrets blank = keep).
func (s *Server) stationEditHTML(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	st, ok := s.findStation(chi.URLParam(r, "id"))
	if !ok {
		http.Error(w, "station not found", http.StatusNotFound)
		return
	}
	writeHTMLShell(w, lang, t(lang, "title.editstation"), "stations", stationForm(lang, &st))
}

// stationForm renders the add (edit==nil) or edit form. Secret fields are never
// pre-filled; in edit mode they're blank with a "keep blank" placeholder, and the
// PUT handler keeps the existing secret for any blank field.
func stationForm(lang string, edit *domain.Station) string {
	method, action, idVal, idRO, title, submit := "POST", "/api/stations", "", "", t(lang, "title.newstation"), t(lang, "form.add")
	idPH := t(lang, "form.id.auto")
	nameVal, baseVal, groupVal, pollVal := "", "", "default", "3m"
	kindNew, kindSub := "selected", ""
	apiPH, patPH, jwtPH := "sk-...", "new-api PAT", "sub2api user JWT"
	userIDVal, userIDPH := "", "new-api user ID (for New-Api-User header)"
	adminEmailPH, adminPassPH := "admin@example.com", "password"
	checkedAttr := "checked"
	loginBtn := ""
	if edit != nil {
		method, action, idVal, idRO, title, submit = "PUT", "/api/stations/"+edit.ID, edit.ID, "readonly", t(lang, "title.editstation"), t(lang, "form.save")
		idPH = ""
		nameVal, baseVal, pollVal = edit.Name, edit.BaseURL, time.Duration(edit.PollInterval).String()
		groupVal = edit.Auth.Group
		if groupVal == "" {
			groupVal = "default"
		}
		kindNew, kindSub = "", ""
		if edit.Kind == domain.KindSub2API {
			kindSub = "selected"
		} else {
			kindNew = "selected"
		}
		checkedAttr = ""
		if edit.Enabled {
			checkedAttr = "checked"
		}
		apiPH, patPH, jwtPH = t(lang, "form.keepblank"), t(lang, "form.keepblank"), t(lang, "form.keepblank")
		adminEmailPH, adminPassPH = t(lang, "form.keepblank"), t(lang, "form.keepblank")
		userIDVal = edit.Auth.UserID
		userIDPH = t(lang, "form.keepblank")
		if edit.Kind == domain.KindSub2API {
			loginBtn = `<button class="btn btn-outline btn-sm" type="button" onclick="tmFetchJWT('` + esc(edit.ID) + `')">` + t(lang, "form.fetchjwt") + `</button>`
		}
	}
	return `<div class="form-wrap"><div class="page-hdr"><h1>` + title + `</h1></div>
<div class="card">
<form id="stform" onsubmit="return tmSubmit('` + method + `','` + action + `')">
  <div class="form-grid">
    <div class="field"><span class="field-label">` + t(lang, "form.id") + `</span><input name="id" value="` + esc(idVal) + `" placeholder="` + esc(idPH) + `" ` + idRO + `></div>
    <div class="field"><span class="field-label">` + t(lang, "form.name") + `</span><input name="name" required value="` + esc(nameVal) + `"></div>
    <div class="field full"><span class="field-label">` + t(lang, "form.baseurl") + `</span><input name="base_url" required value="` + esc(baseVal) + `" placeholder="https://relay.example.com"></div>
    <div class="field"><span class="field-label">` + t(lang, "form.kind") + `</span><select name="kind" onchange="tmKindSwitch(this.value)"><option value="newapi" ` + kindNew + `>newapi</option><option value="sub2api" ` + kindSub + `>sub2api</option></select></div>
    <div class="field"><span class="field-label">` + t(lang, "form.group") + `</span><input name="group" value="` + esc(groupVal) + `"></div>
    <div class="field"><span class="field-label">` + t(lang, "form.pollinterval") + `</span><input name="poll_interval" value="` + esc(pollVal) + `"></div>
    <div></div>
    <hr class="form-sep">
    <div class="field"><span class="field-label">` + t(lang, "form.apikey") + `</span><input name="api_key" placeholder="` + apiPH + `"></div>
  </div>
  <div id="tm-kind-newapi" class="form-grid">
    <div class="field"><span class="field-label">` + t(lang, "form.pat") + `</span><input name="pat" placeholder="` + patPH + `"></div>
    <div class="field"><span class="field-label">` + t(lang, "form.userid") + `</span><input name="user_id" value="` + esc(userIDVal) + `" placeholder="` + esc(userIDPH) + `"></div>
  </div>
  <div id="tm-kind-sub2api" class="form-grid">
    <div class="field"><span class="field-label">` + t(lang, "form.jwt") + `</span><input name="jwt" placeholder="` + jwtPH + `"></div>
    <div class="field"><span class="field-label">` + t(lang, "form.admin_email") + `</span><input name="admin_email" placeholder="` + esc(adminEmailPH) + `"></div>
    <div class="field"><span class="field-label">` + t(lang, "form.admin_pass") + `</span><input name="admin_pass" type="password" placeholder="` + esc(adminPassPH) + `"></div>
    <div class="field">` + loginBtn + `</div>
  </div>
  <div class="form-grid">
    <div class="field"><span class="field-label">` + t(lang, "form.enabled") + `</span><label class="toggle"><input type="checkbox" name="enabled" ` + checkedAttr + `><span class="slider"></span>` + t(lang, "form.enabled") + `</label></div>
  </div>
  <div class="btn-group" style="margin-top:1.2rem"><button class="btn" type="submit">` + submit + `</button><a class="btn btn-outline" href="/stations">&larr; ` + t(lang, "title.stations") + `</a></div>
</form>
</div>
<script>
function tmKindSwitch(k){
  document.getElementById('tm-kind-newapi').style.display = k==='newapi'?'':'none';
  document.getElementById('tm-kind-sub2api').style.display = k==='sub2api'?'':'none';
}
tmKindSwitch(document.querySelector('[name=kind]').value);
function tmSubmit(m,u){
  var f=document.getElementById('stform'), v=function(n){var el=f[n]; if(!el) return ''; if(el.type=='checkbox') return el.checked; return el.value;};
  var st={id:v('id'),name:v('name'),base_url:v('base_url'),kind:v('kind'),auth:{api_key:v('api_key'),pat:v('pat'),user_id:v('user_id'),jwt:v('jwt'),admin_email:v('admin_email'),admin_pass:v('admin_pass'),group:v('group')},poll_interval:v('poll_interval'),enabled:!!v('enabled')};
  fetch(u,{method:m,headers:{'Content-Type':'application/json'},body:JSON.stringify(st)}).then(function(r){if(r.ok){location.href='/stations';}else{r.text().then(function(t){alert(t);});}}).catch(function(e){alert(e);});
  return false;
}
function tmFetchJWT(id){
  var f=document.getElementById('stform'), v=function(n){var el=f[n]; if(!el) return ''; return el.value;};
  var email=v('admin_email'), pass=v('admin_pass');
  if(email||pass){
    var st={id:v('id'),name:v('name'),base_url:v('base_url'),kind:v('kind'),auth:{api_key:v('api_key'),pat:v('pat'),user_id:v('user_id'),jwt:v('jwt'),admin_email:email,admin_pass:pass,group:v('group')},poll_interval:v('poll_interval'),enabled:!!f['enabled'].checked};
    fetch('/api/stations/'+id,{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(st)}).then(function(){
      return fetch('/api/stations/'+id+'/login',{method:'POST'});
    }).then(function(r){return r.json();}).then(function(d){
      if(d.error){alert(d.error);}else{alert(d.message);location.reload();}
    }).catch(function(e){alert(e);});
  } else {
    fetch('/api/stations/'+id+'/login',{method:'POST'}).then(function(r){return r.json();}).then(function(d){
      if(d.error){alert(d.error);}else{alert(d.message);location.reload();}
    }).catch(function(e){alert(e);});
  }
}
</script>
</div>`
}

// stationInput is the JSON shape the form/API sends (poll_interval as a human string).
type stationInput struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	Kind    string `json:"kind"`
	Auth    struct {
		APIKey     string `json:"api_key"`
		PAT        string `json:"pat"`
		UserID     string `json:"user_id"`
		JWT        string `json:"jwt"`
		AdminEmail string `json:"admin_email"`
		AdminPass  string `json:"admin_pass"`
		Group      string `json:"group"`
	} `json:"auth"`
	PollInterval string `json:"poll_interval"`
	Enabled      bool   `json:"enabled"`
}

func (in stationInput) toStation() (domain.Station, error) {
	d, err := time.ParseDuration(in.PollInterval)
	if err != nil {
		return domain.Station{}, err
	}
	return domain.Station{
		ID: in.ID, Name: in.Name, BaseURL: in.BaseURL, Kind: domain.StationKind(in.Kind),
		Auth: domain.AuthConfig{
			APIKey: in.Auth.APIKey, PAT: in.Auth.PAT, UserID: in.Auth.UserID,
			JWT: in.Auth.JWT, AdminEmail: in.Auth.AdminEmail, AdminPass: in.Auth.AdminPass,
			Group: in.Auth.Group,
		},
		PollInterval: domain.Duration(d), Enabled: in.Enabled,
	}, nil
}

// POST /api/stations
func (s *Server) stationsCreate(w http.ResponseWriter, r *http.Request) {
	if s.mgr == nil {
		http.Error(w, "station manager not configured", http.StatusServiceUnavailable)
		return
	}
	var in stationInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if in.Name == "" || in.BaseURL == "" || in.Kind == "" {
		http.Error(w, "name, base_url, kind required", http.StatusBadRequest)
		return
	}
	if in.Kind != "newapi" && in.Kind != "sub2api" {
		http.Error(w, "kind must be newapi or sub2api", http.StatusBadRequest)
		return
	}
	if in.Auth.APIKey == "" {
		http.Error(w, "api_key (sk-) required", http.StatusBadRequest)
		return
	}
	if in.ID == "" {
		for i := 0; i < 8; i++ {
			cand := genStationID()
			if _, ok := s.findStation(cand); !ok {
				in.ID = cand
				break
			}
		}
		if in.ID == "" {
			http.Error(w, "could not generate unique station id", http.StatusInternalServerError)
			return
		}
	}
	st, err := in.toStation()
	if err != nil {
		http.Error(w, "poll_interval: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.mgr.AddStation(st); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 201, map[string]string{"id": st.ID, "status": "added"})
}

// PUT /api/stations/{id} — upsert (edit). Blank secret fields keep the existing value.
func (s *Server) stationsUpsert(w http.ResponseWriter, r *http.Request) {
	if s.mgr == nil {
		http.Error(w, "station manager not configured", http.StatusServiceUnavailable)
		return
	}
	var in stationInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	in.ID = chi.URLParam(r, "id")
	if existing, ok := s.findStation(in.ID); ok {
		if in.Auth.APIKey == "" {
			in.Auth.APIKey = existing.Auth.APIKey
		}
		if in.Auth.PAT == "" {
			in.Auth.PAT = existing.Auth.PAT
		}
		if in.Auth.UserID == "" {
			in.Auth.UserID = existing.Auth.UserID
		}
		if in.Auth.JWT == "" {
			in.Auth.JWT = existing.Auth.JWT
		}
		if in.Auth.AdminEmail == "" {
			in.Auth.AdminEmail = existing.Auth.AdminEmail
		}
		if in.Auth.AdminPass == "" {
			in.Auth.AdminPass = existing.Auth.AdminPass
		}
		if in.Auth.Group == "" {
			in.Auth.Group = existing.Auth.Group
		}
	}
	st, err := in.toStation()
	if err != nil {
		http.Error(w, "poll_interval: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.mgr.AddStation(st); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, map[string]string{"id": st.ID, "status": "upserted"})
}

// genStationID returns a short URL-safe random id (st-<8hex>).
func genStationID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "st-" + hex.EncodeToString(b)
}

// DELETE /api/stations/{id}
func (s *Server) stationsDelete(w http.ResponseWriter, r *http.Request) {
	if s.mgr == nil {
		http.Error(w, "station manager not configured", http.StatusServiceUnavailable)
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.mgr.RemoveStation(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(204)
}

// sparklineSVG renders a mini SVG line chart from values (for the station detail page).
func sparklineSVG(vals []float64, w, h int) string {
	if len(vals) < 2 || h <= 0 || w <= 0 {
		return ""
	}
	lo, hi := vals[0], vals[0]
	for _, v := range vals {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	rng := hi - lo
	if rng < 1e-9 {
		rng = 1
	}
	pad := 4.0
	type pt struct{ x, y, v float64 }
	points := make([]pt, len(vals))
	var linePts []string
	n := len(vals)
	for i, v := range vals {
		x := float64(i) / float64(n-1) * float64(w)
		y := (float64(h) - pad) - (v-lo)/rng*(float64(h)-2*pad) + pad/2
		points[i] = pt{x, y, v}
		linePts = append(linePts, fmt.Sprintf("%.1f,%.1f", x, y))
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<div class="spark-wrap"><svg class="sparksvg" viewBox="0 0 %d %d" preserveAspectRatio="none" xmlns="http://www.w3.org/2000/svg">`, w, h)
	fmt.Fprintf(&b, `<polyline points="%s" fill="none" stroke="var(--primary)" stroke-width="1.5" stroke-linejoin="round"/>`, strings.Join(linePts, " "))
	last := points[len(points)-1]
	fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="3" fill="var(--primary)"/>`, last.x, last.y)
	b.WriteString(`</svg>`)
	fmt.Fprintf(&b, `<div class="spark-dots" style="grid-template-columns:repeat(%d,1fr)">`, len(points))
	for _, p := range points {
		fmt.Fprintf(&b, `<span class="spark-dot" data-tip="%.6fx"></span>`, p.v)
	}
	b.WriteString(`</div></div>`)
	return b.String()
}

// POST /api/stations/{id}/poll — trigger an immediate poll for a station.
func (s *Server) stationsPoll(w http.ResponseWriter, r *http.Request) {
	if s.mgr == nil {
		http.Error(w, "station manager not configured", http.StatusServiceUnavailable)
		return
	}
	id := chi.URLParam(r, "id")
	poller, ok := s.mgr.(interface{ PollNow(string) error })
	if !ok {
		http.Error(w, "poll not supported", http.StatusNotImplemented)
		return
	}
	if err := poller.PollNow(id); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"id": id, "status": "polled"})
}

// POST /api/stations/{id}/login — auto-login to sub2api and obtain a fresh JWT.
// Always returns 200 with a JSON body ({error} on failure, {message} on success)
// because the external ingress proxy replaces 5xx with its own HTML page,
// which would break the browser's r.json() in tmFetchJWT.
func (s *Server) stationsLogin(w http.ResponseWriter, r *http.Request) {
	if s.mgr == nil {
		writeJSON(w, 200, map[string]string{"error": "station manager not configured"})
		return
	}
	id := chi.URLParam(r, "id")
	st, ok := s.findStation(id)
	if !ok {
		writeJSON(w, 200, map[string]string{"error": "station not found"})
		return
	}
	if st.Kind != domain.KindSub2API {
		writeJSON(w, 200, map[string]string{"error": "JWT 登录仅支持 sub2api 站点"})
		return
	}
	lang := s.lang(w, r)
	if st.Auth.AdminEmail == "" || st.Auth.AdminPass == "" {
		writeJSON(w, 200, map[string]string{"error": t(lang, "form.jwt.nocred")})
		return
	}
	token, exp, err := jwtlogin.Login(r.Context(), st.BaseURL, st.Auth.AdminEmail, st.Auth.AdminPass, nil)
	if err != nil {
		writeJSON(w, 200, map[string]string{"error": err.Error()})
		return
	}
	st.Auth.JWT = token
	if err := s.mgr.AddStation(st); err != nil {
		writeJSON(w, 200, map[string]string{"error": err.Error()})
		return
	}
	msg := fmt.Sprintf(t(lang, "form.jwt.ok"), exp.Format("2006-01-02 15:04:05"))
	writeJSON(w, 200, map[string]string{"message": msg, "expires_at": exp.Format(time.RFC3339)})
}
