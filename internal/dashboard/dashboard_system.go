package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"transitmonitor/internal/updater"
)

// systemUpdateContext detaches the operation from the request lifecycle so a
// browser/reverse-proxy disconnect mid-download does NOT abort the binary swap
// (sub2api fixed exactly this with context.WithoutCancel). Capped at 15min.
func systemUpdateContext(r *http.Request) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Minute)
	return ctx, cancel
}

// GET /api/system/version — current build + run mode (no GitHub call).
func (s *Server) systemVersionJSON(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"current":       s.version,
		"mode":          "",
		"wrapper_ready": true,
	}
	if s.updater != nil {
		out["mode"] = s.updater.Mode()
		out["wrapper_ready"] = s.updater.WrapperReady()
	}
	writeJSON(w, 200, out)
}

// GET /api/system/check-updates?force=true — compares the latest GitHub release
// tag to the running version. Soft-fails (200 + error field) when GitHub is
// unreachable so the page degrades gracefully.
func (s *Server) systemCheckUpdatesJSON(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": t(s.lang(w, r), "system.no_updater")})
		return
	}
	force := r.URL.Query().Get("force") == "1" || r.URL.Query().Get("force") == "true"
	info, err := s.updater.CheckUpdates(r.Context(), force)
	if err != nil {
		writeJSON(w, 200, updater.UpdateInfo{CurrentVersion: s.version, Error: err.Error(), CheckedAt: time.Now().Unix()})
		return
	}
	writeJSON(w, 200, info)
}

// GET /api/system/rollback-versions — locally-archived prior binaries.
func (s *Server) systemRollbackVersionsJSON(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": t(s.lang(w, r), "system.no_updater")})
		return
	}
	vers, err := s.updater.ListRollbackVersions(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, vers)
}

// POST /api/system/upgrade — download + verify + atomic swap the latest release.
func (s *Server) systemUpgradeJSON(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	if s.updater == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": t(lang, "system.no_updater")})
		return
	}
	if !s.updater.WrapperReady() {
		writeJSON(w, http.StatusPreconditionFailed, map[string]string{"error": t(lang, "system.wrapper_needed")})
		return
	}
	ctx, cancel := systemUpdateContext(r)
	defer cancel()
	out, err := s.updater.PerformUpdate(ctx)
	if err != nil {
		writeJSON(w, 200, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, out)
}

// POST /api/system/rollback — body optional {"version":"x.y.z"}. Empty body
// restores the most recent local backup; a version restores that one.
func (s *Server) systemRollbackJSON(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	if s.updater == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": t(lang, "system.no_updater")})
		return
	}
	var in struct {
		Version string `json:"version"`
	}
	// Body is optional; ignore decode errors (empty body → Rollback()).
	_ = json.NewDecoder(r.Body).Decode(&in)
	ctx, cancel := systemUpdateContext(r)
	defer cancel()
	var (
		out updater.UpdateOutcome
		err error
	)
	if in.Version == "" {
		out, err = s.updater.Rollback(ctx)
	} else {
		out, err = s.updater.RollbackToVersion(ctx, in.Version)
	}
	if err != nil {
		writeJSON(w, 200, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, out)
}

// POST /api/system/restart — swap in the new binary (bare: syscall.Exec;
// docker: os.Exit(0) + supervisor). Returns immediately; the swap fires after
// a 500ms delay so this response reaches the caller.
func (s *Server) systemRestartJSON(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	if s.updater == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": t(lang, "system.no_updater")})
		return
	}
	if err := s.updater.Restart(r.Context()); err != nil {
		writeJSON(w, 200, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"message": t(lang, "system.restarting")})
}

// systemHTML renders the /system page: current vs latest version, rollback
// dropdown, and the 立即更新 / 回退 / 重启 buttons. Mirrors the /settings page
// (server-rendered HTML + inline fetch() JS).
func (s *Server) systemHTML(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	mode := ""
	wrapperReady := true
	if s.updater != nil {
		mode = s.updater.Mode()
		wrapperReady = s.updater.WrapperReady()
	}

	var b pageBuilder
	b.w(`<div class="page-hdr"><h1>` + t(lang, "title.system") + `</h1><p class="sub">` + t(lang, "sub.system") + `</p></div>`)

	if s.updater == nil {
		b.w(`<div class="card"><div class="banner b-warn">` + t(lang, "system.no_updater") + `</div></div>`)
		writeHTMLShell(w, lang, t(lang, "title.system"), "system", b.String())
		return
	}

	// Version summary card.
	b.w(`<div class="card"><div class="kvs"><div><b>` + t(lang, "system.current_version") +
		`</b> <span class="tag tag-pri" id="tm-sys-cur">` + esc(s.version) + `</span></div>` +
		`<div><b>` + t(lang, "system.latest_version") + `</b> <span id="tm-sys-latest">—</span>` +
		` <button class="btn btn-sm btn-outline" onclick="tmSysCheck(0)">` + t(lang, "system.check") + `</button></div>` +
		`<div><b>` + t(lang, "system.runmode") + `</b> <span class="tag">` + esc(mode) + `</span></div></div>` +
		`<div id="tm-sys-msg" class="meta" style="margin-top:.6rem"></div></div>`)

	// Wrapper-missing warning (docker image without the entrypoint script).
	if !wrapperReady {
		b.w(`<div class="card"><div class="banner b-warn">` + t(lang, "system.wrapper_needed") + `</div></div>`)
	}

	// Action buttons.
	disabled := ""
	if !wrapperReady {
		disabled = ` disabled`
	}
	b.w(`<div class="card btn-group">` +
		`<button class="btn"` + disabled + ` onclick="tmSysUpgrade()">` + t(lang, "system.upgrade_now") + `</button>` +
		`<button class="btn btn-outline"` + disabled + ` onclick="tmSysRollback()">` + t(lang, "system.rollback") + `</button>` +
		`<select id="tm-sys-rollback-ver" class="field" style="max-width:14rem"` + disabled + `><option value="">` + t(lang, "system.rollback_choose") + `</option></select>` +
		`<button class="btn btn-outline" onclick="tmSysRestart()">` + t(lang, "system.restart") + `</button>` +
		`</div>`)

	b.w(`<script>
function tmSysMsg(m){var e=document.getElementById('tm-sys-msg');if(e)e.textContent=m;}
function tmSysCheck(force){
  tmSysMsg('...');
  fetch('/api/system/check-updates'+(force?'?force=1':'')).then(function(r){return r.json();})
    .then(function(d){
      var cur=document.getElementById('tm-sys-cur');
      if(d.current&&cur)cur.textContent=d.current;
      var lat=document.getElementById('tm-sys-latest');
      if(d.error){ if(lat)lat.textContent='⚠'; tmSysMsg(d.error); return; }
      if(lat)lat.textContent=d.latest_version||'—';
      if(d.has_update){ tmSysMsg('` + jsQuote(t(lang, "system.new_available")) + `'); }
      else { tmSysMsg('` + jsQuote(t(lang, "system.up_to_date")) + `'); }
    }).catch(function(e){tmSysMsg(e);});
}
function tmSysUpgrade(){
  if(!confirm('` + jsQuote(t(lang, "system.upgrade_confirm")) + `'))return;
  tmSysMsg('` + jsQuote(t(lang, "system.upgrading")) + `');
  fetch('/api/system/upgrade',{method:'POST'}).then(function(r){return r.json();})
    .then(function(d){
      if(d.error){ tmSysMsg(d.error); alert(d.error); return; }
      tmSysMsg(d.message||'ok');
      if(d.need_restart){ if(confirm('` + jsQuote(t(lang, "system.restart_required")) + `'))tmSysRestart(); }
    }).catch(function(e){tmSysMsg(e);});
}
function tmSysRollback(){
  var ver=(document.getElementById('tm-sys-rollback-ver')||{}).value||'';
  if(!ver){ alert('` + jsQuote(t(lang, "system.rollback_choose")) + `'); return; }
  if(!confirm(ver+' ?'))return;
  tmSysMsg('...');
  fetch('/api/system/rollback',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({version:ver})})
    .then(function(r){return r.json();})
    .then(function(d){
      if(d.error){ tmSysMsg(d.error); alert(d.error); return; }
      tmSysMsg(d.message||'ok');
      if(d.need_restart){ if(confirm('` + jsQuote(t(lang, "system.restart_required")) + `'))tmSysRestart(); }
    }).catch(function(e){tmSysMsg(e);});
}
function tmSysRestart(){
  fetch('/api/system/restart',{method:'POST'}).then(function(r){return r.json();})
    .then(function(d){ tmSysMsg(d.message||d.error||'...'); setTimeout(function(){location.reload();},4000); })
    .catch(function(){ tmSysMsg('` + jsQuote(t(lang, "system.restarting")) + `'); setTimeout(function(){location.reload();},4000); });
}
// On load: check updates + populate rollback dropdown.
tmSysCheck(0);
fetch('/api/system/rollback-versions').then(function(r){return r.json();}).then(function(vs){
  var sel=document.getElementById('tm-sys-rollback-ver'); if(!sel)return;
  (vs||[]).forEach(function(v){var o=document.createElement('option');o.value=v.version;o.textContent=v.version+' ('+new Date(v.published_at*1000).toLocaleString()+')';sel.appendChild(o);});
});
</script>`)

	writeHTMLShell(w, lang, t(lang, "title.system"), "system", b.String())
}
