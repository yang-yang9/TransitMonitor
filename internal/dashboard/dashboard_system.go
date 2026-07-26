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

// systemHTML renders the /system page: a version hero (current → latest with a
// status pill), a prominent "立即更新" CTA, a rollback-versions list (clickable
// cards, not a bare <select>), and a restart danger zone. Mirrors the /settings
// page (server-rendered HTML + inline fetch() JS) but carries its own scoped CSS
// for the version tiles + status pill.
func (s *Server) systemHTML(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	mode := ""
	wrapperReady := true
	if s.updater != nil {
		mode = s.updater.Mode()
		wrapperReady = s.updater.WrapperReady()
	}

	var b pageBuilder
	b.w(`<style>
.sys-tiles{display:grid;grid-template-columns:1fr auto 1fr;gap:1rem;align-items:stretch;margin:.4rem 0 1rem}
.sys-tile{background:var(--bg-2);border:1px solid var(--border);border-radius:var(--radius);padding:1rem 1.2rem;display:flex;flex-direction:column;gap:.25rem;min-width:0}
.sys-tile.cur{border-left:3px solid var(--primary);background:var(--primary-50)}
.sys-tile.lat{border-left:3px solid var(--primary-300)}
.sys-tile .st-lbl{font-size:.72rem;text-transform:uppercase;letter-spacing:.06em;color:var(--muted);font-weight:700}
.sys-tile .st-val{font-size:1.6rem;font-weight:800;font-variant-numeric:tabular-nums;font-family:var(--mono);letter-spacing:-.01em;color:var(--ink);word-break:break-all;line-height:1.15}
.sys-tile .st-sub{font-size:.78rem;color:var(--muted)}
.sys-arrow{display:flex;align-items:center;justify-content:center;color:var(--primary);font-size:1.4rem;font-weight:800}
.sys-status{display:inline-flex;align-items:center;gap:.4rem;padding:.35rem .8rem;border-radius:999px;font-size:.8rem;font-weight:700;border:1px solid var(--border);background:var(--bg-2)}
.sys-status.idle{color:var(--muted)}
.sys-status.ok{color:var(--ok);background:var(--ok-soft);border-color:transparent}
.sys-status.new{color:var(--primary-700);background:var(--primary-100);border-color:transparent}
.sys-status.err{color:var(--crit);background:var(--crit-soft);border-color:transparent}
.sys-status .pulse{width:8px;height:8px;border-radius:50%;background:currentColor;animation:sys-pulse 1.4s ease-in-out infinite}
@keyframes sys-pulse{0%,100%{opacity:.4;transform:scale(.8)}50%{opacity:1;transform:scale(1.2)}}
.sys-cta{display:flex;gap:.6rem;flex-wrap:wrap;align-items:center}
.sys-cta .btn{flex:1;min-width:200px;padding:.7rem 1.2rem;font-size:.95rem}
.sys-msg{margin-top:.8rem;padding:.55rem .8rem;border-radius:var(--radius-sm);background:var(--bg-2);border:1px solid var(--border);font-size:.85rem;color:var(--ink2);min-height:1.2em}
.sys-rollback-list{display:grid;grid-template-columns:repeat(auto-fill,minmax(240px,1fr));gap:.7rem}
.sys-rb-item{display:flex;align-items:center;justify-content:space-between;gap:.6rem;padding:.7rem .9rem;border:1px solid var(--border);border-left:3px solid var(--primary-300);border-radius:var(--radius-sm);background:var(--card);box-shadow:var(--shadow);transition:all .15s}
.sys-rb-item:hover{border-left-color:var(--primary);transform:translateY(-1px);box-shadow:var(--shadow-lg)}
.sys-rb-item .v{font-weight:700;font-family:var(--mono);font-size:.95rem;color:var(--ink)}
.sys-rb-item .t{font-size:.72rem;color:var(--muted)}
.sys-empty{color:var(--muted);font-size:.85rem;padding:1.2rem;text-align:center;border:1px dashed var(--border);border-radius:var(--radius-sm)}
.sys-mode-row{display:flex;align-items:center;gap:.5rem;flex-wrap:wrap;font-size:.8rem;color:var(--muted);margin-top:.4rem}
.sys-mode-row .tag{font-family:var(--mono)}
.sys-banner{padding:.7rem 1rem;border-radius:var(--radius-sm);font-size:.85rem;line-height:1.6}
.sys-banner.b-warn{background:var(--warn-soft);color:var(--warn);border:1px solid transparent}
.sys-banner.b-crit{background:var(--crit-soft);color:var(--crit);border:1px solid transparent}
.sys-zone{margin-top:.4rem}
.sys-zone h2{display:flex;align-items:center;gap:.5rem}
</style>`)

	b.w(`<div class="page-hdr"><h1>` + t(lang, "title.system") + `</h1><p class="sub">` + t(lang, "sub.system") + `</p></div>`)

	if s.updater == nil {
		b.w(`<div class="card"><div class="sys-banner b-warn">` + t(lang, "system.no_updater") + `</div></div>`)
		s.writeHTMLShell(w, lang, t(lang, "title.system"), "system", b.String())
		return
	}

	disabled := ""
	if !wrapperReady {
		disabled = ` disabled`
	}

	// Version hero card.
	b.w(`<div class="card">`)
	b.w(`<div class="sys-tiles">`)
	b.w(`<div class="sys-tile cur"><span class="st-lbl">` + t(lang, "system.current_version") +
		`</span><span class="st-val" id="tm-sys-cur">` + esc(s.version) + `</span>` +
		`<span class="sys-mode-row"><span class="tag">` + esc(mode) + `</span>` + t(lang, "system.runmode") + `</span></div>`)
	b.w(`<div class="sys-arrow">→</div>`)
	b.w(`<div class="sys-tile lat"><span class="st-lbl">` + t(lang, "system.latest_version") +
		`</span><span class="st-val" id="tm-sys-latest">—</span>` +
		`<span class="st-sub"><button class="btn btn-sm btn-outline" onclick="tmSysCheck(1)">↻ ` + t(lang, "system.check") + `</button></span></div>`)
	b.w(`</div>`)
	b.w(`<div><span class="sys-status idle" id="tm-sys-status"><span class="pulse"></span> ` + t(lang, "system.checking") + `</span></div>`)
	b.w(`<div class="sys-msg" id="tm-sys-msg"></div>`)
	b.w(`</div>`)

	// Upgrade CTA.
	b.w(`<div class="card sys-zone">`)
	b.w(`<h2>🔄 ` + t(lang, "system.upgrade_now") + `</h2>`)
	if !wrapperReady {
		b.w(`<div class="sys-banner b-warn" style="margin-bottom:.7rem">` + t(lang, "system.wrapper_needed") + `</div>`)
	}
	b.w(`<div class="sys-cta"><button class="btn"` + disabled + ` onclick="tmSysUpgrade()">` +
		t(lang, "system.upgrade_now") + `</button>` +
		`<button class="btn btn-outline" onclick="tmSysRestart()">↻ ` + t(lang, "system.restart") + `</button></div>`)
	b.w(`</div>`)

	// Rollback list.
	b.w(`<div class="card sys-zone">`)
	b.w(`<h2>⤺ ` + t(lang, "system.rollback") + `</h2>`)
	b.w(`<div class="sys-rollback-list" id="tm-sys-rollback-list"><div class="sys-empty">` + t(lang, "system.rollback_empty") + `</div></div>`)
	b.w(`</div>`)

	b.w(`<script>
function tmSysMsg(m,cls){var e=document.getElementById('tm-sys-msg');if(e){e.textContent=m||'';e.className='sys-msg'+(cls?' '+cls:'');}}
function tmSysStatus(state,txt){var e=document.getElementById('tm-sys-status');if(!e)return;var p={idle:'idle',ok:'ok',new:'new',err:'err'}[state]||'idle';e.className='sys-status '+p;e.innerHTML='<span class="pulse"></span> '+txt;}
function tmSysCheck(force){
  tmSysMsg('','');tmSysStatus('idle',` + jsQuote(t(lang, "system.checking")) + `);
  fetch('/api/system/check-updates'+(force?'?force=1':'')).then(function(r){return r.json();})
    .then(function(d){
      var cur=document.getElementById('tm-sys-cur');
      if(d.current&&cur)cur.textContent=d.current;
      var lat=document.getElementById('tm-sys-latest');
      if(d.error){ if(lat)lat.textContent='⚠'; tmSysStatus('err',` + jsQuote(t(lang, "system.error")) + `); tmSysMsg(d.error); return; }
      if(lat)lat.textContent=d.latest_version||'—';
      if(d.has_update){ tmSysStatus('new',` + jsQuote(t(lang, "system.new_available")) + `); tmSysMsg(` + jsQuote(t(lang, "system.new_available")) + `); }
      else { tmSysStatus('ok',` + jsQuote(t(lang, "system.up_to_date")) + `); tmSysMsg(` + jsQuote(t(lang, "system.up_to_date")) + `); }
    }).catch(function(e){tmSysStatus('err',` + jsQuote(t(lang, "system.error")) + `);tmSysMsg(String(e));});
}
function tmSysUpgrade(){
  if(!confirm(` + jsQuote(t(lang, "system.upgrade_confirm")) + `))return;
  tmSysMsg(` + jsQuote(t(lang, "system.upgrading")) + `);
  fetch('/api/system/upgrade',{method:'POST'}).then(function(r){return r.json();})
    .then(function(d){
      if(d.error){ tmSysMsg(d.error,'err'); alert(d.error); return; }
      tmSysMsg(d.message||'ok','ok');
      if(d.need_restart){ if(confirm(` + jsQuote(t(lang, "system.restart_required")) + `))tmSysRestart(); }
    }).catch(function(e){tmSysMsg(String(e),'err');});
}
function tmSysRollback(ver){
  if(!ver)return;
  if(!confirm(ver+' ?'))return;
  tmSysMsg('...');
  fetch('/api/system/rollback',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({version:ver})})
    .then(function(r){return r.json();})
    .then(function(d){
      if(d.error){ tmSysMsg(d.error,'err'); alert(d.error); return; }
      tmSysMsg(d.message||'ok','ok');
      if(d.need_restart){ if(confirm(` + jsQuote(t(lang, "system.restart_required")) + `))tmSysRestart(); }
    }).catch(function(e){tmSysMsg(String(e),'err');});
}
function tmSysRestart(){
  tmSysMsg(` + jsQuote(t(lang, "system.restarting")) + `);
  fetch('/api/system/restart',{method:'POST'}).then(function(r){return r.json();})
    .then(function(d){ tmSysMsg(d.message||d.error||'...','ok'); setTimeout(function(){location.reload();},4000); })
    .catch(function(){ tmSysMsg(` + jsQuote(t(lang, "system.restarting")) + `,'ok'); setTimeout(function(){location.reload();},4000); });
}
function tmSysFmt(ts){try{return new Date(ts*1000).toLocaleString();}catch(e){return ''; } }
// On load: check updates + populate rollback list.
tmSysCheck(0);
fetch('/api/system/rollback-versions').then(function(r){return r.json();}).then(function(vs){
  var box=document.getElementById('tm-sys-rollback-list'); if(!box)return;
  var list=(vs||[]); if(!list.length){ box.innerHTML='<div class="sys-empty">'+` + jsQuote(t(lang, "system.rollback_empty")) + `+'</div>'; return; }
  box.innerHTML='';
  list.forEach(function(v){
    var d=document.createElement('div');d.className='sys-rb-item';
    var left=document.createElement('div');
    var vv=document.createElement('div');vv.className='v';vv.textContent='v'+v.version;left.appendChild(vv);
    var tt=document.createElement('div');tt.className='t';tt.textContent=tmSysFmt(v.published_at);left.appendChild(tt);
    d.appendChild(left);
    var btn=document.createElement('button');btn.className='btn btn-sm btn-outline';btn.textContent=` + jsQuote(t(lang, "system.rollback")) + `;
    btn.onclick=function(){tmSysRollback(v.version);};
    d.appendChild(btn);
    box.appendChild(d);
  });
}).catch(function(){});
</script>`)

	s.writeHTMLShell(w, lang, t(lang, "title.system"), "system", b.String())
}
