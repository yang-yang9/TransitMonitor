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

// settingsRulesTab renders the alert rules editor table.
func (s *Server) settingsRulesTab(b *pageBuilder, ctx context.Context, lang string) {
	var rules []alert.Rule
	if m, ok := s.mgr.(interface {
		AlertRules(context.Context) []alert.Rule
	}); ok {
		rules = m.AlertRules(ctx)
	}

	b.w(`<p class="sub meta">` + t(lang, "sub.rules") + `</p>`)
	b.w(`<div class="card"><div id="rules-editor">`)

	// Table header
	b.w(`<table class="tbl" id="rules-table"><thead><tr>`)
	b.w(`<th>` + t(lang, "form.rule.name") + `</th>`)
	b.w(`<th>` + t(lang, "form.rule.type") + `</th>`)
	b.w(`<th>` + t(lang, "form.rule.threshold") + `</th>`)
	b.w(`<th>` + t(lang, "form.rule.direction") + `</th>`)
	b.w(`<th>` + t(lang, "form.rule.enabled") + `</th>`)
	b.w(`<th></th>`)
	b.w(`</tr></thead><tbody id="rules-body">`)

	typeOptions := []struct{ val, label string }{
		{"delta_pct", "delta_pct"},
		{"delta_abs", "delta_abs"},
		{"model_added", "model_added"},
		{"model_removed", "model_removed"},
		{"probe_markup_pct", "probe_markup_pct"},
		{"endpoint_auth_failed", "endpoint_auth_failed"},
		{"poll_failure_streak", "poll_failure_streak"},
		{"group_ratio_delta_pct", "group_ratio_delta_pct"},
		{"quota_below", "quota_below"},
		{"quota_drop_pct", "quota_drop_pct"},
	}

	dirOptions := []struct{ val, label string }{
		{"both", t(lang, "form.rule.dir.both")},
		{"up", t(lang, "form.rule.dir.up")},
		{"down", t(lang, "form.rule.dir.down")},
	}

	renderRow := func(r alert.Rule) {
		dir := r.Direction
		if dir == "" {
			dir = "both"
		}
		chk := ""
		if r.Enabled {
			chk = " checked"
		}
		b.w(`<tr class="rule-row">`)
		b.w(`<td><input class="r-name" value="` + esc(r.Name) + `" style="width:100%"></td>`)
		b.w(`<td><select class="r-type">`)
		for _, o := range typeOptions {
			sel := ""
			if o.val == r.Type {
				sel = " selected"
			}
			b.w(`<option value="` + o.val + `"` + sel + `>` + o.label + `</option>`)
		}
		b.w(`</select></td>`)
		b.w(`<td><input class="r-threshold" type="number" step="any" min="0" value="` + fmt.Sprintf("%g", r.Threshold) + `" style="width:80px"></td>`)
		b.w(`<td><select class="r-direction">`)
		for _, o := range dirOptions {
			sel := ""
			if o.val == dir {
				sel = " selected"
			}
			b.w(`<option value="` + o.val + `"` + sel + `>` + esc(o.label) + `</option>`)
		}
		b.w(`</select></td>`)
		b.w(`<td><input class="r-enabled" type="checkbox"` + chk + `></td>`)
		b.w(`<td><button type="button" class="btn btn-sm btn-danger" onclick="this.closest('tr').remove()">` + t(lang, "btn.delete_rule") + `</button></td>`)
		b.w(`</tr>`)
	}

	for _, r := range rules {
		renderRow(r)
	}
	b.w(`</tbody></table>`)

	b.w(`<div style="margin-top:.5rem;display:flex;gap:.5rem;flex-wrap:wrap">`)
	b.w(`<button type="button" class="btn btn-sm" onclick="tmAddRule()">` + t(lang, "btn.add_rule") + `</button>`)
	b.w(`<button type="button" class="btn btn-sm btn-outline" onclick="tmResetRules()">` + t(lang, "btn.reset_rules") + `</button>`)
	b.w(`<button type="button" class="btn" onclick="tmSaveRules()">` + t(lang, "btn.save_rules") + `</button>`)
	b.w(`</div>`)

	b.w(`</div></div>`)

	// Template row for JS add
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
function tmAddRule(){
  var tb=document.getElementById('rules-body');
  var tr=document.createElement('tr');tr.className='rule-row';
  tr.innerHTML='<td><input class="r-name" style="width:100%"></td>'
    +'<td><select class="r-type">'+_typeOpts.map(function(o){return '<option value="'+o.v+'">'+o.l+'</option>';}).join('')+'</select></td>'
    +'<td><input class="r-threshold" type="number" step="any" min="0" value="5" style="width:80px"></td>'
    +'<td><select class="r-direction">'+_dirOpts.map(function(o){return '<option value="'+o.v+'">'+o.l+'</option>';}).join('')+'</select></td>'
    +'<td><input class="r-enabled" type="checkbox" checked></td>'
    +'<td><button type="button" class="btn btn-sm btn-danger" onclick="this.closest(\'tr\').remove()">` + t(lang, "btn.delete_rule") + `</button></td>';
  tb.appendChild(tr);
}
function tmCollectRules(){
  var rows=document.querySelectorAll('#rules-body .rule-row');
  var rules=[];
  rows.forEach(function(tr){
    rules.push({
      name: tr.querySelector('.r-name').value,
      type: tr.querySelector('.r-type').value,
      threshold: parseFloat(tr.querySelector('.r-threshold').value)||0,
      direction: tr.querySelector('.r-direction').value,
      enabled: tr.querySelector('.r-enabled').checked
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
