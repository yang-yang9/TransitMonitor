package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"transitmonitor/internal/alert"
)

// settingsHTML renders the /settings page with two sub-tabs:
//   - notifier: notification channel config (DingTalk / Lark / Slack / Webhook / QQ)
//   - rules:    alert rule config (delta_pct / model_added / quota_below / …)
//
// Tab is selected via ?tab= query param (default: notifier).
func (s *Server) settingsHTML(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	tab := r.URL.Query().Get("tab")
	if tab != "rules" {
		tab = "notifier"
	}

	var b pageBuilder
	b.w(`<div class="page-hdr"><h1>` + t(lang, "title.settings") + `</h1><p class="sub">` + t(lang, "sub.settings") + `</p></div>`)

	// Sub-tab bar
	activeN, activeR := "", ""
	if tab == "notifier" {
		activeN = " active"
	} else {
		activeR = " active"
	}
	b.w(`<div class="tab-bar">`)
	b.w(`<a class="tab-btn` + activeN + `" href="/settings?tab=notifier">` + t(lang, "tab.notifier") + `</a>`)
	b.w(`<a class="tab-btn` + activeR + `" href="/settings?tab=rules">` + t(lang, "tab.rules") + `</a>`)
	b.w(`</div>`)

	if tab == "notifier" {
		s.settingsNotifierTab(&b, lang)
	} else {
		s.settingsRulesTab(&b, r.Context(), lang)
	}

	s.writeHTMLShell(w, lang, t(lang, "title.settings"), "settings", b.String())
}

// settingsNotifierTab renders the notification channel form (original /settings content).
func (s *Server) settingsNotifierTab(b *pageBuilder, lang string) {
	encEnabled := len(s.encKey) > 0

	var nc alert.NotifierConfig
	if m, ok := s.mgr.(interface {
		NotifierConfigs(context.Context) alert.NotifierConfig
	}); ok {
		nc = m.NotifierConfigs(context.Background())
	}
	dingSecretPH := t(lang, "form.keepblank.hint")
	larkSecretPH := t(lang, "form.keepblank.hint")
	qqSecretPH := t(lang, "form.keepblank.hint")

	ro := ""
	if !encEnabled {
		ro = " readonly disabled"
	}

	field := func(label, name, val, ph, ty string, full bool) string {
		cls := "field"
		if full {
			cls += " full"
		}
		tattr := ""
		if ty != "" {
			tattr = ` type="` + ty + `"`
		}
		return `<div class="` + cls + `"><span class="field-label">` + esc(label) +
			`</span><input name="` + name + `" value="` + esc(val) + `" placeholder="` + esc(ph) + `"` + tattr + ro + `></div>`
	}

	b.w(`<p class="sub meta">` + t(lang, "sub.notifier") + `</p>`)

	if !encEnabled {
		b.w(`<div class="card"><div class="banner b-warn">` + t(lang, "settings.enc.disabled") + `</div></div>`)
	}

	b.w(`<div class="card"><form id="settings-form" class="form-grid" onsubmit="return tmSettingsSave();">`)

	b.w(`<div class="field full"><h3>` + t(lang, "section.dingtalk") + `</h3></div>`)
	b.w(field(t(lang, "form.webhook.url"), "dingtalk_webhook", nc.DingTalk.Webhook, "https://oapi.dingtalk.com/robot/send?access_token=...", "", true))
	b.w(field(t(lang, "form.secret"), "dingtalk_secret", "", dingSecretPH, "password", false))

	b.w(`<div class="field full"><h3>` + t(lang, "section.lark") + `</h3></div>`)
	b.w(field(t(lang, "form.webhook.url"), "lark_webhook", nc.Lark.Webhook, "https://open.feishu.cn/open-apis/bot/v2/hook/...", "", true))
	b.w(field(t(lang, "form.secret"), "lark_secret", "", larkSecretPH, "password", false))

	b.w(`<div class="field full"><h3>` + t(lang, "section.slack") + `</h3></div>`)
	b.w(field(t(lang, "form.webhook.url"), "slack_webhook", nc.Slack.Webhook, "https://hooks.slack.com/services/...", "", true))

	b.w(`<div class="field full"><h3>` + t(lang, "section.webhook") + `</h3></div>`)
	b.w(field(t(lang, "form.webhook.url"), "webhook_url", nc.Webhook.URL, "https://example.com/hook", "", true))

	b.w(`<div class="field full"><h3>` + t(lang, "section.qq") + `</h3></div>`)
	b.w(field(t(lang, "form.qq.appid"), "qq_app_id", nc.QQ.AppID, "1024xxx", "", false))
	b.w(field(t(lang, "form.qq.appsecret"), "qq_app_secret", "", qqSecretPH, "password", false))
	b.w(field(t(lang, "form.qq.groupopenid"), "qq_group_openid", nc.QQ.GroupOpenID, "ogrr...", "", false))
	b.w(`<div class="field full"><p class="meta">` + t(lang, "form.qq.note") + `</p></div>`)

	b.w(`<div class="field full"><button type="submit" class="btn"` + ro + `>` + t(lang, "btn.save") +
		`</button> <button type="button" class="btn btn-sm" onclick="tmSettingsTest('all')"` + ro + `>` + t(lang, "btn.test") + `</button></div>`)
	b.w(`</form></div>`)

	b.w(`<script>
function tmSettingsSave(){
  var v=function(n){var e=document.querySelector('[name='+n+']');return e?e.value:'';};
  var st={dingtalk:{webhook:v('dingtalk_webhook'),secret:v('dingtalk_secret')},
          lark:{webhook:v('lark_webhook'),secret:v('lark_secret')},
          slack:{webhook:v('slack_webhook')},
          webhook:{url:v('webhook_url')},
          qq:{app_id:v('qq_app_id'),app_secret:v('qq_app_secret'),group_openid:v('qq_group_openid')}};
  fetch('/api/settings',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(st)})
    .then(function(r){ if(r.ok){ alert(` + jsQuote(t(lang, "settings.saved")) + `); location.reload(); } else { r.text().then(function(t){ alert(t); }); } })
    .catch(function(e){ alert(e); });
  return false;
}
function tmSettingsTest(kind){
  fetch('/api/settings/test',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({kind:kind})})
    .then(function(r){ return r.json(); })
    .then(function(d){ alert(d.error ? d.error : d.message); })
    .catch(function(e){ alert(e); });
}
</script>`)
}

// settingsRulesTab renders the alert rules editor.
func (s *Server) settingsRulesTab(b *pageBuilder, ctx context.Context, lang string) {
	var rules []alert.Rule
	if m, ok := s.mgr.(interface {
		AlertRules(context.Context) []alert.Rule
	}); ok {
		rules = m.AlertRules(ctx)
	}

	b.w(`<p class="sub meta">` + t(lang, "sub.rules") + `</p>`)
	b.w(`<div id="rules-editor">`)

	typeOptions := []struct{ val, label string }{
		{"delta_pct", "价格变动百分比"},
		{"delta_abs", "价格变动绝对值"},
		{"model_added", "模型新增"},
		{"model_removed", "模型下架"},
		{"probe_markup_pct", "探测加价百分比"},
		{"endpoint_auth_failed", "鉴权失败"},
		{"poll_failure_streak", "连续轮询失败"},
		{"group_ratio_delta_pct", "分组倍率变动百分比"},
		{"quota_below", "余额低于阈值"},
		{"quota_drop_pct", "余额下降百分比"},
	}

	dirOptions := []struct{ val, label string }{
		{"both", t(lang, "form.rule.dir.both")},
		{"up", t(lang, "form.rule.dir.up")},
		{"down", t(lang, "form.rule.dir.down")},
	}

	delLabel := t(lang, "btn.delete_rule")

	renderRow := func(r alert.Rule) {
		dir := r.Direction
		if dir == "" {
			dir = "both"
		}
		chk := ""
		if r.Enabled {
			chk = " checked"
		}
		b.w(`<div class="rule-card card">`)
		b.w(`<div class="rule-fields">`)
		// name
		b.w(`<div class="field rule-f-name"><span class="field-label">` + t(lang, "form.rule.name") + `</span>`)
		b.w(`<input class="r-name" value="` + esc(r.Name) + `"></div>`)
		// type
		b.w(`<div class="field rule-f-type"><span class="field-label">` + t(lang, "form.rule.type") + `</span>`)
		b.w(`<select class="r-type" onchange="tmOnTypeChange(this)">`)
		for _, o := range typeOptions {
			sel := ""
			if o.val == r.Type {
				sel = " selected"
			}
			b.w(`<option value="` + o.val + `"` + sel + `>` + o.label + `</option>`)
		}
		b.w(`</select></div>`)
		// threshold
		b.w(`<div class="field rule-f-thr"><span class="field-label">` + t(lang, "form.rule.threshold") + `</span>`)
		b.w(`<input class="r-threshold" type="number" step="any" min="0" value="` + fmt.Sprintf("%g", r.Threshold) + `"></div>`)
		// direction
		b.w(`<div class="field rule-f-dir"><span class="field-label">` + t(lang, "form.rule.direction") + `</span>`)
		b.w(`<select class="r-direction">`)
		for _, o := range dirOptions {
			sel := ""
			if o.val == dir {
				sel = " selected"
			}
			b.w(`<option value="` + o.val + `"` + sel + `>` + esc(o.label) + `</option>`)
		}
		b.w(`</select><span class="dir-na">— 不适用</span></div>`)
		// enabled toggle
		b.w(`<div class="field rule-f-en"><span class="field-label">` + t(lang, "form.rule.enabled") + `</span>`)
		b.w(`<label class="rule-toggle"><input class="r-enabled" type="checkbox"` + chk + `><span class="rule-toggle-slider"></span></label></div>`)
		// delete
		b.w(`<div class="field rule-f-del"><span class="field-label">&nbsp;</span>`)
		b.w(`<button type="button" class="btn btn-sm btn-danger" onclick="this.closest('.rule-card').remove()">` + delLabel + `</button></div>`)
		b.w(`</div></div>`)
	}

	b.w(`<div id="rules-body">`)
	for _, r := range rules {
		renderRow(r)
	}
	b.w(`</div>`)

	b.w(`<div class="rule-actions">`)
	b.w(`<button type="button" class="btn btn-sm" onclick="tmAddRule()">+ ` + t(lang, "btn.add_rule") + `</button>`)
	b.w(`<button type="button" class="btn btn-sm btn-outline" onclick="tmResetRules()">` + t(lang, "btn.reset_rules") + `</button>`)
	b.w(`<button type="button" class="btn" onclick="tmSaveRules()">` + t(lang, "btn.save_rules") + `</button>`)
	b.w(`</div>`)

	// ── alert behavior: cooldown + digest interval ──
	cooldownMin, digestMin := 30, 0
	if bev, ok := s.mgr.(interface {
		AlertBehavior() (int, int)
	}); ok {
		cooldownMin, digestMin = bev.AlertBehavior()
	}
	b.w(`<div class="card behavior-card"><div class="form-grid">`)
	b.w(`<div class="field"><span class="field-label">` + t(lang, "form.cooldown") + `</span>`)
	b.w(`<input id="beh-cooldown" type="number" min="0" step="1" value="` + fmt.Sprintf("%d", cooldownMin) + `">`)
	b.w(`<p class="meta">` + t(lang, "form.cooldown.hint") + `</p></div>`)
	b.w(`<div class="field"><span class="field-label">` + t(lang, "form.digest") + `</span>`)
	b.w(`<input id="beh-digest" type="number" min="0" step="1" value="` + fmt.Sprintf("%d", digestMin) + `">`)
	b.w(`<p class="meta">` + t(lang, "form.digest.hint") + `</p></div>`)
	b.w(`<div class="field full"><button type="button" class="btn" onclick="tmSaveBehavior()">` + t(lang, "btn.save_behavior") + `</button></div>`)
	b.w(`</div></div>`)

	b.w(`</div>`)

	// JS options arrays
	typeOptsJS := "["
	for i, o := range typeOptions {
		if i > 0 {
			typeOptsJS += ","
		}
		typeOptsJS += `{v:` + jsQuote(o.val) + `,l:` + jsQuote(o.label) + `}`
	}
	typeOptsJS += "]"
	dirOptsJS := "["
	for i, o := range dirOptions {
		if i > 0 {
			dirOptsJS += ","
		}
		dirOptsJS += `{v:` + jsQuote(o.val) + `,l:` + jsQuote(o.label) + `}`
	}
	dirOptsJS += "]"

	b.w(`<script>
var _typeOpts=` + typeOptsJS + `;
var _dirOpts=` + dirOptsJS + `;
// Rule types that carry a signed delta → direction (up/down) is meaningful.
var _dirTypes={'delta_pct':1,'delta_abs':1,'probe_markup_pct':1,'group_ratio_delta_pct':1,'quota_drop_pct':1};
function tmOnTypeChange(sel){
  var card=sel.closest('.rule-card');
  var dirField=card.querySelector('.rule-f-dir');
  if(!dirField)return;
  var dirSel=card.querySelector('.r-direction');
  var csel=dirField.querySelector('.csel');        // custom dropdown wrapper (built by global init)
  var ph=dirField.querySelector('.dir-na');        // "— 不适用" placeholder
  if(_dirTypes[sel.value]){
    if(dirSel)dirSel.disabled=false;
    if(csel)csel.style.display='';else if(dirSel)dirSel.style.display='';
    if(ph)ph.style.display='none';
    dirField.style.opacity='1';
  }else{
    if(dirSel){dirSel.value='both';dirSel.disabled=true;dirSel.style.display='none';}
    if(csel)csel.style.display='none';
    if(ph)ph.style.display='';
    dirField.style.opacity='.5';
  }
}
function tmAddRule(){
  var body=document.getElementById('rules-body');
  var card=document.createElement('div');card.className='rule-card card';
  card.innerHTML='<div class="rule-fields">'
    +'<div class="field rule-f-name"><span class="field-label">` + t(lang, "form.rule.name") + `</span><input class="r-name"></div>'
    +'<div class="field rule-f-type"><span class="field-label">` + t(lang, "form.rule.type") + `</span><select class="r-type" onchange="tmOnTypeChange(this)">'+_typeOpts.map(function(o){return '<option value="'+o.v+'">'+o.l+'</option>';}).join('')+'</select></div>'
    +'<div class="field rule-f-thr"><span class="field-label">` + t(lang, "form.rule.threshold") + `</span><input class="r-threshold" type="number" step="any" min="0" value="5"></div>'
    +'<div class="field rule-f-dir"><span class="field-label">` + t(lang, "form.rule.direction") + `</span><select class="r-direction">'+_dirOpts.map(function(o){return '<option value="'+o.v+'">'+o.l+'</option>';}).join('')+'</select><span class="dir-na">— 不适用</span></div>'
    +'<div class="field rule-f-en"><span class="field-label">` + t(lang, "form.rule.enabled") + `</span><label class="rule-toggle"><input class="r-enabled" type="checkbox" checked><span class="rule-toggle-slider"></span></label></div>'
    +'<div class="field rule-f-del"><span class="field-label">&nbsp;</span><button type="button" class="btn btn-sm btn-danger" onclick="this.closest(\'.rule-card\').remove()">` + delLabel + `</button></div>'
    +'</div>';
  body.appendChild(card);
  tmOnTypeChange(card.querySelector('.r-type'));
}
function tmCollectRules(){
  var cards=document.querySelectorAll('#rules-body .rule-card');
  var rules=[];
  cards.forEach(function(c){
    rules.push({
      name: c.querySelector('.r-name').value,
      type: c.querySelector('.r-type').value,
      threshold: parseFloat(c.querySelector('.r-threshold').value)||0,
      direction: c.querySelector('.r-direction').value,
      enabled: c.querySelector('.r-enabled').checked
    });
  });
  return rules;
}
function tmSaveRules(){
  var rules=tmCollectRules();
  fetch('/api/settings/rules',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(rules)})
    .then(function(r){ if(r.ok){ alert(` + jsQuote(t(lang, "settings.rules.saved")) + `); location.reload(); } else { r.text().then(function(t){ alert(t); }); } })
    .catch(function(e){ alert(e); });
}
function tmResetRules(){
  if(!confirm(` + jsQuote(t(lang, "btn.reset_rules")+"?") + `))return;
  fetch('/api/settings/rules/reset',{method:'POST'})
    .then(function(r){ if(r.ok){ alert(` + jsQuote(t(lang, "settings.rules.reset")) + `); location.reload(); } else { r.text().then(function(t){ alert(t); }); } })
    .catch(function(e){ alert(e); });
}
function tmSaveBehavior(){
  var cd=document.getElementById('beh-cooldown').value;
  var di=document.getElementById('beh-digest').value;
  fetch('/api/settings/behavior',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({cooldown_minutes:parseInt(cd)||0,digest_interval_minutes:parseInt(di)||0})})
    .then(function(r){ if(r.ok){ alert(` + jsQuote(t(lang, "settings.behavior.saved")) + `); } else { r.text().then(function(t){ alert(t); }); } })
    .catch(function(e){ alert(e); });
}
// Set initial direction-field state for existing rows (disable for non-directional types).
// Wrapped in DOMContentLoaded so the global custom-select (.csel) widget is built first.
document.addEventListener('DOMContentLoaded',function(){
  document.querySelectorAll('#rules-body .r-type').forEach(function(s){tmOnTypeChange(s);});
});
</script>`)
}

// pageBuilder is a tiny string builder with HTML-escape passthrough (callers
// pre-escape interpolated values with esc()).
type pageBuilder struct{ buf []byte }

func (b *pageBuilder) w(s string)     { b.buf = append(b.buf, s...) }
func (b *pageBuilder) String() string { return string(b.buf) }

// jsQuote returns a JS-single-quoted-string literal for s (for inline scripts).
func jsQuote(s string) string {
	out := []byte{'\''}
	for _, r := range s {
		switch r {
		case '\\':
			out = append(out, '\\', '\\')
		case '\'':
			out = append(out, '\\', '\'')
		case '\n':
			out = append(out, '\\', 'n')
		default:
			out = append(out, string(r)...)
		}
	}
	out = append(out, '\'')
	return string(out)
}

func (s *Server) settingsSave(w http.ResponseWriter, r *http.Request) {
	if s.mgr == nil {
		http.Error(w, t(s.lang(w, r), "settings.no.manager"), http.StatusServiceUnavailable)
		return
	}
	if len(s.encKey) == 0 {
		http.Error(w, t(s.lang(w, r), "settings.enc.disabled"), http.StatusBadRequest)
		return
	}
	var nc alert.NotifierConfig
	if err := json.NewDecoder(r.Body).Decode(&nc); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	saver, ok := s.mgr.(interface {
		SaveNotifierConfig(context.Context, alert.NotifierConfig) error
	})
	if !ok {
		http.Error(w, t(s.lang(w, r), "settings.no.manager"), http.StatusServiceUnavailable)
		return
	}
	if err := saver.SaveNotifierConfig(r.Context(), nc); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "saved"})
}

func (s *Server) settingsTest(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	if s.mgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": t(lang, "settings.no.manager")})
		return
	}
	var in struct {
		Kind string `json:"kind"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	tester, ok := s.mgr.(interface {
		SendTestAlert(context.Context, string) error
	})
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": t(lang, "settings.no.manager")})
		return
	}
	if err := tester.SendTestAlert(r.Context(), in.Kind); err != nil {
		writeJSON(w, 200, map[string]string{"error": fmt.Sprintf(t(lang, "settings.test.fail"), err.Error())})
		return
	}
	writeJSON(w, 200, map[string]string{"message": t(lang, "settings.test.ok")})
}

// settingsRulesSave handles POST /api/settings/rules.
func (s *Server) settingsRulesSave(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	if s.mgr == nil {
		http.Error(w, t(lang, "settings.no.manager"), http.StatusServiceUnavailable)
		return
	}
	var rules []alert.Rule
	if err := json.NewDecoder(r.Body).Decode(&rules); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	saver, ok := s.mgr.(interface {
		SaveAlertRules(context.Context, []alert.Rule) error
	})
	if !ok {
		http.Error(w, t(lang, "settings.no.manager"), http.StatusServiceUnavailable)
		return
	}
	if err := saver.SaveAlertRules(r.Context(), rules); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "saved"})
}

// settingsRulesReset handles POST /api/settings/rules/reset.
func (s *Server) settingsRulesReset(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	if s.mgr == nil {
		http.Error(w, t(lang, "settings.no.manager"), http.StatusServiceUnavailable)
		return
	}
	resetter, ok := s.mgr.(interface {
		ResetRules(context.Context) error
	})
	if !ok {
		http.Error(w, t(lang, "settings.no.manager"), http.StatusServiceUnavailable)
		return
	}
	if err := resetter.ResetRules(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "reset"})
}

// settingsBehaviorSave handles POST /api/settings/behavior (cooldown + digest).
func (s *Server) settingsBehaviorSave(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	if s.mgr == nil {
		http.Error(w, t(lang, "settings.no.manager"), http.StatusServiceUnavailable)
		return
	}
	var in struct {
		CooldownMinutes       int `json:"cooldown_minutes"`
		DigestIntervalMinutes int `json:"digest_interval_minutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	saver, ok := s.mgr.(interface {
		SaveAlertBehavior(context.Context, int, int) error
	})
	if !ok {
		http.Error(w, t(lang, "settings.no.manager"), http.StatusServiceUnavailable)
		return
	}
	if err := saver.SaveAlertBehavior(r.Context(), in.CooldownMinutes, in.DigestIntervalMinutes); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "saved"})
}
