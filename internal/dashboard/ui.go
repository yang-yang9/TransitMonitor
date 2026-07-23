package dashboard

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// pageSize is the default rows-per-page for paginated tables. paginationCap is
// the max rows fetched for in-memory pagination (older rows beyond the cap are
// not reachable via the pager — keeps memory bounded on long-running installs).
const (
	pageSize    = 50
	paginationCap = 2000
)


const appCSS = `
:root{
  --bg1:#f8fafc;--bg2:#f0fdfa;--bg3:#f1f5f9;
  --card:#fff;--ink:#1e293b;--ink2:#334155;--muted:#64748b;--border:#e2e8f0;--row:#f6f8fa;
  --primary:#14b8a6;--primary-600:#0d9488;--primary-700:#0f766e;--primary-50:#f0fdfa;--primary-100:#ccfbf1;--primary-soft:#d3f3ee;
  --ok:#10b981;--warn:#f59e0b;--crit:#ef4444;
  --ok-soft:#f0fdf4;--warn-soft:#fef3c7;--crit-soft:#fee2e2;
  --mono:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;
  --shadow:0 1px 3px rgba(16,24,40,.06),0 4px 12px rgba(16,24,40,.04);
  --shadow-lg:0 4px 16px rgba(16,24,40,.08),0 8px 32px rgba(16,24,40,.04);
  --th-bg:#f1f5f8;--th-ink:#334155;--hdr-bg:rgba(255,255,255,.82);
  --radius:12px;--radius-sm:8px;--radius-xs:6px;
  --input-bg:#fff;--input-border:#d1d5db;--input-focus:rgba(20,184,166,.35);
  --input-ring:0 0 0 3px var(--input-focus);
}
[data-theme="dark"]{
  --bg1:#0b1220;--bg2:#0d1526;--bg3:#0b1220;--card:#111a2e;--ink:#e5e7eb;--ink2:#cbd5e1;--muted:#94a3b8;--border:#1e293b;--row:#0f1830;
  --primary:#2dd4bf;--primary-600:#14b8a6;--primary-700:#5eead4;--primary-50:#0d1f1d;--primary-100:#0c2622;--primary-soft:#10302c;
  --ok:#34d399;--warn:#fbbf24;--crit:#f87171;
  --ok-soft:#0f1f1a;--warn-soft:#1f1808;--crit-soft:#1f0e0e;
  --shadow:0 1px 3px rgba(0,0,0,.3),0 4px 12px rgba(0,0,0,.2);
  --shadow-lg:0 4px 16px rgba(0,0,0,.4),0 8px 32px rgba(0,0,0,.2);
  --th-bg:#0d1526;--th-ink:#cbd5e1;--hdr-bg:rgba(17,26,46,.82);
  --input-bg:#0f172a;--input-border:#334155;--input-focus:rgba(45,212,191,.3);
}
*{box-sizing:border-box;margin:0}
html{scroll-behavior:smooth;-webkit-font-smoothing:antialiased;-moz-osx-font-smoothing:grayscale}
body{font:14px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",system-ui,sans-serif;color:var(--ink);background:linear-gradient(135deg,var(--bg1),var(--bg2) 40%,var(--bg3));min-height:100vh;position:relative;overflow-x:hidden;transition:color .2s,background .2s}
body::before{content:"";position:fixed;inset:0;pointer-events:none;z-index:0;
  background:radial-gradient(55rem 55rem at 108% -8%,rgba(20,184,166,.08),transparent 60%),radial-gradient(45rem 45rem at -8% 108%,rgba(20,184,166,.06),transparent 60%),linear-gradient(rgba(20,184,166,.018) 1px,transparent 1px) 0 0/64px 64px,linear-gradient(90deg,rgba(20,184,166,.018) 1px,transparent 1px) 0 0/64px 64px}
[data-theme="dark"] body::before{background:radial-gradient(55rem 55rem at 108% -8%,rgba(45,212,191,.08),transparent 60%),radial-gradient(45rem 45rem at -8% 108%,rgba(45,212,191,.06),transparent 60%),linear-gradient(rgba(45,212,191,.03) 1px,transparent 1px) 0 0/64px 64px,linear-gradient(90deg,rgba(45,212,191,.03) 1px,transparent 1px) 0 0/64px 64px}
header.top,main,footer{position:relative;z-index:1}
a{color:var(--primary-700);text-decoration:none;transition:color .15s}
a:hover{text-decoration:underline}

/* ── header ── */
header.top{position:sticky;top:0;z-index:20;background:var(--hdr-bg);backdrop-filter:blur(12px);-webkit-backdrop-filter:blur(12px);border-bottom:1px solid var(--border);padding:.6rem 1.4rem;transition:background .2s,border-color .2s}
header .top-row{max-width:1200px;margin:0 auto;display:flex;align-items:center;gap:1rem}
header .brand{font-weight:700;font-size:1.08rem;display:flex;align-items:center;gap:.55rem;white-space:nowrap;color:var(--ink)}
header .brand .logo{width:32px;height:32px;border-radius:10px;background:linear-gradient(135deg,var(--primary),var(--primary-600));display:inline-flex;align-items:center;justify-content:center;color:#fff;font-size:.82rem;font-weight:800;box-shadow:0 2px 8px rgba(20,184,166,.3),inset 0 1px 0 rgba(255,255,255,.2);flex-shrink:0}
nav{display:flex;gap:.15rem;flex-wrap:wrap;margin-left:auto}
nav a{color:var(--muted);padding:.38rem .75rem;border-radius:var(--radius-sm);font-weight:500;font-size:.87rem;transition:all .15s}
nav a:hover{background:var(--primary-50);color:var(--primary-700);text-decoration:none}
nav a.active{background:var(--primary-100);color:var(--primary-700);font-weight:600}
.tools{display:flex;gap:.4rem;align-items:center;flex-shrink:0}
.icon-btn,.lang-btn{border:1px solid var(--border);background:var(--card);color:var(--muted);border-radius:var(--radius-sm);padding:.34rem .55rem;font-size:.85rem;cursor:pointer;font-weight:600;line-height:1;transition:all .15s}
.icon-btn:hover,.lang-btn:hover{background:var(--primary-50);color:var(--primary-700);border-color:var(--primary-100)}
.lang-btn{font-family:var(--mono)}
.ham{display:none;border:1px solid var(--border);background:var(--card);color:var(--muted);border-radius:var(--radius-sm);padding:.34rem .5rem;font-size:1.1rem;cursor:pointer;line-height:1;transition:all .15s}
.ham:hover{background:var(--primary-50);color:var(--primary-700)}

/* ── main ── */
main{max-width:1200px;margin:0 auto;padding:1.8rem 1.4rem 3.5rem}
h1{font-size:1.5rem;margin:0 0 .2rem;font-weight:700;letter-spacing:-.01em;color:var(--ink)}
h2{font-size:1.1rem;font-weight:600;color:var(--ink2);margin:0 0 .6rem}
.sub{color:var(--muted);margin:0 0 1.4rem;font-size:.88rem;line-height:1.6;display:flex;flex-wrap:wrap;align-items:center;gap:.5rem}
.page-hdr{margin-bottom:1.5rem}

/* ── cards ── */
.card{background:var(--card);border:1px solid var(--border);border-radius:var(--radius);padding:1.2rem 1.3rem;margin-bottom:1.1rem;box-shadow:var(--shadow);transition:box-shadow .2s,border-color .2s,transform .2s}
.card:hover{box-shadow:var(--shadow-lg)}
.card h2{margin:.1rem 0 .7rem;font-size:1.05rem}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(270px,1fr));gap:1rem}

/* ── station KPI cards ── */
.stcard{display:flex;flex-direction:column;gap:.4rem;cursor:pointer;text-decoration:none;color:inherit}
.stcard:hover{transform:translateY(-2px);border-color:var(--primary-100)}
.stcard .st-hdr{display:flex;align-items:center;justify-content:space-between;gap:.5rem}
.stcard .st-name{font-weight:600;font-size:.95rem;color:var(--ink2)}
.stcard .kpi-label{color:var(--muted);font-size:.72rem;text-transform:uppercase;letter-spacing:.06em;display:flex;align-items:center;gap:.4rem}
.stcard .kpi{font-size:2rem;font-weight:700;font-variant-numeric:tabular-nums;line-height:1;margin:.2rem 0;color:var(--ink)}
.stcard .meta{color:var(--muted);font-size:.82rem;line-height:1.6;word-break:break-all}
.gr-preview{display:flex;flex-wrap:wrap;gap:.25rem;margin:.1rem 0 .2rem}
.badge-sm{display:inline-flex;align-items:center;gap:.15rem;font-size:.7rem;padding:.1rem .35rem;border-radius:4px;background:var(--bg-2);border:1px solid var(--border);color:var(--ink2);font-variant-numeric:tabular-nums}
.badge-sm.b-cheap{color:#0a7c43;border-color:#9fe3c0;background:rgba(10,124,67,.06)}
.badge-sm.b-warn{color:#b6500a;border-color:#f3d3a6;background:rgba(182,80,10,.06)}
.badge-sm.b-ok{color:var(--ink2)}

/* group-ratio bar chart */
.gr-chart{display:flex;flex-direction:column;gap:.4rem;margin-bottom:.5rem}
.gr-chart.lg{gap:.5rem}
.gr-chart.lg .gr-row{grid-template-columns:11rem 1fr 4rem}
.gr-chart.lg .gr-name{font-size:.92rem}
.gr-chart.lg .gr-track{height:20px;border-radius:10px}
.gr-chart.lg .gr-bar{border-radius:9px}
.gr-chart.lg .gr-val{font-size:.92rem}
.gr-row{display:grid;grid-template-columns:9rem 1fr 3.5rem;align-items:center;gap:.6rem}
.gr-name{font-size:.82rem;color:var(--ink2);white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.gr-track{height:14px;background:var(--bg-2);border-radius:7px;overflow:hidden;border:1px solid var(--border)}
.gr-bar{display:block;height:100%;border-radius:6px;transition:width .3s}
.gr-bar.b-cheap{background:linear-gradient(90deg,#16a34a,#4ade80)}
.gr-bar.b-ok{background:linear-gradient(90deg,#6366f1,#818cf8)}
.gr-bar.b-warn{background:linear-gradient(90deg,#ea580c,#fb923c)}
.gr-val{font-size:.82rem;font-weight:600;font-variant-numeric:tabular-nums;text-align:right}

/* group-ratio trend sparkline grid */
.spark-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(220px,1fr));gap:.6rem}
.spark-cell{display:flex;flex-direction:column;gap:.25rem;padding:.6rem .7rem;background:var(--card);border:1px solid var(--border);border-radius:var(--radius)}
.spark-cell .sc-hdr{display:flex;justify-content:space-between;align-items:baseline;gap:.4rem}
.spark-cell .sc-name{font-weight:600;font-size:.85rem;color:var(--ink2);white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.spark-cell .sc-val{font-size:1rem;font-weight:700;font-variant-numeric:tabular-nums}
.spark-cell svg{display:block;width:100%;height:32px}
.spark-cell .sc-delta{font-size:.72rem;font-weight:600}

/* collapsible details sections */
details.sec{margin:.6rem 0;border:1px solid var(--border);border-radius:var(--radius);background:var(--card)}
details.sec>summary{cursor:pointer;padding:.6rem .8rem;font-weight:600;font-size:.9rem;color:var(--ink2);list-style:none;display:flex;align-items:center;gap:.4rem}
details.sec>summary::before{content:"▸";color:var(--muted);transition:transform .15s}
details.sec[open]>summary::before{content:"▾"}
details.sec[open]>summary{border-bottom:1px solid var(--border)}

/* group × station matrix cell */
.gcell{display:inline-block;min-width:3rem;padding:.18rem .4rem;border-radius:5px;font-variant-numeric:tabular-nums;font-weight:600;font-size:.85rem}
.gcell.p-cheap{background:rgba(22,163,74,.12);color:#0a7c43}
.gcell.p-mid{background:var(--bg-2);color:var(--ink2)}
.gcell.p-high{background:rgba(234,88,12,.12);color:#b6500a}
.gcell.p-na{color:var(--muted)}

/* ratio table visual bars + group separators */
.rat-bar{height:6px;background:var(--bg-2);border-radius:3px;overflow:hidden;margin-bottom:.2rem;min-width:80px}
.rb-fill{display:block;height:100%;background:linear-gradient(90deg,var(--primary),var(--primary-300));border-radius:3px}
tr.grp-sep td{padding:.45rem .6rem!important;background:var(--bg-1);border-bottom:2px solid var(--primary-100)}
.grp-tag{font-weight:600;font-size:.85rem;color:var(--primary)}
.muted-cell{background:var(--bg-1)}
.b-strong{font-weight:700}
.dot-s{width:9px;height:9px;border-radius:50%;display:inline-block;flex-shrink:0}
.dot-s.ok{background:var(--ok);box-shadow:0 0 6px rgba(16,185,129,.4)}.dot-s.bad{background:var(--crit);box-shadow:0 0 6px rgba(239,68,68,.4)}.dot-s.none{background:#cbd5e1}

/* ── tables ── */
.tbl-wrap{overflow-x:auto;border:1px solid var(--border);border-radius:var(--radius);background:var(--card);box-shadow:var(--shadow);-webkit-overflow-scrolling:touch}
table{width:100%;border-collapse:collapse;font-size:.88rem}
thead th{position:sticky;top:0;z-index:2;background:var(--th-bg);text-align:left;padding:.6rem .75rem;border-bottom:2px solid var(--border);white-space:nowrap;font-weight:600;color:var(--th-ink);font-size:.8rem;text-transform:uppercase;letter-spacing:.03em}
tbody td{padding:.5rem .75rem;border-bottom:1px solid var(--border);font-variant-numeric:tabular-nums;vertical-align:middle;transition:background .1s}
tbody tr:last-child td{border-bottom:0}
tbody tr{transition:background .1s}
tbody tr:hover{background:var(--row)}
.mono{font-family:var(--mono)}.num{text-align:right;white-space:nowrap}

/* ── pager ── */
.pager{display:flex;align-items:center;gap:.35rem;flex-wrap:wrap;padding:.55rem .2rem .1rem}
.pg-info{color:var(--muted);font-size:.78rem;margin-right:auto}
.pg-btn,.pg-num{display:inline-flex;align-items:center;justify-content:center;min-width:2rem;padding:.28rem .6rem;border:1px solid var(--border);border-radius:var(--radius);background:var(--card);color:var(--ink);font-size:.8rem;text-decoration:none;line-height:1.4}
.pg-btn:hover,.pg-num:hover{border-color:var(--primary);color:var(--primary)}
.pg-btn.disabled,.pg-num.disabled{opacity:.4;pointer-events:none}
.pg-num.cur{background:var(--primary);border-color:var(--primary);color:#fff;font-weight:700}
.pg-gap{color:var(--muted);padding:0 .15rem}

/* ── badges ── */
.badge{display:inline-flex;align-items:center;gap:.3rem;padding:.18rem .6rem;border-radius:999px;font-size:.72rem;font-weight:700;line-height:1.5;white-space:nowrap;letter-spacing:.01em;transition:transform .1s}
.b-ok{background:var(--ok-soft);color:var(--ok)}.b-warn{background:var(--warn-soft);color:var(--warn)}.b-crit{background:var(--crit-soft);color:var(--crit)}.b-muted{background:#eef2f5;color:var(--muted)}
[data-theme="dark"] .b-muted{background:#1e293b;color:var(--muted)}
.pcell{font-family:var(--mono);font-weight:600}
.p-cheap{color:var(--ok)}.p-high{color:var(--crit)}.p-mid{color:var(--ink)}.p-na{color:#94a3b8;font-weight:400}

/* ── tags ── */
.tag{font-family:var(--mono);font-size:.72rem;background:#eef2f5;color:#334155;padding:.1rem .4rem;border-radius:var(--radius-xs);white-space:nowrap}
[data-theme="dark"] .tag{background:#1e293b;color:#cbd5e1}
.tag-pri{background:var(--primary-50);color:var(--primary-700)}

/* ── buttons ── */
.btn{display:inline-flex;align-items:center;justify-content:center;gap:.4rem;border:none;border-radius:var(--radius-sm);padding:.45rem .95rem;font-size:.85rem;font-weight:600;cursor:pointer;transition:all .15s;text-decoration:none;background:linear-gradient(135deg,var(--primary),var(--primary-600));color:#fff;box-shadow:0 2px 8px rgba(20,184,166,.25),inset 0 1px 0 rgba(255,255,255,.15)}
.btn:hover{filter:brightness(1.08);transform:translateY(-1px);box-shadow:0 4px 12px rgba(20,184,166,.3);text-decoration:none}
.btn:active{transform:translateY(0);filter:brightness(.97)}
.btn-outline{background:transparent;color:var(--primary-700);border:1.5px solid var(--border);box-shadow:none}
.btn-outline:hover{background:var(--primary-50);border-color:var(--primary);box-shadow:none;filter:none}
.btn-danger{background:linear-gradient(135deg,var(--crit),#dc2626);box-shadow:0 2px 8px rgba(239,68,68,.2)}
.btn-danger:hover{box-shadow:0 4px 12px rgba(239,68,68,.25)}
.btn-sm{padding:.3rem .65rem;font-size:.8rem;border-radius:var(--radius-xs)}
.field-sel{display:flex;flex-wrap:wrap;align-items:center;gap:.4rem;margin:.5rem 0 1rem;padding:.6rem .8rem;background:var(--card);border:1px solid var(--border);border-radius:var(--radius)}
.field-sel .cur-field{font-weight:700;color:var(--primary)}
.btn-group{display:flex;gap:.5rem;flex-wrap:wrap;align-items:center}

/* ── forms ── */
.form-wrap{max-width:720px;margin:0 auto}
.form-grid{display:grid;grid-template-columns:repeat(2,1fr);gap:1rem}
.form-grid .full{grid-column:1/-1}
.form-sep{grid-column:1/-1;border:0;border-top:1px solid var(--border);margin:.3rem 0}
.field{display:flex;flex-direction:column;gap:.35rem}
.field-label{font-size:.78rem;font-weight:600;color:var(--muted);text-transform:uppercase;letter-spacing:.04em}
.field input,.field select{width:100%;padding:.6rem .75rem;font-size:.88rem;font-family:inherit;color:var(--ink);background:var(--input-bg);border:1.5px solid var(--input-border);border-radius:var(--radius-sm);outline:none;transition:border-color .15s,box-shadow .15s}
.field input:focus,.field select:focus{border-color:var(--primary);box-shadow:var(--input-ring)}
.field input::placeholder{color:var(--muted);opacity:.7}
.field input[readonly]{opacity:.6;cursor:not-allowed;background:var(--row)}
.field select{-webkit-appearance:none;appearance:none;background-image:url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 12 12'%3E%3Cpath fill='%2364748b' d='M2 4l4 4 4-4'/%3E%3C/svg%3E");background-repeat:no-repeat;background-position:right .75rem center;padding-right:2rem}
.toggle{position:relative;display:inline-flex;align-items:center;gap:.6rem;cursor:pointer;font-size:.88rem;color:var(--ink)}
.toggle input{position:absolute;opacity:0;width:0;height:0}
.toggle .slider{width:44px;height:24px;background:var(--input-border);border-radius:12px;position:relative;transition:background .2s;flex-shrink:0}
.toggle .slider::after{content:"";position:absolute;top:3px;left:3px;width:18px;height:18px;background:#fff;border-radius:50%;transition:transform .2s;box-shadow:0 1px 3px rgba(0,0,0,.15)}
.toggle input:checked+.slider{background:var(--primary)}
.toggle input:checked+.slider::after{transform:translateX(20px)}
.toggle input:focus-visible+.slider{box-shadow:var(--input-ring)}

/* ── misc ── */
.kvs{display:flex;flex-wrap:wrap;gap:.5rem;font-size:.85rem}
.kvs b{color:var(--muted);font-weight:500}
.empty{color:var(--muted);padding:2rem;text-align:center;font-size:.9rem}

/* ── footer ── */
footer{color:var(--muted);font-size:.78rem;text-align:center;padding:2rem 1.4rem 1.5rem;border-top:1px solid var(--border);margin-top:2rem}
footer a{color:var(--muted);transition:color .15s}
footer a:hover{color:var(--primary-700)}
.footer-links{display:flex;justify-content:center;gap:.3rem 1.2rem;flex-wrap:wrap}

/* ── focus-visible ── */
:focus-visible{outline:2px solid var(--primary);outline-offset:2px}
button:focus-visible{outline:2px solid var(--primary);outline-offset:2px}

/* ── scrollbar ── */
::-webkit-scrollbar{width:6px;height:6px}
::-webkit-scrollbar-track{background:transparent}
::-webkit-scrollbar-thumb{background:var(--border);border-radius:3px}
::-webkit-scrollbar-thumb:hover{background:var(--muted)}

/* ── responsive ── */
@media(max-width:1024px){main{max-width:100%}}
@media(max-width:768px){
  header.top{padding:.5rem 1rem}
  .ham{display:block}
  nav{display:none;position:absolute;top:100%;left:0;right:0;background:var(--card);border-bottom:1px solid var(--border);padding:.5rem;flex-direction:column;box-shadow:var(--shadow-lg);z-index:30}
  nav.open{display:flex}
  nav a{padding:.55rem .9rem;border-radius:var(--radius-xs)}
  main{padding:1.2rem 1rem 2.5rem}
  .grid{grid-template-columns:1fr}
  .form-grid{grid-template-columns:1fr}
}
@media(max-width:480px){
  header.top{padding:.45rem .7rem}
  main{padding:1rem .7rem 2rem}
  h1{font-size:1.25rem}
  .btn-group{flex-direction:column}
  .btn-group .btn{width:100%;justify-content:center}
}
`

type navItem struct{ H, Label, Key string }

var navItems = []navItem{
	{"/", "", "overview"},
	{"/matrix", "", "matrix"},
	{"/changes", "", "changes"},
	{"/probes", "", "probes"},
	{"/audit", "", "audit"},
	{"/alerts", "", "alerts"},
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
		`window.tmHam=function(){document.getElementById('tm-nav').classList.toggle('open');};` +
		`document.addEventListener('click',function(e){var n=document.getElementById('tm-nav');if(n&&n.classList.contains('open')&&!e.target.closest('nav')&&!e.target.closest('.ham'))n.classList.remove('open');});` +
		`})();</script>`
	favicon := `<link rel="icon" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'%3E%3Crect width='32' height='32' rx='8' fill='%2314b8a6'/%3E%3Ctext x='16' y='23' font-size='16' font-weight='bold' fill='white' text-anchor='middle' font-family='sans-serif'%3ETM%3C/text%3E%3C/svg%3E">`
	return fmt.Sprintf(`<!doctype html><html lang="%s"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">%s<title>%s · TransitMonitor</title><style>%s</style></head>`+
		`<body><header class="top"><div class="top-row">`+
		`<div class="brand"><span class="logo">TM</span>TransitMonitor</div>`+
		`<button class="ham" onclick="tmHam()" aria-label="menu">☰</button>`+
		`<nav id="tm-nav">%s</nav>%s</div></header>`+
		`<main>%s</main>`+
		`<footer><div class="footer-links">`+
		`<span>TransitMonitor</span>`+
		`<a href="/api/stations">JSON API</a>`+
		`<a href="/metrics">/metrics</a>`+
		`<a href="/healthz">/healthz</a>`+
		`</div></footer>%s</body></html>`,
		lang, favicon, html.EscapeString(title), appCSS, n.String(), tools, body, js)
}

func writeHTMLShell(w http.ResponseWriter, lang, title, active, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
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

// renderRatioTable renders the model-ratio table, grouping rows by the first cell
// (group name) with a full-width separator row carrying the group + its ratio.
func renderRatioTable(cols []string, rows [][]string) string {
	var b strings.Builder
	b.WriteString(`<div class="tbl-wrap"><table><thead><tr>`)
	for _, c := range cols {
		b.WriteString("<th>")
		b.WriteString(html.EscapeString(c))
		b.WriteString("</th>")
	}
	b.WriteString("</tr></thead><tbody>")
	prev := ""
	for _, row := range rows {
		grp := row[0]
		if grp != prev {
			b.WriteString(`<tr class="grp-sep"><td colspan="` + fmt.Sprint(len(cols)) + `">` +
				`<span class="grp-tag">` + grp + `</span></td></tr>`)
			prev = grp
		}
		b.WriteString(`<tr>`)
		// first cell is the group name — suppress repeating it (shown in separator)
		b.WriteString(`<td class="muted-cell"></td>`)
		for _, cell := range row[1:] {
			b.WriteString("<td>")
			b.WriteString(cell)
			b.WriteString("</td>")
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table></div>")
	return b.String()
}

// paginateRows slices rows for the current page (1-based, read from query key
// pageKey) and returns the page slice + a rendered pager. q is the full query;
// its other params are preserved across page links. total = len(rows).
func paginateRows(lang, base, pageKey string, q url.Values, rows [][]string) ([][]string, string) {
	total := len(rows)
	pages := (total + pageSize - 1) / pageSize
	if pages < 1 {
		pages = 1
	}
	page := atoiDefault(q.Get(pageKey), 1)
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return rows[start:end], pager(lang, base, pageKey, q, page, pages, total)
}

// pager renders a prev/next + windowed page-number pager. All query params
// from q are preserved on every link except pageKey (set per-link).
func pager(lang, base, pageKey string, q url.Values, page, pages, total int) string {
	clone := url.Values{}
	for k, vs := range q {
		clone[k] = vs
	}
	clone.Del(pageKey)
	qs := clone.Encode()
	link := func(n int) string {
		if qs == "" {
			return fmt.Sprintf("%s?%s=%d", base, pageKey, n)
		}
		return fmt.Sprintf("%s?%s&%s=%d", base, qs, pageKey, n)
	}
	var b strings.Builder
	b.WriteString(`<div class="pager"><span class="pg-info">`)
	b.WriteString(fmt.Sprintf(t(lang, "pager.info"), total, page, pages))
	b.WriteString(`</span>`)
	if pages <= 1 {
		b.WriteString(`</div>`)
		return b.String()
	}
	if page > 1 {
		b.WriteString(`<a class="pg-btn" href="` + link(page-1) + `">` + t(lang, "pager.prev") + `</a>`)
	} else {
		b.WriteString(`<span class="pg-btn disabled">` + t(lang, "pager.prev") + `</span>`)
	}
	for _, n := range pageWindow(page, pages) {
		if n == 0 {
			b.WriteString(`<span class="pg-gap">…</span>`)
			continue
		}
		if n == page {
			b.WriteString(`<span class="pg-num cur">` + strconv.Itoa(n) + `</span>`)
		} else {
			b.WriteString(fmt.Sprintf(`<a class="pg-num" href="%s">%d</a>`, link(n), n))
		}
	}
	if page < pages {
		b.WriteString(`<a class="pg-btn" href="` + link(page+1) + `">` + t(lang, "pager.next") + `</a>`)
	} else {
		b.WriteString(`<span class="pg-btn disabled">` + t(lang, "pager.next") + `</span>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// pageWindow returns page-number slots to show; 0 marks an ellipsis gap.
// Always includes first and last, plus a ±2 window around the current page.
func pageWindow(page, pages int) []int {
	if pages <= 7 {
		out := make([]int, 0, pages)
		for i := 1; i <= pages; i++ {
			out = append(out, i)
		}
		return out
	}
	out := []int{1}
	lo := page - 2
	hi := page + 2
	if lo < 2 {
		lo = 2
	}
	if hi > pages-1 {
		hi = pages - 1
	}
	if lo > 2 {
		out = append(out, 0)
	}
	for i := lo; i <= hi; i++ {
		out = append(out, i)
	}
	if hi < pages-1 {
		out = append(out, 0)
	}
	out = append(out, pages)
	return out
}

func atoiDefault(s string, d int) int {
	if s == "" {
		return d
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return d
	}
	return n
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

// groupRatioChart renders the horizontal bar chart of group ratios, sorted
// cheapest-first. lg=true renders the larger hero variant (used on the station
// detail page); false the compact variant (overview cards). Returns "" when
// the map is empty.
func groupRatioChart(grs map[string]float64, lg bool) string {
	if len(grs) == 0 {
		return ""
	}
	type gr struct {
		name string
		v    float64
	}
	grp := make([]gr, 0, len(grs))
	maxV := 0.0
	for k, v := range grs {
		grp = append(grp, gr{k, v})
		if v > maxV {
			maxV = v
		}
	}
	sort.Slice(grp, func(i, j int) bool { return grp[i].v < grp[j].v })
	cls := ""
	if lg {
		cls = " lg"
	}
	var b strings.Builder
	b.WriteString(`<div class="gr-chart` + cls + `">`)
	for _, g := range grp {
		pct := 0.0
		if maxV > 0 {
			pct = g.v / maxV * 100
		}
		bc := "b-ok"
		if g.v < 0.5 {
			bc = "b-cheap"
		} else if g.v > 1.0 {
			bc = "b-warn"
		}
		b.WriteString(fmt.Sprintf(
			`<div class="gr-row"><span class="gr-name" title="%s">%s</span>`+
				`<div class="gr-track"><span class="gr-bar %s" style="width:%.1f%%"></span></div>`+
				`<span class="gr-val">%.2fx</span></div>`,
			esc(g.name), esc(g.name), bc, pct, g.v))
	}
	b.WriteString(`</div>`)
	return b.String()
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
