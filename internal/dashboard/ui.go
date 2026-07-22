package dashboard

import (
	"fmt"
	"html"
	"net/http"
	"strings"
)

const appCSS = `
:root{
  --bg1:#f8fafc;--bg2:#f0fdfa;--bg3:#f1f5f9;
  --card:#fff;--ink:#1e293b;--muted:#64748b;--border:#e2e8f0;--row:#f6f8fa;
  --primary:#14b8a6;--primary-600:#0d9488;--primary-700:#0f766e;--primary-50:#f0fdfa;--primary-100:#ccfbf1;--primary-soft:#d3f3ee;
  --ok:#10b981;--warn:#f59e0b;--crit:#ef4444;
  --ok-soft:#f0fdf4;--warn-soft:#fef3c7;--crit-soft:#fee2e2;
  --mono:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;
  --shadow:0 4px 6px -1px rgba(16,24,40,.07),0 2px 4px -2px rgba(16,24,40,.05);
  --shadow-lg:0 10px 25px -5px rgba(16,24,40,.08),0 8px 10px -6px rgba(16,24,40,.04);
  --th-bg:#f1f5f8;--th-ink:#334155;--hdr-bg:rgba(255,255,255,.82);
  --input-bg:#fff;--input-border:#d1d5db;--input-focus:rgba(20,184,166,.35);
  --input-ring:0 0 0 3px var(--input-focus);
  --radius:12px;--radius-sm:8px;
}
[data-theme="dark"]{
  --bg1:#0b1220;--bg2:#0d1526;--bg3:#0b1220;--card:#111a2e;--ink:#e5e7eb;--muted:#94a3b8;--border:#1e293b;--row:#0f1830;
  --primary:#2dd4bf;--primary-600:#14b8a6;--primary-700:#5eead4;--primary-50:#0d1f1d;--primary-100:#0c2622;--primary-soft:#10302c;
  --ok:#34d399;--warn:#fbbf24;--crit:#f87171;
  --ok-soft:#0f1f1a;--warn-soft:#1f1808;--crit-soft:#1f0e0e;
  --shadow:0 4px 6px -1px rgba(0,0,0,.4);--shadow-lg:0 10px 25px -5px rgba(0,0,0,.5);
  --th-bg:#0d1526;--th-ink:#cbd5e1;--hdr-bg:rgba(17,26,46,.82);
  --input-bg:#0f172a;--input-border:#334155;--input-focus:rgba(45,212,191,.3);
}
*{box-sizing:border-box;margin:0}
html{scroll-behavior:smooth;-webkit-font-smoothing:antialiased}
body{font:14px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",system-ui,sans-serif;color:var(--ink);background:linear-gradient(135deg,var(--bg1),var(--bg2) 40%,var(--bg3));min-height:100vh;position:relative;overflow-x:hidden}
body::before{content:"";position:fixed;inset:0;pointer-events:none;z-index:0;background:radial-gradient(55rem at 108% -8%,rgba(20,184,166,.10),transparent 60%),radial-gradient(45rem at -8% 108%,rgba(20,184,166,.07),transparent 60%),linear-gradient(rgba(20,184,166,.025) 1px,transparent 1px) 0 0/64px 64px,linear-gradient(90deg,rgba(20,184,166,.025) 1px,transparent 1px) 0 0/64px 64px}
[data-theme="dark"] body::before{background:radial-gradient(55rem at 108% -8%,rgba(45,212,191,.10),transparent 60%),radial-gradient(45rem at -8% 108%,rgba(45,212,191,.07),transparent 60%),linear-gradient(rgba(45,212,191,.04) 1px,transparent 1px) 0 0/64px 64px,linear-gradient(90deg,rgba(45,212,191,.04) 1px,transparent 1px) 0 0/64px 64px}
header.top,main,footer{position:relative;z-index:1}
a{color:var(--primary-700);text-decoration:none}a:hover{text-decoration:underline}

/* ── Header ── */
header.top{position:sticky;top:0;z-index:20;background:var(--hdr-bg);backdrop-filter:blur(12px);border-bottom:1px solid var(--border);padding:.55rem 1.2rem}
header .top-row{max-width:1180px;margin:0 auto;display:flex;align-items:center;gap:1rem;flex-wrap:wrap}
header .brand{font-weight:700;font-size:1.05rem;display:flex;align-items:center;gap:.5rem}
header .brand .logo{width:30px;height:30px;border-radius:9px;background:linear-gradient(135deg,var(--primary),var(--primary-600));display:inline-flex;align-items:center;justify-content:center;color:#fff;font-size:.8rem;font-weight:800;box-shadow:var(--shadow),inset 0 1px 0 rgba(255,255,255,.2)}
nav{display:flex;gap:.15rem;flex-wrap:wrap;margin-left:auto}
nav a{color:var(--muted);padding:.35rem .7rem;border-radius:var(--radius-sm);font-weight:500;font-size:.88rem;transition:all .15s}
nav a:hover{background:var(--primary-50);color:var(--primary-700);text-decoration:none}
nav a.active{background:var(--primary-100);color:var(--primary-700);font-weight:600}
.tools{display:flex;gap:.35rem;align-items:center}
.icon-btn,.lang-btn{border:1px solid var(--border);background:var(--card);color:var(--muted);border-radius:var(--radius-sm);padding:.3rem .5rem;font-size:.85rem;cursor:pointer;font-weight:600;line-height:1;transition:all .15s}
.icon-btn:hover,.lang-btn:hover{background:var(--primary-50);color:var(--primary-700);border-color:var(--primary)}
.lang-btn{font-family:var(--mono)}

/* ── Layout ── */
main{max-width:1180px;margin:0 auto;padding:1.6rem 1.2rem 3rem}
h1{font-size:1.4rem;margin:0 0 .15rem;font-weight:700}
h2{font-size:1.05rem;margin:0 0 .7rem;font-weight:600}
.sub{color:var(--muted);margin:0 0 1.2rem;font-size:.9rem;line-height:1.5}

/* ── Card ── */
.card{background:var(--card);border:1px solid var(--border);border-radius:var(--radius);padding:1.25rem;margin-bottom:1.1rem;box-shadow:var(--shadow);transition:transform .2s,box-shadow .2s}
.card:hover{box-shadow:var(--shadow-lg)}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(260px,1fr));gap:1rem}
.stcard{display:flex;flex-direction:column;gap:.3rem;transition:transform .2s}
.stcard:hover{transform:translateY(-2px)}
.stcard .kpi-label{color:var(--muted);font-size:.72rem;text-transform:uppercase;letter-spacing:.06em;display:flex;align-items:center;gap:.4rem}
.stcard .kpi{font-size:1.9rem;font-weight:700;font-variant-numeric:tabular-nums;line-height:1;margin:.12rem 0}
.stcard .meta{color:var(--muted);font-size:.82rem;line-height:1.6;word-break:break-all}
.dot-s{width:8px;height:8px;border-radius:50%;display:inline-block}
.dot-s.ok{background:var(--ok)}.dot-s.bad{background:var(--crit)}.dot-s.none{background:#cbd5e1}

/* ── Table ── */
.tbl-wrap{overflow-x:auto;border:1px solid var(--border);border-radius:var(--radius);background:var(--card);box-shadow:var(--shadow)}
table{width:100%;border-collapse:collapse;font-size:.88rem}
thead th{position:sticky;top:0;background:var(--th-bg);text-align:left;padding:.6rem .75rem;border-bottom:1px solid var(--border);white-space:nowrap;font-weight:600;color:var(--th-ink);font-size:.78rem;text-transform:uppercase;letter-spacing:.03em}
tbody td{padding:.5rem .75rem;border-bottom:1px solid var(--border);font-variant-numeric:tabular-nums;vertical-align:middle}
tbody tr:last-child td{border-bottom:0}
tbody tr{transition:background .1s}
tbody tr:hover{background:var(--row)}
.mono{font-family:var(--mono)}.num{text-align:right;white-space:nowrap}

/* ── Badges ── */
.badge{display:inline-block;padding:.15rem .55rem;border-radius:999px;font-size:.68rem;font-weight:700;line-height:1.5;white-space:nowrap;letter-spacing:.01em}
.b-ok{background:var(--ok-soft);color:var(--ok)}.b-warn{background:var(--warn-soft);color:var(--warn)}.b-crit{background:var(--crit-soft);color:var(--crit)}.b-muted{background:#eef2f5;color:var(--muted)}
[data-theme="dark"] .b-muted{background:#1e293b;color:var(--muted)}
.pcell{font-family:var(--mono);font-weight:600}
.p-cheap{color:var(--ok)}.p-high{color:var(--crit)}.p-mid{color:var(--ink)}.p-na{color:#94a3b8;font-weight:400}
.tag{font-family:var(--mono);font-size:.72rem;background:#eef2f5;color:#334155;padding:.12rem .4rem;border-radius:5px}
[data-theme="dark"] .tag{background:#1e293b;color:#cbd5e1}
.tag-pri{background:var(--primary-50);color:var(--primary-700)}

/* ── Buttons ── */
.btn{display:inline-flex;align-items:center;justify-content:center;gap:.4rem;border:none;border-radius:10px;padding:.5rem 1rem;font-size:.85rem;font-weight:600;cursor:pointer;transition:all .18s;text-decoration:none;background:linear-gradient(135deg,var(--primary),var(--primary-600));color:#fff;box-shadow:0 2px 8px rgba(20,184,166,.25),inset 0 1px 0 rgba(255,255,255,.15)}
.btn:hover{filter:brightness(1.08);transform:translateY(-1px);box-shadow:0 4px 12px rgba(20,184,166,.3);text-decoration:none}
.btn:active{transform:translateY(0);filter:brightness(.97)}
.btn-outline{background:transparent;color:var(--primary-700);border:1.5px solid var(--border);box-shadow:none}
.btn-outline:hover{background:var(--primary-50);border-color:var(--primary);box-shadow:none}
.btn-danger{background:linear-gradient(135deg,var(--crit),#dc2626);box-shadow:0 2px 8px rgba(239,68,68,.2)}
.btn-sm{padding:.3rem .65rem;font-size:.8rem;border-radius:8px}
.btn-group{display:flex;gap:.5rem;flex-wrap:wrap;align-items:center}

/* ── Forms ── */
.form-wrap{max-width:720px;margin:0 auto}
.form-grid{display:grid;grid-template-columns:repeat(2,1fr);gap:1rem}
.form-grid .full{grid-column:1/-1}
.field{display:flex;flex-direction:column;gap:.35rem}
.field-label{font-size:.78rem;font-weight:600;color:var(--muted);text-transform:uppercase;letter-spacing:.04em}
.field input,.field select{width:100%;padding:.6rem .75rem;font-size:.88rem;font-family:inherit;color:var(--ink);background:var(--input-bg);border:1.5px solid var(--input-border);border-radius:var(--radius-sm);outline:none;transition:border-color .15s,box-shadow .15s}
.field input:focus,.field select:focus{border-color:var(--primary);box-shadow:var(--input-ring)}
.field input::placeholder{color:var(--muted);opacity:.7}
.field input[readonly]{opacity:.6;cursor:not-allowed;background:var(--row)}
.field select{-webkit-appearance:none;appearance:none;background-image:url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 12 12'%3E%3Cpath fill='%2364748b' d='M2 4l4 4 4-4'/%3E%3C/svg%3E");background-repeat:no-repeat;background-position:right .75rem center;padding-right:2rem}
/* Toggle switch */
.toggle{position:relative;display:inline-flex;align-items:center;gap:.6rem;cursor:pointer;font-size:.88rem;color:var(--ink)}
.toggle input{position:absolute;opacity:0;width:0;height:0}
.toggle .slider{width:44px;height:24px;background:var(--input-border);border-radius:12px;position:relative;transition:background .2s;flex-shrink:0}
.toggle .slider::after{content:"";position:absolute;top:3px;left:3px;width:18px;height:18px;background:#fff;border-radius:50%;transition:transform .2s;box-shadow:0 1px 3px rgba(0,0,0,.15)}
.toggle input:checked+.slider{background:var(--primary)}
.toggle input:checked+.slider::after{transform:translateX(20px)}
.toggle input:focus-visible+.slider{box-shadow:var(--input-ring)}

/* ── Misc ── */
.kvs{display:flex;flex-wrap:wrap;gap:.4rem .7rem;font-size:.85rem}
footer{color:var(--muted);font-size:.8rem;text-align:center;padding:1.5rem}
.empty{color:var(--muted);padding:1.6rem;text-align:center}
@media(max-width:640px){header.top{padding:.5rem .8rem}main{padding:1rem .8rem}nav{margin-left:0;width:100%}.form-grid{grid-template-columns:1fr}}
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
	tools := fmt.Sprintf(`<div class="tools"><button class="icon-btn" id="tm-autorefresh" onclick="tmToggleAuto()" title="Auto-refresh">🔄</button>`+
		`<button class="icon-btn" id="tm-theme" onclick="tmToggleTheme()" title="%s">🌙</button>`+
		`<button class="lang-btn" onclick="tmSetLang('%s')">%s</button></div>`, t(lang, "theme"), other, otherLabel)
	js := `<script>(function(){var d=document.documentElement,s=localStorage.getItem('tm-theme');` +
		`if(s==='dark'||s==='light'){d.dataset.theme=s;}function syncTheme(){var b=document.getElementById('tm-theme');if(b)b.textContent=d.dataset.theme==='dark'?'☀️':'🌙';}syncTheme();` +
		`window.tmToggleTheme=function(){var n=(d.dataset.theme==='dark')?'light':'dark';d.dataset.theme=n;localStorage.setItem('tm-theme',n);syncTheme();};` +
		`window.tmSetLang=function(l){document.cookie='tm-lang='+l+';path=/;max-age=2592000';location.reload();};` +
		`var ar=localStorage.getItem('tm-autorefresh');function syncAuto(){var b=document.getElementById('tm-autorefresh');if(b){b.style.opacity=ar==='1'?'1':'.4';b.style.background=ar==='1'?'var(--primary-50)':'var(--card)';}}syncAuto();` +
		`if(ar==='1'){setTimeout(function(){location.reload();},60000);}` +
		`window.tmToggleAuto=function(){var n=localStorage.getItem('tm-autorefresh')==='1'?'0':'1';localStorage.setItem('tm-autorefresh',n);location.reload();};` +
		`})();</script>`
	favicon := `<link rel="icon" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'%3E%3Crect width='32' height='32' rx='8' fill='%2314b8a6'/%3E%3Ctext x='16' y='23' font-size='16' font-weight='bold' fill='white' text-anchor='middle' font-family='sans-serif'%3ETM%3C/text%3E%3C/svg%3E">`
	return fmt.Sprintf(`<!doctype html><html lang="%s"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">%s<title>%s · TransitMonitor</title><style>%s</style></head>`+
		`<body><header class="top"><div class="top-row"><div class="brand"><span class="logo">TM</span>TransitMonitor</div><nav>%s</nav>%s</div></header>`+
		`<main>%s</main><footer>TransitMonitor · <a href="/api/stations">JSON API</a> · <a href="/metrics">/metrics</a> · <a href="/healthz">/healthz</a></footer>%s</body></html>`,
		lang, favicon, html.EscapeString(title), appCSS, n.String(), tools, body, js)
}

func writeHTMLShell(w http.ResponseWriter, lang, title, active, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(pageShell(lang, title, active, body)))
}

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
	cls, key := "b-muted", "badge.info"
	switch strings.ToLower(sev) {
	case "critical":
		cls, key = "b-crit", "badge.critical"
	case "warning":
		cls, key = "b-warn", "badge.warning"
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
