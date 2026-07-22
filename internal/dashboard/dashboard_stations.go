package dashboard

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
		enabled := "✅"
		if !st.Enabled {
			enabled = "—"
		}
		edit := `<a class="btn btn-outline btn-sm" href="/stations/` + esc(st.ID) + `/edit">` + t(lang, "form.edit") + `</a>`
		del := `<button class="btn btn-danger btn-sm" onclick="tmDel('` + esc(st.ID) + `')">` + t(lang, "form.delete") + `</button>`
		rows = append(rows, []string{
			esc(st.ID), esc(st.Name),
			`<span class="tag tag-pri">` + esc(string(st.Kind)) + `</span>`,
			esc(st.BaseURL), enabled, edit + " " + del,
		})
	}
	body := `<h1>` + t(lang, "title.stations") + `</h1>` +
		`<p><a class="btn" href="/stations/new">+ ` + t(lang, "title.newstation") + `</a></p>` +
		renderTable(lang, []string{t(lang, "form.id"), t(lang, "form.name"), t(lang, "form.kind"), t(lang, "form.baseurl"), t(lang, "form.enabled"), ""}, rows) +
		`<script>function tmDel(id){if(!confirm('` + t(lang, "form.confirm") + `'))return;fetch('/api/stations/'+id,{method:'DELETE'}).then(function(){location.reload();});}</script>`
	writeHTMLShell(w, lang, t(lang, "title.stations"), "stations", body)
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
