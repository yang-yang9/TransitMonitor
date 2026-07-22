package dashboard

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"transitmonitor/internal/domain"
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
<<<<<<< HEAD
		edit := `<a class="btn btn-outline btn-sm" href="/stations/` + esc(st.ID) + `/edit">` + t(lang, "form.edit") + `</a>`
		del := `<button class="btn btn-danger btn-sm" onclick="tmDel('` + esc(st.ID) + `')">` + t(lang, "form.delete") + `</button>`
=======
		edit := `<a class="btn btn-sm btn-ghost" href="/stations/` + esc(st.ID) + `/edit">` + t(lang, "form.edit") + `</a>`
		del := `<button class="btn btn-sm btn-danger" onclick="tmDel('` + esc(st.ID) + `')">` + t(lang, "form.delete") + `</button>`
>>>>>>> worktree-ui-optimization
		rows = append(rows, []string{
			`<span class="mono">` + esc(st.ID) + `</span>`, esc(st.Name),
			`<span class="tag tag-pri">` + esc(string(st.Kind)) + `</span>`,
			`<span class="mono" style="font-size:.8rem">` + esc(st.BaseURL) + `</span>`, enabled,
			`<div style="display:flex;gap:.4rem">` + edit + ` ` + del + `</div>`,
		})
	}
	body := `<div class="page-hdr"><h1>` + t(lang, "title.stations") + `</h1>` +
		`<p class="sub"><a class="btn" href="/stations/new">+ ` + t(lang, "title.newstation") + `</a></p></div>` +
		renderTable(lang, []string{t(lang, "form.id"), t(lang, "form.name"), t(lang, "form.kind"), t(lang, "form.baseurl"), t(lang, "form.enabled"), ""}, rows) +
		`<script>function tmDel(id){if(!confirm('` + t(lang, "form.confirm") + `'))return;fetch('/api/stations/'+id,{method:'DELETE'}).then(function(){location.reload();});}</script>`
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
	// ratios
	obs, _ := s.store.LatestRatioObservations(ctx, id)
	ratioRows := make([][]string, 0, len(obs))
	for _, o := range obs {
		ratioRows = append(ratioRows, []string{
			esc(o.GroupName), `<span class="mono">` + esc(o.ModelName) + `</span>`,
			`<span class="num mono">` + fmtUSD(o.InputUSDPer1M) + `</span>`,
			`<span class="num mono">` + fmtUSD(o.OutputUSDPer1M) + `</span>`,
			statusBadge(lang, o.Sentinel),
		})
	}
	// changes
	evs, _ := s.store.ListChangeEvents(ctx, id, 20)
	changeRows := make([][]string, 0, len(evs))
	for _, e := range evs {
		changeRows = append(changeRows, []string{
			`<span class="mono">` + fmtTime(e.ObservedAt) + `</span>`,
			`<span class="mono">` + esc(e.Model) + `</span>`,
			`<span class="tag">` + esc(e.Field) + `</span>`,
			`<span class="num">` + fmtPct(e.DeltaPct) + `</span>`,
			severityBadge(lang, e.Severity),
		})
	}
	// probes
	prs, _ := s.store.ListProbeResults(ctx, id, 20)
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
	info := `<span class="tag tag-pri">` + esc(string(st.Kind)) + `</span> ` + esc(st.BaseURL) +
		` <a class="btn btn-outline btn-sm" href="/stations/` + esc(st.ID) + `/edit">` + t(lang, "form.edit") + `</a>`
	body := `<h1>` + esc(st.Name) + `</h1><p class="sub">` + info + `</p>` +
		`<h2>` + t(lang, "section.ratios") + `</h2>` + renderTable(lang, []string{t(lang, "col.group"), t(lang, "col.model"), "input $/M", "output $/M", t(lang, "col.status")}, ratioRows) +
		`<h2>` + t(lang, "title.changes") + `</h2>` + renderTable(lang, []string{t(lang, "col.time"), t(lang, "col.model"), t(lang, "col.field"), t(lang, "col.deltapct"), t(lang, "col.severity")}, changeRows) +
		`<h2>` + t(lang, "title.probes") + `</h2>` + renderTable(lang, []string{t(lang, "col.time"), t(lang, "col.model"), t(lang, "col.declared"), t(lang, "col.measured"), t(lang, "col.markup"), t(lang, "col.status")}, probeRows)
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
	checkedAttr := "checked"
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
	}
<<<<<<< HEAD
	return `<div class="form-wrap"><h1>` + title + `</h1><p class="sub">` + t(lang, "form.id.auto") + `</p>
<div class="card">
<form id="stform" onsubmit="return tmSubmit('` + method + `','` + action + `')">
  <div class="form-grid">
    <div class="field"><span class="field-label">` + t(lang, "form.id") + `</span><input name="id" value="` + esc(idVal) + `" placeholder="` + esc(idPH) + `" ` + idRO + `></div>
    <div class="field"><span class="field-label">` + t(lang, "form.name") + `</span><input name="name" required value="` + esc(nameVal) + `"></div>
    <div class="field full"><span class="field-label">` + t(lang, "form.baseurl") + `</span><input name="base_url" required value="` + esc(baseVal) + `" placeholder="https://relay.example.com"></div>
    <div class="field"><span class="field-label">` + t(lang, "form.kind") + `</span><select name="kind"><option value="newapi" ` + kindNew + `>newapi</option><option value="sub2api" ` + kindSub + `>sub2api</option></select></div>
    <div class="field"><span class="field-label">` + t(lang, "form.group") + `</span><input name="group" value="` + esc(groupVal) + `"></div>
    <div class="field"><span class="field-label">` + t(lang, "form.pollinterval") + `</span><input name="poll_interval" value="` + esc(pollVal) + `"></div>
    <div class="field"><span class="field-label">` + t(lang, "form.apikey") + `</span><input name="api_key" placeholder="` + apiPH + `"></div>
    <div class="field"><span class="field-label">` + t(lang, "form.pat") + `</span><input name="pat" placeholder="` + patPH + `"></div>
    <div class="field"><span class="field-label">` + t(lang, "form.jwt") + `</span><input name="jwt" placeholder="` + jwtPH + `"></div>
    <div class="field"><span class="field-label">` + t(lang, "form.enabled") + `</span><label class="toggle"><input type="checkbox" name="enabled" ` + checkedAttr + `><span class="slider"></span>` + t(lang, "form.enabled") + `</label></div>
  </div>
  <div class="btn-group" style="margin-top:1.2rem"><button class="btn" type="submit">` + submit + `</button><a class="btn btn-outline" href="/stations">←</a></div>
=======
	return `<div class="page-hdr"><h1>` + title + `</h1></div>
<div class="card">
<form id="stform" onsubmit="return tmSubmit('` + method + `','` + action + `')">
  <div class="form-grid">
    <label>` + t(lang, "form.id") + `<input name="id" ` + idRequired + ` value="` + esc(idVal) + `" placeholder="` + esc(idPH) + `" ` + idRO + `></label>
    <label>` + t(lang, "form.name") + `<input name="name" required value="` + esc(nameVal) + `"></label>
    <label class="full">` + t(lang, "form.baseurl") + `<input name="base_url" required value="` + esc(baseVal) + `" placeholder="https://relay.example.com"></label>
    <label>` + t(lang, "form.kind") + `<select name="kind"><option value="newapi" ` + kindNew + `>newapi</option><option value="sub2api" ` + kindSub + `>sub2api</option></select></label>
    <label>` + t(lang, "form.group") + `<input name="group" value="` + esc(groupVal) + `"></label>
    <label>` + t(lang, "form.pollinterval") + `<input name="poll_interval" value="` + esc(pollVal) + `"></label>
    <div></div>
    <hr class="form-sep">
    <label>` + t(lang, "form.apikey") + `<input name="api_key" value="` + esc(apiVal) + `" placeholder="` + apiPH + `"></label>
    <label>` + t(lang, "form.pat") + `<input name="pat" value="` + esc(patVal) + `" placeholder="` + patPH + `"></label>
    <label>` + t(lang, "form.jwt") + `<input name="jwt" value="` + esc(jwtVal) + `" placeholder="` + jwtPH + `"></label>
    <div class="chk-wrap"><input type="checkbox" name="enabled" id="st-enabled" ` + checked + `><label for="st-enabled">` + t(lang, "form.enabled") + `</label></div>
  </div>
  <div class="form-actions"><button class="btn" type="submit">` + submit + `</button> <a class="btn btn-ghost" href="/stations">&larr; ` + t(lang, "title.stations") + `</a></div>
>>>>>>> worktree-ui-optimization
</form>
</div>
<script>
function tmSubmit(m,u){
  var f=document.getElementById('stform'), v=function(n){var el=f[n]; if(!el) return ''; if(el.type=='checkbox') return el.checked; return el.value;};
  var st={id:v('id'),name:v('name'),base_url:v('base_url'),kind:v('kind'),auth:{api_key:v('api_key'),pat:v('pat'),jwt:v('jwt'),group:v('group')},poll_interval:v('poll_interval'),enabled:!!v('enabled')};
  fetch(u,{method:m,headers:{'Content-Type':'application/json'},body:JSON.stringify(st)}).then(function(r){if(r.ok){location.href='/stations';}else{r.text().then(function(t){alert(t);});}}).catch(function(e){alert(e);});
  return false;
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
		APIKey, PAT, JWT, Group string
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
		Auth:         domain.AuthConfig{APIKey: in.Auth.APIKey, PAT: in.Auth.PAT, JWT: in.Auth.JWT, Group: in.Auth.Group},
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
	if in.BaseURL == "" || in.Kind == "" {
		http.Error(w, "base_url, kind required", http.StatusBadRequest)
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
		if in.Auth.JWT == "" {
			in.Auth.JWT = existing.Auth.JWT
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
