package dashboard

import (
	"fmt"
	"html"
	"net/http"
	"strings"
)

// appCSS — shared stylesheet (sub2api-inspired teal theme + dark mode overrides).
const appCSS = `
:root{
  --bg1:#f8fafc;--bg2:#f0fdfa;--bg3:#f1f5f9;
  --card:#fff;--ink:#1e293b;--muted:#64748b;--border:#e2e8f0;--row:#f6f8fa;
  --primary:#14b8a6;--primary-600:#0d9488;--primary-700:#0f766e;--primary-50:#f0fdfa;--primary-100:#ccfbf1;--primary-soft:#d3f3ee;
  --ok:#10b981;--warn:#f59e0b;--crit:#ef4444;
  --ok-soft:#f0fdf4;--warn-soft:#fef3c7;--crit-soft:#fee2e2;
  --mono:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;
  --shadow:0 4px 6px -1px rgba(16,24,40,.07),0 2px 4px -2px rgba(16,24,40,.05);
  --th-bg:#f1f5f8;--th-ink:#334155;--hdr-bg:rgba(255,255,255,.82);
}
[data-theme="dark"]{
  --bg1:#0b1220;--bg2:#0d1526;--bg3:#0b1220;--card:#111a2e;--ink:#e5e7eb;--muted:#94a3b8;--border:#1e293b;--row:#0f1830;
  --primary:#2dd4bf;--primary-600:#14b8a6;--primary-700:#5eead4;--primary-50:#0d1f1d;--primary-100:#0c2622;--primary-soft:#10302c;
  --ok:#34d399;--warn:#fbbf24;--crit:#f87171;
  --ok-soft:#0f1f1a;--warn-soft:#1f1808;--crit-soft:#1f0e0e;
  --shadow:0 4px 6px -1px rgba(0,0,0,.4);
  --th-bg:#0d1526;--th-ink:#cbd5e1;--hdr-bg:rgba(17,26,46,.82);
}
*{box-sizing:border-box}
html{scroll-behavior:smooth;-webkit-font-smoothing:antialiased}
body{margin:0;font:14px/1.55 -apple-system,BlinkMacSystemFont,"Segoe UI",system-ui,sans-serif;color:var(--ink);background:linear-gradient(135deg,var(--bg1),var(--bg2) 40%,var(--bg3));min-height:100vh;position:relative;overflow-x:hidden}
body::before{content:"";position:fixed;inset:0;pointer-events:none;z-index:0;
  background:radial-gradient(55rem 55rem at 108% -8%,rgba(20,184,166,.10),transparent 60%),radial-gradient(45rem 45rem at -8% 108%,rgba(20,184,166,.07),transparent 60%),linear-gradient(rgba(20,184,166,.025) 1px,transparent 1px) 0 0/64px 64px,linear-gradient(90deg,rgba(20,184,166,.025) 1px,transparent 1px) 0 0/64px 64px}
[data-theme="dark"] body::before{background:radial-gradient(55rem 55rem at 108% -8%,rgba(45,212,191,.10),transparent 60%),radial-gradient(45rem 45rem at -8% 108%,rgba(45,212,191,.07),transparent 60%),linear-gradient(rgba(45,212,191,.04) 1px,transparent 1px) 0 0/64px 64px,linear-gradient(90deg,rgba(45,212,191,.04) 1px,transparent 1px) 0 0/64px 64px}
header.top,main,footer{position:relative;z-index:1}
a{color:var(--primary-700);text-decoration:none}
a:hover{text-decoration:underline}
header.top{position:sticky;top:0;z-index:20;background:var(--hdr-bg);backdrop-filter:blur(10px);border-bottom:1px solid var(--border);padding:.55rem 1.2rem}
header .top-row{max-width:1180px;margin:0 auto;display:flex;align-items:center;gap:1.1rem;flex-wrap:wrap}
header .brand{font-weight:700;font-size:1.05rem;display:flex;align-items:center;gap:.5rem}
header .brand .logo{width:30px;height:30px;border-radius:9px;background:linear-gradient(135deg,var(--primary),var(--primary-600));display:inline-flex;align-items:center;justify-content:center;color:#fff;font-size:.82rem;font-weight:800;box-shadow:var(--shadow)}
nav{display:flex;gap:.2rem;flex-wrap:wrap;margin-left:auto}
nav a{color:var(--muted);padding:.34rem .7rem;border-radius:8px;font-weight:500}
nav a:hover{background:var(--primary-50);color:var(--primary-700);text-decoration:none}
nav a.active{background:var(--primary-100);color:var(--primary-700);font-weight:600}
.tools{display:flex;gap:.4rem;align-items:center}
.icon-btn,.lang-btn{border:1px solid var(--border);background:var(--card);color:var(--muted);border-radius:8px;padding:.32rem .55rem;font-size:.85rem;cursor:pointer;font-weight:600;line-height:1}
.icon-btn:hover,.lang-btn:hover{background:var(--primary-50);color:var(--primary-700)}
.lang-btn{font-family:var(--mono)}
main{max-width:1180px;margin:0 auto;padding:1.6rem 1.2rem 3rem}
h1{font-size:1.4rem;margin:.1rem 0 .15rem;font-weight:700}
.sub{color:var(--muted);margin:0 0 1.2rem;font-size:.92rem}
.card{background:var(--card);border:1px solid var(--border);border-radius:12px;padding:1.1rem 1.15rem;margin-bottom:1.1rem;box-shadow:var(--shadow)}
.card h2{margin:.1rem 0 .7rem;font-size:1.02rem}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(255px,1fr));gap:1rem}
.stcard{display:flex;flex-direction:column;gap:.3rem}
.stcard .kpi-label{color:var(--muted);font-size:.74rem;text-transform:uppercase;letter-spacing:.05em;display:flex;align-items:center;gap:.4rem}
.stcard .kpi{font-size:1.85rem;font-weight:700;font-variant-numeric:tabular-nums;line-height:1;margin:.15rem 0}
.stcard .meta{color:var(--muted);font-size:.82rem;line-height:1.55;word-break:break-all}
.dot-s{width:8px;height:8px;border-radius:50%;display:inline-block}
.dot-s.ok{background:var(--ok)} .dot-s.bad{background:var(--crit)} .dot-s.none{background:#cbd5e1}
.tbl-wrap{overflow-x:auto;border:1px solid var(--border);border-radius:11px;background:var(--card);box-shadow:var(--shadow)}
table{width:100%;border-collapse:collapse;font-size:.9rem}
thead th{position:sticky;top:0;background:var(--th-bg);text-align:left;padding:.55rem .7rem;border-bottom:1px solid var(--border);white-space:nowrap;font-weight:600;color:var(--th-ink)}
tbody td{padding:.45rem .7rem;border-bottom:1px solid var(--border);font-variant-numeric:tabular-nums;vertical-align:middle}
tbody tr:last-child td{border-bottom:0}
tbody tr:hover{background:var(--row)}
.mono{font-family:var(--mono)}
.num{text-align:right;white-space:nowrap}
.badge{display:inline-block;padding:.14rem .55rem;border-radius:999px;font-size:.7rem;font-weight:700;line-height:1.5;white-space:nowrap}
.b-ok{background:var(--ok-soft);color:var(--ok)} .b-warn{background:var(--warn-soft);color:var(--warn)} .b-crit{background:var(--crit-soft);color:var(--crit)} .b-muted{background:#eef2f5;color:var(--muted)}
[data-theme="dark"] .b-muted{background:#1e293b;color:var(--muted)}
.pcell{font-family:var(--mono);font-weight:600}
.p-cheap{color:var(--ok)} .p-high{color:var(--crit)} .p-mid{color:var(--ink)} .p-na{color:#94a3b8;font-weight:400}
.btn{display:inline-flex;align-items:center;gap:.4rem;border-radius:10px;padding:.4rem .85rem;font-size:.85rem;font-weight:600;background:linear-gradient(135deg,var(--primary),var(--primary-600));color:#fff;box-shadow:0 2px 6px rgba(20,184,166,.25)}
.btn:hover{text-decoration:none;filter:brightness(1.06)}
.kvs{display:flex;flex-wrap:wrap;gap:.35rem .9rem;font-size:.85rem}
.kvs b{color:var(--muted);font-weight:500}
footer{color:var(--muted);font-size:.8rem;text-align:center;padding:1.2rem}
.tag{font-family:var(--mono);font-size:.72rem;background:#eef2f5;color:#334155;padding:.05rem .35rem;border-radius:5px}
[data-theme="dark"] .tag{background:#1e293b;color:#cbd5e1}
.tag-pri{background:var(--primary-50);color:var(--primary-700)}
.empty{color:var(--muted);padding:1.4rem;text-align:center}
@media(max-width:640px){header.top{padding:.5rem .8rem}main{padding:1rem .8rem}nav{margin-left:0;width:100%}}
`

type navItem struct{ H, Label, Key string }

var navItems = []navItem{
	{"/", "", "overview"},
	{"/matrix", "", "matrix"},
	{"/changes", "", "changes"},
	{"/probes", "", "probes"},
	{"/audit", "", "audit"},
	{"/stations", "", "stations"},
	{"/metrics", "", "metrics"},
}

// pageShell wraps body in a full HTML doc with the shared CSS + top nav +
// theme (dark/light) + language (中/EN) toggles (client-side, persisted).
func pageShell(lang, title, active, body string) string {
	var n strings.Builder
	for _, it := range navItems {
		cls := ""
		if it.Key == active {
			cls = ` class="active"`
		}
		fmt.Fprintf(&n, `<a href="%s"%s>%s</a>`, it.H, cls, t(lang, "nav."+it.Key))
	}
	other := "en"
	otherLabel := "EN"
	if lang == "en" {
		other, otherLabel = "zh", "中"
	}
	tools := fmt.Sprintf(`<div class="tools"><button class="icon-btn" id="tm-theme" onclick="tmToggleTheme()" title="%s">🌙</button>`+
		`<button class="lang-btn" onclick="tmSetLang('%s')">%s</button></div>`, t(lang, "theme"), other, otherLabel)
	js := `<script>(function(){var d=document.documentElement,s=localStorage.getItem('tm-theme');` +
		`if(s==='dark'||s==='light'){d.dataset.theme=s;}function sync(){var b=document.getElementById('tm-theme');if(b)b.textContent=d.dataset.theme==='dark'?'☀️':'🌙';}sync();` +
		`window.tmToggleTheme=function(){var n=(d.dataset.theme==='dark')?'light':'dark';d.dataset.theme=n;localStorage.setItem('tm-theme',n);sync();};` +
		`window.tmSetLang=function(l){document.cookie='tm-lang='+l+';path=/;max-age=2592000';location.reload();};})();</script>`
	return fmt.Sprintf(`<!doctype html><html lang="%s"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s · TransitMonitor</title><style>%s</style></head>`+
		`<body><header class="top"><div class="top-row"><div class="brand"><span class="logo">TM</span>TransitMonitor</div><nav>%s</nav>%s</div></header>`+
		`<main>%s</main><footer>TransitMonitor · <a href="/api/stations">JSON API</a> · <a href="/metrics">/metrics</a> (Prometheus) · <a href="/healthz">/healthz</a></footer>%s</body></html>`,
		lang, html.EscapeString(title), appCSS, n.String(), tools, body, js)
}

func writeHTMLShell(w http.ResponseWriter, lang, title, active, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(pageShell(lang, title, active, body)))
}

// renderTable renders a styled table; cells are raw HTML (callers escape plain text).
func renderTable(lang string, cols []string, rows [][]string) string {
	var b strings.Builder
	b.WriteString(`<div class="tbl-wrap"><table><thead><tr>`)
	for _, c := range cols {
		b.WriteString("<th>")
		b.WriteString(html.EscapeString(c))
		b.WriteString("</th>")
	}
	b.WriteString("</tr></thead><tbody>")
	if len(rows) == 0 {
		b.WriteString(`<tr><td colspan="` + fmt.Sprint(len(cols)) + `"><div class="empty">` + t(lang, "empty") + `</div></td></tr>`)
	}
	for _, row := range rows {
		b.WriteString("<tr>")
		for _, cell := range row {
			b.WriteString("<td>")
			b.WriteString(cell)
			b.WriteString("</td>")
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table></div>")
	return b.String()
}

func severityBadge(lang, sev string) string {
	cls := "b-muted"
	key := "badge.info"
	switch strings.ToLower(sev) {
	case "critical":
		cls, key = "b-crit", "badge.critical"
	case "warning":
		cls, key = "b-warn", "badge.warning"
	case "info":
		cls, key = "b-muted", "badge.info"
	}
	return `<span class="badge ` + cls + `" title="` + esc(sev) + `">` + t(lang, key) + `</span>`
}

func statusBadge(lang, sentinel string) string {
	if sentinel == "" {
		return `<span class="badge b-ok" title="ok">` + t(lang, "badge.ok") + `</span>`
	}
	return `<span class="badge b-warn">` + esc(sentinel) + `</span>`
}

func priceColorClass(v, lo, hi float64) string {
	if hi <= lo {
		return "p-mid"
	}
	if v <= lo+1e-12 {
		return "p-cheap"
	}
	if v >= hi-1e-12 {
		return "p-high"
	}
	return "p-mid"
}

func esc(s string) string { return html.EscapeString(s) }
