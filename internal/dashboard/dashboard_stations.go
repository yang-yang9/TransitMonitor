package dashboard

import (
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

// GET /stations — management page: list + add link + delete buttons.
func (s *Server) stationsPage(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	sts := s.stationsList()
	rows := make([][]string, 0, len(sts))
	for _, st := range sts {
		enabled := "✅"
		if !st.Enabled {
			enabled = "—"
		}
		del := `<button class="btn" onclick="tmDel('` + esc(st.ID) + `')">` + t(lang, "form.delete") + `</button>`
		rows = append(rows, []string{
			esc(st.ID), esc(st.Name),
			`<span class="tag tag-pri">` + esc(string(st.Kind)) + `</span>`,
			esc(st.BaseURL), enabled, del,
		})
	}
	body := `<h1>` + t(lang, "title.stations") + `</h1>` +
		`<p><a class="btn" href="/stations/new">+ ` + t(lang, "title.newstation") + `</a></p>` +
		renderTable(lang, []string{t(lang, "form.id"), t(lang, "form.name"), t(lang, "form.kind"), t(lang, "form.baseurl"), t(lang, "form.enabled"), ""}, rows) +
		`<script>function tmDel(id){if(!confirm('` + t(lang, "form.confirm") + `'))return;fetch('/api/stations/'+id,{method:'DELETE'}).then(function(){location.reload();});}</script>`
	writeHTMLShell(w, lang, t(lang, "title.stations"), "stations", body)
}

// GET /stations/new — add-station form (posts JSON to /api/stations).
func (s *Server) stationFormHTML(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	body := `<h1>` + t(lang, "title.newstation") + `</h1>` + stationForm(lang)
	writeHTMLShell(w, lang, t(lang, "title.newstation"), "stations", body)
}

func stationForm(lang string) string {
	return `
<div class="card">
<form id="stform" onsubmit="return tmSubmit(event)">
  <div class="grid">
    <label>` + t(lang, "form.id") + `<input name="id" required></label>
    <label>` + t(lang, "form.name") + `<input name="name" required></label>
    <label>` + t(lang, "form.baseurl") + `<input name="base_url" required placeholder="https://relay.example.com"></label>
    <label>` + t(lang, "form.kind") + `<select name="kind"><option value="newapi">newapi</option><option value="sub2api">sub2api</option></select></label>
    <label>` + t(lang, "form.group") + `<input name="group" value="default"></label>
    <label>` + t(lang, "form.pollinterval") + `<input name="poll_interval" value="3m"></label>
    <label>` + t(lang, "form.apikey") + `<input name="api_key" placeholder="sk-..."></label>
    <label>` + t(lang, "form.pat") + `<input name="pat" placeholder="new-api PAT"></label>
    <label>` + t(lang, "form.jwt") + `<input name="jwt" placeholder="sub2api user JWT"></label>
    <label>` + t(lang, "form.enabled") + `<input type="checkbox" name="enabled" checked></label>
  </div>
  <p><button class="btn" type="submit">` + t(lang, "form.add") + `</button> <a class="btn" href="/stations">←</a></p>
</form>
<script>
function tmSubmit(e){
  e.preventDefault();
  var f=e.target, v=function(n){var el=f[n]; if(!el) return ''; if(el.type=='checkbox') return el.checked; return el.value;};
  var st={id:v('id'),name:v('name'),base_url:v('base_url'),kind:v('kind'),auth:{api_key:v('api_key'),pat:v('pat'),jwt:v('jwt'),group:v('group')},poll_interval:v('poll_interval'),enabled:!!v('enabled')};
  fetch('/api/stations',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(st)}).then(function(r){if(r.ok){location.href='/stations';}else{r.text().then(function(t){alert(t);});}}).catch(function(e){alert(e);});
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
	lang := s.lang(w, r)
	if s.mgr == nil {
		http.Error(w, "station manager not configured", http.StatusServiceUnavailable)
		return
	}
	var in stationInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if in.ID == "" || in.BaseURL == "" || in.Kind == "" {
		http.Error(w, "id, base_url, kind required", http.StatusBadRequest)
		return
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
	_ = lang
	writeJSON(w, 201, map[string]string{"id": st.ID, "status": "added"})
}

// PUT /api/stations/{id} — upsert (edit).
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
