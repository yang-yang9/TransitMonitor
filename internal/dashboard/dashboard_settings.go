package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"transitmonitor/internal/alert"
)

// settingsHTML renders the notifier configuration form (/settings). Mirrors the
// station form (dashboard_stations.go): server-rendered HTML + inline fetch()
// JS posting JSON to /api/settings. Secrets are never pre-filled (keep-blank
// contract); when no encryption key is set the page is read-only with a hint.
func (s *Server) settingsHTML(w http.ResponseWriter, r *http.Request) {
	lang := s.lang(w, r)
	encEnabled := len(s.encKey) > 0

	// Defaults from the live (merged, secrets-redacted) notifier config.
	var nc alert.NotifierConfig
	if m, ok := s.mgr.(interface{ NotifierConfigs(context.Context) alert.NotifierConfig }); ok {
		nc = m.NotifierConfigs(r.Context())
	}
	// Read-only display values for the (redacted) secret fields' placeholders.
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

	var b pageBuilder
	b.w(`<div class="page-hdr"><h1>` + t(lang, "title.settings") + `</h1><p class="sub">` + t(lang, "sub.settings") + `</p></div>`)

	if !encEnabled {
		b.w(`<div class="card"><div class="banner b-warn">` + t(lang, "settings.enc.disabled") + `</div></div>`)
	}

	b.w(`<div class="card"><form id="settings-form" class="form-grid" onsubmit="return tmSettingsSave();">`)

	// DingTalk
	b.w(`<div class="field full"><h3>` + t(lang, "section.dingtalk") + `</h3></div>`)
	b.w(field(t(lang, "form.webhook.url"), "dingtalk_webhook", nc.DingTalk.Webhook, "https://oapi.dingtalk.com/robot/send?access_token=...", "", true))
	b.w(field(t(lang, "form.secret"), "dingtalk_secret", "", dingSecretPH, "password", false))

	// Lark / 飞书
	b.w(`<div class="field full"><h3>` + t(lang, "section.lark") + `</h3></div>`)
	b.w(field(t(lang, "form.webhook.url"), "lark_webhook", nc.Lark.Webhook, "https://open.feishu.cn/open-apis/bot/v2/hook/...", "", true))
	b.w(field(t(lang, "form.secret"), "lark_secret", "", larkSecretPH, "password", false))

	// Slack
	b.w(`<div class="field full"><h3>` + t(lang, "section.slack") + `</h3></div>`)
	b.w(field(t(lang, "form.webhook.url"), "slack_webhook", nc.Slack.Webhook, "https://hooks.slack.com/services/...", "", true))

	// Generic webhook
	b.w(`<div class="field full"><h3>` + t(lang, "section.webhook") + `</h3></div>`)
	b.w(field(t(lang, "form.webhook.url"), "webhook_url", nc.Webhook.URL, "https://example.com/hook", "", true))

	// QQ bot
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

	writeHTMLShell(w, lang, t(lang, "title.settings"), "settings", b.String())
}

// pageBuilder is a tiny string builder with HTML-escape passthrough (callers
// pre-escape interpolated values with esc()).
type pageBuilder struct{ buf []byte }

func (b *pageBuilder) w(s string) { b.buf = append(b.buf, s...) }
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
	saver, ok := s.mgr.(interface{ SaveNotifierConfig(context.Context, alert.NotifierConfig) error })
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
	var in struct{ Kind string `json:"kind"` }
	_ = json.NewDecoder(r.Body).Decode(&in)
	tester, ok := s.mgr.(interface{ SendTestAlert(context.Context, string) error })
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
