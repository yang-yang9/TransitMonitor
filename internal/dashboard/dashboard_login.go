package dashboard

import (
	"fmt"
	"net/http"
)

func writeLoginPage(w http.ResponseWriter, lang, errMsg string) {
	errHTML := ""
	if errMsg != "" {
		errHTML = `<div class="login-err">` + esc(errMsg) + `</div>`
	}

	body := fmt.Sprintf(`<style>
.login-wrap{display:flex;align-items:center;justify-content:center;min-height:70vh}
.login-box{width:100%%;max-width:400px;background:var(--card);border:1px solid var(--border);border-radius:var(--radius);padding:2.5rem 2rem;box-shadow:var(--shadow-lg);text-align:center}
.login-box .logo-lg{width:56px;height:56px;border-radius:14px;background:linear-gradient(135deg,var(--primary),var(--primary-600));display:inline-flex;align-items:center;justify-content:center;color:#fff;font-size:1.2rem;font-weight:800;margin-bottom:1.2rem;animation:logo-glow 3s ease-in-out infinite}
.login-box h1{font-size:1.3rem;margin:0 0 .3rem;font-weight:800;letter-spacing:-.01em}
.login-box .sub{margin-bottom:1.5rem}
.login-input{width:100%%;padding:.75rem 1rem;font-size:1rem;font-family:inherit;color:var(--ink);background:var(--input-bg);border:2px solid var(--input-border);border-radius:var(--radius-sm);outline:none;transition:border-color .15s,box-shadow .2s;margin-bottom:1rem;text-align:center;letter-spacing:.1em}
.login-input:focus{border-color:var(--primary);box-shadow:var(--input-ring)}
.login-btn{width:100%%;padding:.75rem;font-size:1rem;font-weight:700;cursor:pointer;border:none;border-radius:var(--radius-sm);background:linear-gradient(135deg,var(--primary),var(--primary-600));color:#fff;box-shadow:0 2px 10px rgba(20,184,166,.3),inset 0 1px 0 rgba(255,255,255,.15);transition:all .2s cubic-bezier(.4,0,.2,1)}
.login-btn:hover{filter:brightness(1.08);transform:translateY(-2px);box-shadow:0 6px 20px rgba(20,184,166,.35)}
.login-btn:active{transform:translateY(0) scale(.98);filter:brightness(.95)}
.login-err{background:var(--crit-soft);color:var(--crit);padding:.55rem .8rem;border-radius:var(--radius-xs);font-size:.88rem;font-weight:600;margin-bottom:1rem}
</style>
<div class="login-wrap"><div class="login-box">
<span class="logo-lg">TM</span>
<h1>TransitMonitor</h1>
<p class="sub">%s</p>
%s
<form method="POST" action="/api/login">
<input class="login-input" type="password" name="password" placeholder="%s" autofocus autocomplete="current-password">
<button class="login-btn" type="submit">%s</button>
</form>
</div></div>`,
		esc(t(lang, "login.subtitle")),
		errHTML,
		esc(t(lang, "login.placeholder")),
		esc(t(lang, "login.submit")),
	)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = w.Write([]byte(loginPageShell(lang, body)))
}

func loginPageShell(lang, body string) string {
	favicon := `<link rel="icon" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'%3E%3Crect width='32' height='32' rx='8' fill='%2314b8a6'/%3E%3Ctext x='16' y='23' font-size='16' font-weight='bold' fill='white' text-anchor='middle' font-family='sans-serif'%3ETM%3C/text%3E%3C/svg%3E">`
	js := `<script>(function(){var d=document.documentElement,s=localStorage.getItem('tm-theme');` +
		`if(s==='dark'||s==='light'){d.dataset.theme=s;}` +
		`})();</script>`
	return fmt.Sprintf(`<!doctype html><html lang="%s"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">%s<title>%s · TransitMonitor</title><style>%s</style></head>`+
		`<body>%s%s</body></html>`,
		lang, favicon, esc(t(lang, "login.title")), appCSS, body, js)
}
