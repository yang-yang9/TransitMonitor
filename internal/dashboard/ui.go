package dashboard

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"transitmonitor/internal/domain"
)

// pageSize is the default rows-per-page for paginated tables. paginationCap is
// the max rows fetched for in-memory pagination (older rows beyond the cap are
// not reachable via the pager — keeps memory bounded on long-running installs).
const (
	pageSize      = 50
	paginationCap = 2000
)

const appCSS = `
:root{
  --bg1:#f8fafc;--bg2:#f0fdfa;--bg3:#f1f5f9;--bg-1:#f1f5f9;--bg-2:#e8eef4;
  --card:#fff;--ink:#1e293b;--ink2:#334155;--muted:#64748b;--border:#e2e8f0;--row:rgba(20,184,166,.03);
  --primary:#14b8a6;--primary-300:#5eead4;--primary-600:#0d9488;--primary-700:#0f766e;--primary-50:#f0fdfa;--primary-100:#ccfbf1;--primary-soft:#d3f3ee;
  --ok:#10b981;--warn:#f59e0b;--crit:#ef4444;
  --ok-soft:#ecfdf5;--warn-soft:#fffbeb;--crit-soft:#fef2f2;
  --mono:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;
  --shadow:0 1px 3px rgba(16,24,40,.08),0 4px 16px rgba(16,24,40,.04);
  --shadow-lg:0 8px 30px rgba(16,24,40,.1),0 4px 12px rgba(16,24,40,.06);
  --th-bg:linear-gradient(180deg,#f1f5f9,#e9eef4);--th-ink:#334155;--hdr-bg:rgba(255,255,255,.88);
  --radius:14px;--radius-sm:10px;--radius-xs:6px;
  --input-bg:#fff;--input-border:#d1d5db;--input-focus:rgba(20,184,166,.35);
  --input-ring:0 0 0 3px var(--input-focus);
  --accent-bar:var(--primary);
}
[data-theme="dark"]{
  --bg1:#080e1a;--bg2:#0a1225;--bg3:#080e1a;--bg-1:#0d1526;--bg-2:#131d35;
  --card:#0f1729;--ink:#e5e7eb;--ink2:#cbd5e1;--muted:#94a3b8;--border:#1c2842;--row:rgba(45,212,191,.04);
  --primary:#2dd4bf;--primary-300:#5eead4;--primary-600:#14b8a6;--primary-700:#5eead4;--primary-50:#0d1f1d;--primary-100:#0c2622;--primary-soft:#10302c;
  --ok:#34d399;--warn:#fbbf24;--crit:#f87171;
  --ok-soft:rgba(16,185,129,.12);--warn-soft:rgba(251,191,36,.1);--crit-soft:rgba(248,113,113,.1);
  --shadow:0 1px 4px rgba(0,0,0,.4),0 6px 20px rgba(0,0,0,.25);
  --shadow-lg:0 8px 32px rgba(0,0,0,.5),0 4px 14px rgba(0,0,0,.3);
  --th-bg:linear-gradient(180deg,#111b30,#0d1526);--th-ink:#cbd5e1;--hdr-bg:rgba(12,18,37,.92);
  --input-bg:#0c1425;--input-border:#243050;--input-focus:rgba(45,212,191,.3);
  --accent-bar:#2dd4bf;
}
@keyframes logo-glow{0%,100%{box-shadow:0 2px 8px rgba(20,184,166,.3),inset 0 1px 0 rgba(255,255,255,.2)}50%{box-shadow:0 2px 16px rgba(20,184,166,.5),inset 0 1px 0 rgba(255,255,255,.2)}}
*{box-sizing:border-box;margin:0}
html{scroll-behavior:smooth;-webkit-font-smoothing:antialiased;-moz-osx-font-smoothing:grayscale}
body{font:14px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",system-ui,sans-serif;color:var(--ink);background:var(--bg1);min-height:100vh;position:relative;overflow-x:hidden;transition:color .25s,background .25s}
body::before{content:"";position:fixed;inset:0;pointer-events:none;z-index:0;
  background:radial-gradient(60rem 60rem at 100% -12%,rgba(20,184,166,.10),transparent 55%),radial-gradient(50rem 50rem at -5% 105%,rgba(20,184,166,.07),transparent 55%)}
[data-theme="dark"] body::before{background:radial-gradient(60rem 60rem at 100% -12%,rgba(45,212,191,.08),transparent 55%),radial-gradient(50rem 50rem at -5% 105%,rgba(45,212,191,.05),transparent 55%)}
header.top,main,footer{position:relative;z-index:1}
a{color:var(--primary-700);text-decoration:none;transition:color .15s}
a:hover{text-decoration:underline}

/* ── header ── */
header.top{position:sticky;top:0;z-index:20;background:linear-gradient(135deg,#0f172a 0%,#1e293b 100%);padding:0;transition:background .25s;box-shadow:0 2px 12px rgba(0,0,0,.15)}
[data-theme="dark"] header.top{background:linear-gradient(135deg,#060b16 0%,#0d1526 100%);box-shadow:0 2px 16px rgba(0,0,0,.4)}
header .top-row{max-width:100%;margin:0;display:flex;align-items:stretch;min-height:52px}
header .brand{font-weight:800;font-size:1.08rem;display:flex;align-items:center;gap:.6rem;white-space:nowrap;color:#fff;letter-spacing:.03em;padding:0 1.2rem;background:rgba(0,0,0,.15);border-right:1px solid rgba(255,255,255,.08);flex-shrink:0}
header .brand .logo{width:30px;height:30px;border-radius:8px;background:linear-gradient(135deg,var(--primary),var(--primary-600));display:inline-flex;align-items:center;justify-content:center;color:#fff;font-size:.75rem;font-weight:800;animation:logo-glow 3s ease-in-out infinite;flex-shrink:0}
nav{display:flex;gap:0;margin:0;padding:0 .5rem;align-items:stretch}
nav a{color:rgba(255,255,255,.6);padding:0 .9rem;font-weight:500;font-size:.85rem;transition:all .15s;position:relative;display:inline-flex;align-items:center;border-bottom:2px solid transparent}
nav a:hover{color:rgba(255,255,255,.95);background:rgba(255,255,255,.06);text-decoration:none}
nav a.active{color:#fff;font-weight:600;background:rgba(255,255,255,.08);border-bottom:2px solid var(--primary)}
.tools{display:flex;gap:.3rem;align-items:center;flex-shrink:0;margin-left:auto;padding:0 1rem}
.icon-btn,.lang-btn{border:1px solid rgba(255,255,255,.12);background:rgba(255,255,255,.06);color:rgba(255,255,255,.65);border-radius:var(--radius-xs);padding:.3rem .48rem;font-size:.8rem;cursor:pointer;font-weight:600;line-height:1;transition:all .15s}
.icon-btn:hover,.lang-btn:hover{background:rgba(255,255,255,.12);color:#fff;border-color:rgba(255,255,255,.2)}
.lang-btn{font-family:var(--mono)}
.auto-btn{display:inline-flex;align-items:center;gap:.35rem;border:1px solid rgba(255,255,255,.12);background:rgba(255,255,255,.06);color:rgba(255,255,255,.5);border-radius:var(--radius-xs);padding:.3rem .6rem;font-size:.8rem;cursor:pointer;font-weight:600;line-height:1;transition:all .15s}
.auto-btn:hover{background:rgba(255,255,255,.12);color:#fff;border-color:rgba(255,255,255,.2)}
.auto-btn.on{background:rgba(20,184,166,.2);color:var(--primary-300);border-color:rgba(20,184,166,.4)}
.auto-btn.on:hover{background:rgba(20,184,166,.3)}
.ham{display:none;border:1px solid rgba(255,255,255,.12);background:rgba(255,255,255,.06);color:rgba(255,255,255,.65);border-radius:var(--radius-xs);padding:.3rem .48rem;font-size:1.05rem;cursor:pointer;line-height:1;transition:all .15s}
.ham:hover{background:rgba(255,255,255,.12);color:#fff}

/* ── main ── */
main{max-width:1200px;margin:0 auto;padding:2rem 1.4rem 4rem}
/* widen the canvas only on the overview page so more station cards fit per
   row; other pages (matrix/audit/stations) share main and keep 1200px so
   their table line lengths stay readable. */
body.page-overview main{max-width:1600px}
h1{font-size:1.65rem;margin:0 0 .25rem;font-weight:800;letter-spacing:-.02em;color:var(--ink)}
.tip-wrap{position:relative;display:inline-flex;align-items:center;vertical-align:middle}
.tip-dot{width:18px;height:18px;border-radius:50%;background:var(--bg-2);border:1.5px solid var(--border);color:var(--muted);font-size:.7rem;font-weight:700;display:inline-flex;align-items:center;justify-content:center;cursor:help;transition:all .15s;line-height:1;font-family:var(--mono)}
.tip-dot:hover{background:var(--primary-50);border-color:var(--primary);color:var(--primary)}
.tip-pop{display:none;position:fixed;min-width:320px;max-width:420px;padding:.7rem .9rem;background:var(--card);border:1.5px solid var(--border);border-radius:var(--radius-sm);box-shadow:var(--shadow-lg);font-size:.8rem;font-weight:400;line-height:1.7;color:var(--ink2);white-space:normal;z-index:9999}
.tip-pop.show{display:block}
h2{font-size:1.05rem;font-weight:700;color:var(--ink2);margin:1.2rem 0 .7rem;padding-left:.65rem;border-left:3px solid var(--primary);line-height:1.3}
.sub{color:var(--muted);margin:0 0 1.5rem;font-size:.88rem;line-height:1.7;display:flex;flex-wrap:wrap;align-items:center;gap:.5rem}
.page-hdr{margin-bottom:1.8rem}

/* ── cards ── */
.card{background:var(--card);border:1px solid var(--border);border-left:3px solid var(--accent-bar);border-radius:var(--radius);padding:1.3rem 1.4rem;margin-bottom:1.2rem;box-shadow:var(--shadow);transition:all .25s cubic-bezier(.4,0,.2,1);overflow:visible}
.card:hover{box-shadow:var(--shadow-lg);border-left-color:var(--primary-600)}
[data-theme="dark"] .card{background:linear-gradient(180deg,var(--card),rgba(15,23,42,.6))}
.card h2{margin:.1rem 0 .7rem;font-size:1.05rem;border:none;padding-left:0}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(300px,1fr));gap:1.1rem}
/* compact overview mode (toggle on): narrower cards + pill ratios → ~5-6/row
   on 1440px, ~6-7/row on 1920px. 230px (not 210px) keeps the 768-1024px tablet
   band from rendering 3-4 cramped columns; the 768px single-col fallback still
   applies. minmax(300px) above is the detailed-bars default. */
.grid.grid-compact{grid-template-columns:repeat(auto-fill,minmax(230px,1fr));gap:.8rem}
.grid.grid-compact .stcard{padding:.85rem .9rem;gap:.3rem}
.grid.grid-compact .stcard .st-name{font-size:.92rem}
.grid.grid-compact .stcard .meta:not(.change-hints){display:none}
.grid.grid-compact .change-hints{font-size:.68rem;gap:.1rem;line-height:1.2;margin-top:.15rem}
.grid.grid-compact .change-hints .badge{font-size:.62rem;padding:.05rem .3rem}
.grid.grid-compact .ch-item{gap:0}
.grid.grid-compact .ch-val{font-size:.66rem}
.grid.grid-compact .ch-ts{font-size:.58rem;margin-left:.25rem}

/* ── station KPI cards ── */
.stcard{display:flex;flex-direction:column;gap:.5rem;cursor:pointer;text-decoration:none;color:inherit;border-left-color:var(--ok)}
.stcard:hover{transform:translateY(-3px);border-color:var(--primary);border-left-color:var(--primary);text-decoration:none}
.stcard .st-hdr{display:flex;align-items:center;justify-content:space-between;gap:.5rem}
.stcard .st-name{font-weight:700;font-size:1rem;color:var(--ink)}
.stcard .kpi-label{color:var(--muted);font-size:.72rem;text-transform:uppercase;letter-spacing:.06em;display:flex;align-items:center;gap:.4rem}
.stcard .kpi{font-size:2.2rem;font-weight:800;font-variant-numeric:tabular-nums;line-height:1;margin:.25rem 0;color:var(--ink);letter-spacing:-.02em}
.stcard .meta{color:var(--muted);font-size:.82rem;line-height:1.7;word-break:break-all}
.change-hints{display:flex;flex-direction:column;gap:.2rem;font-size:.78rem;line-height:1.3}
.ch-item{display:flex;align-items:baseline;gap:0}
.ch-name{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--ink2)}
.ch-val{white-space:nowrap;font-variant-numeric:tabular-nums;font-family:var(--mono);font-size:.75rem}
.ch-old{color:var(--muted);opacity:.65}
.ch-ts{font-size:.65rem;color:var(--muted);opacity:.55;white-space:nowrap;margin-left:.4rem;min-width:2rem;text-align:right}
.gr-preview{display:flex;flex-wrap:wrap;gap:.2rem .25rem;margin:.1rem 0 .2rem}
.badge-sm{display:inline-flex;align-items:center;gap:.15rem;font-size:.68rem;padding:.1rem .38rem;border-radius:5px;background:var(--bg-2);border:1px solid var(--border);color:var(--ink2);font-variant-numeric:tabular-nums;font-family:var(--mono);max-width:100%}
.badge-sm .pn{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;max-width:7em}
.badge-sm.b-cheap{color:#059669;border-color:#6ee7b7;background:rgba(5,150,105,.08)}
.badge-sm.b-warn{color:#d97706;border-color:#fcd34d;background:rgba(217,119,6,.08)}
.badge-sm.b-ok{color:var(--ink2)}
[data-theme="dark"] .badge-sm{background:rgba(255,255,255,.04);border-color:var(--border)}
[data-theme="dark"] .badge-sm.b-cheap{color:#34d399;border-color:rgba(52,211,153,.3);background:rgba(52,211,153,.08)}
[data-theme="dark"] .badge-sm.b-warn{color:#fbbf24;border-color:rgba(251,191,36,.3);background:rgba(251,191,36,.08)}

/* group-ratio bar chart */
.gr-chart{display:flex;flex-direction:column;gap:.45rem;margin-bottom:.6rem}
.gr-chart.lg{gap:.55rem}
.gr-chart.lg .gr-row{grid-template-columns:11rem 1fr 6rem}
.gr-chart.lg .gr-name{font-size:.92rem}
.gr-chart.lg .gr-track{height:22px;border-radius:11px}
.gr-chart.lg .gr-bar{border-radius:10px}
.gr-chart.lg .gr-val{font-size:.95rem}
.gr-row{display:grid;grid-template-columns:9rem 1fr 5rem;align-items:center;gap:.7rem}
.gr-name{font-size:.82rem;color:var(--ink2);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;font-weight:500}
.gr-track{height:16px;background:var(--bg-2);border-radius:8px;overflow:hidden;border:1px solid var(--border)}
.gr-bar{display:block;height:100%;border-radius:7px;transition:width .4s cubic-bezier(.4,0,.2,1)}
.gr-bar.b-cheap{background:linear-gradient(90deg,#059669,#34d399);box-shadow:inset 0 1px 0 rgba(255,255,255,.2)}
.gr-bar.b-ok{background:linear-gradient(90deg,#4f46e5,#818cf8);box-shadow:inset 0 1px 0 rgba(255,255,255,.2)}
.gr-bar.b-warn{background:linear-gradient(90deg,#d97706,#fbbf24);box-shadow:inset 0 1px 0 rgba(255,255,255,.2)}
.gr-val{font-size:.85rem;font-weight:700;font-variant-numeric:tabular-nums;text-align:right;font-family:var(--mono)}
.gr-ovr{display:inline-block;margin-left:.2rem;font-size:.7rem;color:var(--muted);cursor:help;vertical-align:baseline}
.gr-hidden{margin:.4rem 0 0;border:1px dashed var(--border);border-radius:var(--radius-xs);background:var(--bg-1)}
.gr-hidden>summary{cursor:pointer;padding:.35rem .6rem;font-size:.78rem;color:var(--muted);list-style:none}
.gr-hidden>summary::before{content:"▸ ";color:var(--muted)}
.gr-hidden[open]>summary::before{content:"▾ "}
.gr-dim{opacity:.5}
.gr-dim .gr-track{background:transparent}

/* group-ratio trend sparkline grid */
.spark-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(220px,1fr));gap:.7rem}
.spark-cell{display:flex;flex-direction:column;gap:.25rem;padding:.7rem .8rem;background:var(--card);border:1px solid var(--border);border-radius:var(--radius);transition:all .2s;box-shadow:var(--shadow)}
.spark-cell:hover{border-color:var(--primary-100);box-shadow:var(--shadow-lg)}
[data-theme="dark"] .spark-cell{background:linear-gradient(180deg,var(--card),rgba(15,23,42,.5))}
.spark-cell .sc-hdr{display:flex;justify-content:space-between;align-items:baseline;gap:.4rem}
.spark-cell .sc-name{font-weight:600;font-size:.85rem;color:var(--ink2);white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.spark-cell .sc-val{font-size:1.1rem;font-weight:800;font-variant-numeric:tabular-nums;font-family:var(--mono);text-align:right;min-width:5ch}
.spark-cell svg{display:block;width:100%;height:36px}
.spark-wrap{position:relative;line-height:0;height:36px}
.spark-wrap svg{display:block;width:100%;height:100%}
.spark-dots{position:absolute;top:0;left:0;right:0;bottom:0;display:grid}
.spark-dot{cursor:crosshair;min-width:0}
.spark-dot:hover{background:rgba(20,184,166,.15)}
#tm-tip{position:fixed;background:#1e293b;color:#fff;font-size:.75rem;font-family:var(--mono);padding:.3rem .6rem;border-radius:var(--radius-xs);white-space:nowrap;pointer-events:none;opacity:0;transition:opacity .1s;z-index:9999;box-shadow:0 4px 14px rgba(0,0,0,.35);transform:translate(-50%,-100%);margin-top:-8px}
#tm-tip.show{opacity:1}
[data-theme="dark"] #tm-tip{background:#334155;box-shadow:0 4px 14px rgba(0,0,0,.6)}
.spark-cell .sc-delta{font-size:.72rem;font-weight:700}

/* collapsible details sections */
details.sec{margin:.7rem 0;border:1px solid var(--border);border-left:3px solid var(--primary);border-radius:var(--radius);background:var(--card);box-shadow:var(--shadow)}
[data-theme="dark"] details.sec{background:linear-gradient(180deg,var(--card),rgba(15,23,42,.5))}
details.sec>summary{cursor:pointer;padding:.7rem .9rem;font-weight:600;font-size:.92rem;color:var(--ink2);list-style:none;display:flex;align-items:center;gap:.5rem;transition:color .15s}
details.sec>summary:hover{color:var(--primary)}
details.sec>summary::before{content:"▸";color:var(--muted);transition:transform .15s}
details.sec[open]>summary::before{content:"▾";color:var(--primary)}
details.sec[open]>summary{border-bottom:1px solid var(--border)}

/* group × station matrix cell */
.gcell{display:inline-block;min-width:5rem;padding:.22rem .5rem;border-radius:var(--radius-xs);font-variant-numeric:tabular-nums;font-weight:700;font-size:.85rem;font-family:var(--mono);text-align:center}
.gcell.p-cheap{background:rgba(5,150,105,.12);color:#059669}
.gcell.p-mid{background:var(--bg-2);color:var(--ink2)}
.gcell.p-high{background:rgba(217,119,6,.12);color:#d97706}
.gcell.p-na{color:var(--muted);font-weight:400}
[data-theme="dark"] .gcell.p-cheap{background:rgba(52,211,153,.12);color:#34d399}
[data-theme="dark"] .gcell.p-mid{background:rgba(255,255,255,.04);color:var(--ink2)}
[data-theme="dark"] .gcell.p-high{background:rgba(251,191,36,.12);color:#fbbf24}
.gstar{font-size:.7rem;color:var(--primary);margin-left:.15rem}
.pin-btn{background:none;border:none;cursor:pointer;font-size:1rem;opacity:.3;transition:opacity .15s;padding:.1rem .3rem;line-height:1}
.pin-btn:hover{opacity:.7}
.pin-btn.pinned{opacity:1}
.gr-row.gr-pinned{background:linear-gradient(90deg,rgba(20,184,166,.14),transparent 70%);border-left:3px solid var(--primary);padding-left:.5rem;margin-left:-.5rem;border-radius:0 4px 4px 0}
.gr-row.gr-pinned .gr-name{font-weight:800;color:var(--primary-700)}
.gr-row.gr-pinned .gr-val{color:var(--primary);font-weight:800;font-size:1rem}
.gr-row.gr-pinned .gr-bar{box-shadow:0 0 8px rgba(20,184,166,.5)}
.badge-sm.pill-pinned{border:1.5px solid var(--primary)!important;background:linear-gradient(135deg,rgba(20,184,166,.18),rgba(20,184,166,.06))!important;box-shadow:0 0 6px rgba(20,184,166,.35);font-weight:800}
.badge-sm.pill-pinned .pn{font-weight:800;color:var(--primary-700)}

/* ratio table visual bars + group separators */
.rat-bar{height:7px;background:var(--bg-2);border-radius:4px;overflow:hidden;margin-bottom:.2rem;min-width:80px}
.rb-fill{display:block;height:100%;background:linear-gradient(90deg,var(--primary),var(--primary-300));border-radius:4px;box-shadow:0 0 6px rgba(20,184,166,.25)}
tr.grp-sep td{padding:.5rem .7rem!important;background:var(--bg-1);border-bottom:2px solid var(--primary-100);font-weight:600}
.grp-tag{font-weight:700;font-size:.88rem;color:var(--primary)}
tr.ratio-row{background:var(--primary-50);border-left:3px solid var(--primary)}
tr.ratio-row td{font-weight:600}
[data-theme="dark"] tr.ratio-row{background:rgba(20,184,166,.08)}
.change-batch{margin:.5rem 0;border-left:3px solid var(--primary);border-radius:var(--radius);padding-left:0}
.change-batch>.tbl-wrap,.change-batch>details{border-left:0;margin:0}
.change-batch>.tbl-wrap{border-radius:var(--radius) var(--radius) 0 0}
.change-batch>details{border-radius:0 0 var(--radius) var(--radius);border-top:0}
.change-batch>.tbl-wrap:last-child{border-radius:var(--radius)}
.change-batch>details:first-child{border-radius:var(--radius)}
tr.batch-sep td{height:6px;padding:0!important;border-bottom:none;background:transparent;position:relative}
tr.batch-sep td::after{content:"";position:absolute;left:4%;right:4%;top:50%;border-top:1px dashed var(--border)}
tr.batch-toggle-row td{padding:.35rem .8rem!important;border-bottom:1px solid var(--border);background:var(--bg-1)}
.batch-toggle{font-size:.78rem;color:var(--primary);font-weight:600;cursor:pointer;text-decoration:none}
.muted-cell{background:var(--bg-1)}
.b-strong{font-weight:700}
.dot-s{width:10px;height:10px;border-radius:50%;display:inline-block;flex-shrink:0}
.dot-s.ok{background:var(--ok);box-shadow:0 0 8px rgba(16,185,129,.45)}.dot-s.bad{background:var(--crit);box-shadow:0 0 8px rgba(239,68,68,.45)}.dot-s.none{background:#cbd5e1}

/* ── tables ── */
.tbl-wrap{overflow-x:auto;overflow-y:visible;border:1px solid var(--border);border-radius:var(--radius);background:var(--card);box-shadow:var(--shadow);-webkit-overflow-scrolling:touch;position:relative}
[data-theme="dark"] .tbl-wrap{background:linear-gradient(180deg,var(--card),rgba(11,18,32,.7))}
table{width:100%;border-collapse:collapse;font-size:.86rem}
thead th{position:sticky;top:0;z-index:2;background:var(--th-bg);text-align:left;padding:.65rem .8rem;border-bottom:2px solid var(--border);white-space:nowrap;font-weight:700;color:var(--th-ink);font-size:.74rem;text-transform:uppercase;letter-spacing:.05em}
thead th:first-child{border-left:3px solid var(--primary);border-radius:0}
tbody td{padding:.55rem .8rem;border-bottom:1px solid var(--border);font-variant-numeric:tabular-nums;vertical-align:middle;transition:background .1s;overflow:visible;position:relative}
tbody tr:nth-child(even){background:rgba(0,0,0,.015)}
[data-theme="dark"] tbody tr:nth-child(even){background:rgba(255,255,255,.015)}
tbody tr:last-child td{border-bottom:0}
tbody tr{transition:background .1s}
tbody tr:hover{background:var(--row)}
.mono{font-family:var(--mono)}.num{display:inline-block;text-align:right;white-space:nowrap;min-width:4ch}
.cell-grp{display:block;font-size:.68rem;line-height:1.1;color:var(--muted);font-weight:400;font-family:var(--mono);margin-top:.05rem}

/* ── pager ── */
.pager{display:flex;align-items:center;gap:.4rem;flex-wrap:wrap;padding:.7rem .3rem .2rem}
.pg-info{color:var(--muted);font-size:.78rem;margin-right:auto}
.pg-btn,.pg-num{display:inline-flex;align-items:center;justify-content:center;min-width:2.1rem;padding:.3rem .65rem;border:1px solid var(--border);border-radius:var(--radius-sm);background:var(--card);color:var(--ink);font-size:.8rem;text-decoration:none;line-height:1.4;transition:all .15s}
.pg-btn:hover,.pg-num:hover{border-color:var(--primary);color:var(--primary);text-decoration:none}
.pg-btn.disabled,.pg-num.disabled{opacity:.35;pointer-events:none}
.pg-num.cur{background:linear-gradient(135deg,var(--primary),var(--primary-600));border-color:var(--primary);color:#fff;font-weight:700;box-shadow:0 2px 8px rgba(20,184,166,.3)}
.pg-gap{color:var(--muted);padding:0 .15rem}

/* ── badges ── */
.badge{display:inline-flex;align-items:center;gap:.3rem;padding:.2rem .65rem;border-radius:999px;font-size:.72rem;font-weight:700;line-height:1.5;white-space:nowrap;letter-spacing:.01em;transition:all .15s}
.badge:hover{transform:scale(1.05)}
.b-ok{background:var(--ok-soft);color:var(--ok)}.b-warn{background:var(--warn-soft);color:var(--warn)}.b-crit{background:var(--crit-soft);color:var(--crit)}.b-muted{background:#eef2f5;color:var(--muted)}
[data-theme="dark"] .b-muted{background:#1e293b;color:var(--muted)}
.pcell{display:inline-block;text-align:right;min-width:4ch;font-family:var(--mono);font-weight:700}
.p-cheap{color:var(--ok)}.p-high{color:var(--crit)}.p-mid{color:var(--ink)}.p-na{color:#94a3b8;font-weight:400}

/* ── tags ── */
.tag{font-family:var(--mono);font-size:.72rem;background:#eef2f5;color:#334155;padding:.12rem .45rem;border-radius:var(--radius-xs);white-space:nowrap;font-weight:500}
[data-theme="dark"] .tag{background:rgba(255,255,255,.06);color:#cbd5e1;border:1px solid var(--border)}
.tag-pri{background:var(--primary-50);color:var(--primary-700);border:1px solid var(--primary-100)}

/* ── buttons ── */
.btn{display:inline-flex;align-items:center;justify-content:center;gap:.4rem;border:none;border-radius:var(--radius-sm);padding:.48rem 1rem;font-size:.85rem;font-weight:600;cursor:pointer;transition:all .2s cubic-bezier(.4,0,.2,1);text-decoration:none;background:linear-gradient(135deg,var(--primary),var(--primary-600));color:#fff;box-shadow:0 2px 10px rgba(20,184,166,.3),inset 0 1px 0 rgba(255,255,255,.15)}
.btn:hover{filter:brightness(1.08);transform:translateY(-2px);box-shadow:0 6px 20px rgba(20,184,166,.35);text-decoration:none}
.btn:active{transform:translateY(0) scale(.98);filter:brightness(.95)}
.btn-outline{background:var(--card);color:var(--primary-700);border:1.5px solid var(--border);box-shadow:none}
.btn-outline:hover{background:var(--primary-50);border-color:var(--primary);box-shadow:0 2px 8px rgba(20,184,166,.12);filter:none}
.btn-danger{background:linear-gradient(135deg,#ef4444,#dc2626);box-shadow:0 2px 10px rgba(239,68,68,.25)}
.btn-danger:hover{box-shadow:0 6px 20px rgba(239,68,68,.3)}
.btn-sm{padding:.3rem .7rem;font-size:.8rem;border-radius:var(--radius-xs)}
.drag-handle{cursor:grab;user-select:none;font-size:1.1rem;color:var(--ink2);opacity:.5;text-align:center;width:1.5rem;padding:0 .2rem;transition:opacity .15s}
.drag-handle:hover{opacity:1}
tr.dragging{opacity:.4;outline:2px dashed var(--primary);outline-offset:-2px}
tr.drag-over{box-shadow:0 -2px 0 var(--primary) inset}
tr.drag-over-above td{border-top:2px solid var(--primary)!important}
tr.drag-over-below td{border-bottom:2px solid var(--primary)!important}
.field-sel{display:flex;flex-wrap:wrap;align-items:center;gap:.4rem;margin:.6rem 0 1.1rem;padding:.65rem .9rem;background:var(--card);border:1px solid var(--border);border-left:3px solid var(--primary);border-radius:var(--radius);box-shadow:var(--shadow)}
.field-sel .cur-field{font-weight:700;color:var(--primary)}
.matrix-bar{gap:.5rem .7rem}
.bar-seg{display:inline-flex;flex-wrap:wrap;align-items:center;gap:.35rem;font-size:.85rem;font-weight:500;color:var(--ink2)}
.bar-sep{width:1px;align-self:stretch;min-height:1.4rem;background:var(--border);margin:0 .3rem}
.st-sel{position:relative;display:inline-flex}
.st-btn{display:inline-flex;align-items:center;gap:.3rem;padding:.3rem .6rem;font-size:.82rem;font-weight:500;color:var(--ink);background:var(--card);border:1.5px solid var(--border);border-radius:var(--radius-sm);cursor:pointer;user-select:none;white-space:nowrap}
.st-btn:hover{border-color:var(--primary)}
.st-sel.open .st-btn{border-color:var(--primary);box-shadow:var(--input-ring)}
.st-sel.open .csel-arrow{transform:rotate(180deg)}
.st-drop{display:none;position:absolute;top:calc(100% + 4px);left:0;min-width:100%;background:var(--card);border:1.5px solid var(--border);border-radius:var(--radius-sm);box-shadow:var(--shadow-lg);z-index:20;overflow:hidden}
.st-sel.open .st-drop{display:block}
.st-opt{display:block;padding:.45rem .7rem;font-size:.82rem;color:var(--ink);white-space:nowrap;text-decoration:none}
.st-opt:hover{background:var(--primary-50)}
.st-opt.cur{background:var(--primary-50);color:var(--primary-700);font-weight:600}
.st-opt.cur::before{content:"\2713 ";color:var(--primary);font-weight:700}
.btn-group{display:flex;gap:.5rem;flex-wrap:wrap;align-items:center}

/* ── forms ── */
.form-wrap{max-width:720px;margin:0 auto}
.form-grid{display:grid;grid-template-columns:repeat(2,1fr);gap:1.1rem}
.form-grid .full{grid-column:1/-1}
.form-sep{grid-column:1/-1;border:0;border-top:1px solid var(--border);margin:.4rem 0}
.field{display:flex;flex-direction:column;gap:.35rem}
.field-label{font-size:.76rem;font-weight:700;color:var(--muted);text-transform:uppercase;letter-spacing:.05em}
.field input:not([type="checkbox"]),.field select{width:100%;padding:.65rem .8rem;font-size:.88rem;font-family:inherit;color:var(--ink);background:var(--input-bg);border:2px solid var(--input-border);border-radius:var(--radius-sm);outline:none;transition:border-color .15s,box-shadow .2s}
.field input:not([type="checkbox"]):focus,.field select:focus{border-color:var(--primary);box-shadow:var(--input-ring)}
.field input::placeholder{color:var(--muted);opacity:.6}
.field input[readonly]{opacity:.5;cursor:not-allowed;background:var(--bg-2)}
.field select{-webkit-appearance:none;appearance:none;background-image:url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='14' height='14' viewBox='0 0 14 14'%3E%3Cpath fill='%2364748b' d='M2 4.5l5 5 5-5'/%3E%3C/svg%3E");background-repeat:no-repeat;background-position:right .8rem center;padding-right:2.2rem;cursor:pointer}
.field select option{background:var(--card);color:var(--ink);padding:.4rem .6rem}
[data-theme="dark"] .field select{background-image:url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='14' height='14' viewBox='0 0 14 14'%3E%3Cpath fill='%2394a3b8' d='M2 4.5l5 5 5-5'/%3E%3C/svg%3E")}
[data-theme="dark"] .field select option{background:#1e293b;color:#e5e7eb}
.toggle{position:relative;display:inline-flex;align-items:center;gap:.6rem;cursor:pointer;font-size:.88rem;color:var(--ink)}
.toggle input{position:absolute;opacity:0;width:0;height:0;padding:0;border:0;margin:0;overflow:hidden}
.toggle .slider{width:46px;height:26px;background:var(--input-border);border-radius:13px;position:relative;transition:background .2s;flex-shrink:0}
.toggle .slider::after{content:"";position:absolute;top:3px;left:3px;width:20px;height:20px;background:#fff;border-radius:50%;transition:transform .2s;box-shadow:0 1px 4px rgba(0,0,0,.2)}
.toggle input:checked+.slider{background:var(--primary);box-shadow:0 0 10px rgba(20,184,166,.3)}
.toggle input:checked+.slider::after{transform:translateX(20px)}
.toggle input:focus-visible+.slider{box-shadow:var(--input-ring)}

/* ── custom select ── */
.csel{position:relative}
.csel select{display:none}
.csel-btn{display:flex;align-items:center;justify-content:space-between;width:100%;padding:.65rem .8rem;font-size:.88rem;font-family:inherit;color:var(--ink);background:var(--input-bg);border:2px solid var(--input-border);border-radius:var(--radius-sm);cursor:pointer;transition:border-color .15s,box-shadow .2s;outline:none;user-select:none}
.csel-btn:hover{border-color:var(--primary)}
.csel-btn:focus,.csel.open .csel-btn{border-color:var(--primary);box-shadow:var(--input-ring)}
.csel-arrow{font-size:.7rem;color:var(--muted);transition:transform .15s}
.csel.open .csel-arrow{transform:rotate(180deg)}
.csel-drop{display:none;position:absolute;top:calc(100% + 4px);left:0;right:0;background:var(--card);border:1.5px solid var(--border);border-radius:var(--radius-sm);box-shadow:var(--shadow-lg);z-index:20;overflow:hidden;max-height:240px;overflow-y:auto}
.csel.open .csel-drop{display:block}
.csel-opt{display:flex;align-items:center;gap:.5rem;padding:.55rem .85rem;font-size:.88rem;color:var(--ink);cursor:pointer;transition:background .1s}
.csel-opt:hover{background:var(--primary-50)}
.csel-opt.cur{background:var(--primary-50);color:var(--primary-700);font-weight:600}
.csel-opt.cur::before{content:"✓";color:var(--primary);font-weight:700;font-size:.8rem}
[data-theme="dark"] .csel-drop{background:var(--card);border-color:var(--border)}

/* ── modal dialog ── */
.modal-overlay{display:none;position:fixed;inset:0;background:rgba(0,0,0,.45);z-index:100;align-items:center;justify-content:center;backdrop-filter:blur(3px)}
.modal-overlay.show{display:flex}
.modal-box{background:var(--card);border:1px solid var(--border);border-radius:var(--radius);padding:1.6rem;min-width:320px;max-width:420px;box-shadow:var(--shadow-lg);text-align:center}
.modal-box h3{font-size:1.05rem;font-weight:700;margin-bottom:.6rem;color:var(--ink)}
.modal-box p{color:var(--muted);font-size:.9rem;margin-bottom:1.2rem;line-height:1.5}
.modal-actions{display:flex;gap:.6rem;justify-content:center}
[data-theme="dark"] .modal-overlay{background:rgba(0,0,0,.6)}
.kvs{display:flex;flex-wrap:wrap;gap:.45rem;font-size:.85rem}
.kvs b{color:var(--muted);font-weight:500}
.empty{color:var(--muted);padding:2.5rem;text-align:center;font-size:.9rem}

/* ── footer ── */
footer{color:var(--muted);font-size:.78rem;text-align:center;padding:2.5rem 1.4rem 1.5rem;border-top:1px solid var(--border);margin-top:2.5rem}
footer a{color:var(--muted);transition:color .15s}
footer a:hover{color:var(--primary-700)}
.footer-links{display:flex;justify-content:center;gap:.3rem 1.5rem;flex-wrap:wrap}

/* ── focus-visible ── */
:focus-visible{outline:2px solid var(--primary);outline-offset:2px}
button:focus-visible{outline:2px solid var(--primary);outline-offset:2px}

/* ── scrollbar ── */
::-webkit-scrollbar{width:7px;height:7px}
::-webkit-scrollbar-track{background:transparent}
::-webkit-scrollbar-thumb{background:var(--border);border-radius:4px}
::-webkit-scrollbar-thumb:hover{background:var(--muted)}

/* ── responsive ── */
@media(max-width:1024px){main{max-width:100%}}
@media(max-width:768px){
  header.top{padding:0}
  .ham{display:flex;align-items:center}
  nav{display:none;position:absolute;top:100%;left:0;right:0;background:linear-gradient(180deg,#1e293b,#0f172a);border-bottom:1px solid rgba(255,255,255,.08);padding:.5rem;flex-direction:column;box-shadow:0 8px 24px rgba(0,0,0,.3);z-index:30}
  nav.open{display:flex}
  nav a{padding:.6rem .9rem;border-radius:var(--radius-xs);border-bottom:none}
  nav a.active{border-bottom:none;background:rgba(255,255,255,.1)}
  main{padding:1.2rem 1rem 2.5rem}
  .grid{grid-template-columns:1fr}
  .form-grid{grid-template-columns:1fr}
}
@media(max-width:480px){
  header.top{padding:.45rem .7rem}
  main{padding:1rem .7rem 2rem}
  h1{font-size:1.3rem}
  .btn-group{flex-direction:column}
  .btn-group .btn{width:100%;justify-content:center}
}
.tab-bar{display:flex;gap:0;border-bottom:2px solid var(--border);margin-bottom:1rem}
.tab-btn{padding:.5rem 1.2rem;text-decoration:none;color:var(--fg-2);font-weight:500;border-bottom:2px solid transparent;margin-bottom:-2px;transition:all .15s}
.tab-btn:hover{color:var(--fg);background:var(--bg-2);border-radius:6px 6px 0 0}
.tab-btn.active{color:var(--accent);border-bottom-color:var(--accent)}
.rule-card{padding:.9rem 1rem;margin-bottom:.6rem}
.rule-fields{display:grid;grid-template-columns:1.5fr 1.5fr .7fr 1fr .5fr auto;gap:.7rem;align-items:end}
.rule-f-del{display:flex;align-items:end}
.rule-actions{display:flex;gap:.5rem;flex-wrap:wrap;margin-top:.8rem}
.dir-na{align-self:center;color:var(--muted);font-size:.85rem}
.rule-toggle{position:relative;display:inline-block;width:40px;height:22px}
.rule-toggle input{opacity:0;width:0;height:0}
.rule-toggle-slider{position:absolute;cursor:pointer;inset:0;background:var(--border);border-radius:22px;transition:.2s}
.rule-toggle-slider:before{content:'';position:absolute;height:16px;width:16px;left:3px;bottom:3px;background:#fff;border-radius:50%;transition:.2s}
.rule-toggle input:checked+.rule-toggle-slider{background:var(--primary)}
.rule-toggle input:checked+.rule-toggle-slider:before{transform:translateX(18px)}
@media(max-width:768px){.rule-fields{grid-template-columns:1fr 1fr;gap:.5rem}}
@media(max-width:480px){.rule-fields{grid-template-columns:1fr;gap:.4rem}}
`

type navItem struct{ H, Label, Key string }

var navItems = []navItem{
	{"/", "", "overview"},
	{"/balance", "", "balance"},
	{"/matrix", "", "matrix"},
	{"/changes", "", "changes"},
	{"/probes", "", "probes"},
	{"/audit", "", "audit"},
	{"/alerts", "", "alerts"},
	{"/stations", "", "stations"},
	{"/settings", "", "settings"},
	{"/system", "", "system"},
	{"/metrics", "", "metrics"},
}

func pageShell(lang, title, active, body string, showLogout bool) string {
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
	logoutBtn := ""
	if showLogout {
		logoutBtn = `<button class="icon-btn" onclick="tmLogout()" title="` + t(lang, "logout") + `">⏻</button>`
	}
	viewBtn := ""
	if active == "overview" {
		viewBtn = `<button class="auto-btn" id="tm-view" onclick="tmToggleView()" title="` + t(lang, "view.toggle") + `">⊞ <span id="tm-view-label">` + t(lang, "view.compact") + `</span></button>`
	}
	tools := fmt.Sprintf(`<div class="tools">%s<button class="auto-btn" id="tm-autorefresh" onclick="tmToggleAuto()" title="Auto-refresh">🔄 <span id="tm-ar-label">60s</span></button>`+
		`<button class="icon-btn" id="tm-theme" onclick="tmToggleTheme()" title="%s">🌙</button>`+
		`<button class="lang-btn" onclick="tmSetLang('%s')">%s</button>%s</div>`, viewBtn, t(lang, "theme"), other, otherLabel, logoutBtn)
	js := `<script>(function(){var d=document.documentElement,s=localStorage.getItem('tm-theme');` +
		`if(s==='dark'||s==='light'){d.dataset.theme=s;}function syncTheme(){var b=document.getElementById('tm-theme');if(b)b.textContent=d.dataset.theme==='dark'?'☀️':'🌙';}syncTheme();` +
		`window.tmToggleTheme=function(){var n=(d.dataset.theme==='dark')?'light':'dark';d.dataset.theme=n;localStorage.setItem('tm-theme',n);syncTheme();};` +
		`window.tmSetLang=function(l){document.cookie='tm-lang='+l+';path=/;max-age=2592000';location.reload();};` +
		`var ar=localStorage.getItem('tm-autorefresh');if(ar===null)ar='1';function syncAuto(){var b=document.getElementById('tm-autorefresh'),l=document.getElementById('tm-ar-label');if(b){b.classList.toggle('on',ar==='1');if(l)l.textContent=ar==='1'?'60s':'OFF';}}syncAuto();` +
		// overview density toggle: cookie (not localStorage) so the SERVER sees
		// the mode at render time and picks groupRatioPills vs groupRatioChart
		// (mirrors the tm-lang cookie pattern). Default compact = pill strip.
		`var vm=(document.cookie.match(/(?:^|; )tm-overview=([^;]+)/)||[])[1]||'compact';function syncView(){var b=document.getElementById('tm-view'),l=document.getElementById('tm-view-label');if(b){b.classList.toggle('on',vm==='compact');if(l)l.textContent=vm==='compact'?'` + t(lang, "view.compact") + `':'` + t(lang, "view.detail") + `';}}syncView();` +
		`window.tmToggleView=function(){var n=vm==='compact'?'detail':'compact';document.cookie='tm-overview='+n+';path=/;max-age=2592000';location.reload();};` +
		`window.tmSwapMain=function(html){var doc=new DOMParser().parseFromString(html,'text/html');var nm=doc.querySelector('main');if(!nm)return false;var m=document.querySelector('main');if(!m)return false;var sx=window.scrollX,sy=window.scrollY;m.replaceWith(nm);/* scripts that come back via DOM parsing don't auto-execute, so re-run inline page scripts (drag-reorder, form handlers) inside the swapped <main> */nm.querySelectorAll('script').forEach(function(old){var s=document.createElement('script');if(old.src){s.src=old.src;}else{s.textContent=old.textContent;}old.replaceWith(s);});if(window.tmInitSelects)window.tmInitSelects();window.scrollTo(sx,sy);nm.style.opacity='0';nm.style.transition='opacity .15s';requestAnimationFrame(function(){requestAnimationFrame(function(){nm.style.opacity='1';});});return true;};` +
		`window.tmRefreshPartial=function(){return fetch(location.href,{credentials:'same-origin',cache:'no-store'}).then(function(r){/* 302 to /login (session expired) or any non-200 → fall back to a hard reload, which will then redirect to login */if(!r.ok||r.redirected){throw new Error('reload');}return r.text();}).then(function(t){if(!window.tmSwapMain(t))throw new Error('no main');});};` +
		`if(ar==='1'){var tmDirty=false;window.tmMarkDirty=function(){tmDirty=true;};document.addEventListener('input',function(){tmDirty=true;},true);document.addEventListener('change',function(){tmDirty=true;},true);/* skip refresh once the user has edited a field (incl. drag-reorder), so unsaved edits aren't wiped; reschedule so it keeps ticking instead of doing a one-shot full reload */function tmSched(){setTimeout(function(){if(tmDirty){tmSched();return;}window.tmRefreshPartial().then(tmSched).catch(function(){location.reload();});},60000);}tmSched();}` +
		`window.tmToggleAuto=function(){var n=(localStorage.getItem('tm-autorefresh')||'1')==='1'?'0':'1';localStorage.setItem('tm-autorefresh',n);location.reload();};` +
		`window.tmHam=function(){document.getElementById('tm-nav').classList.toggle('open');};` +
		`document.addEventListener('click',function(e){var n=document.getElementById('tm-nav');if(n&&n.classList.contains('open')&&!e.target.closest('nav')&&!e.target.closest('.ham'))n.classList.remove('open');});` +
		// custom select init — wrapped as a re-callable global so a partial
		// <main> swap can re-init fresh <select> elements without re-running the
		// whole IIFE (which would double-bind document-level listeners).
		`window.tmInitSelects=function(){document.querySelectorAll('.field select').forEach(function(sel){if(sel.closest('.csel'))return;` +
		`var w=document.createElement('div');w.className='csel';sel.parentNode.insertBefore(w,sel);w.appendChild(sel);` +
		`var btn=document.createElement('div');btn.className='csel-btn';btn.tabIndex=0;btn.textContent=sel.options[sel.selectedIndex]?sel.options[sel.selectedIndex].text:'';` +
		`var arr=document.createElement('span');arr.className='csel-arrow';arr.textContent='▾';btn.appendChild(arr);w.appendChild(btn);` +
		`var drop=document.createElement('div');drop.className='csel-drop';` +
		`Array.from(sel.options).forEach(function(o){var d=document.createElement('div');d.className='csel-opt'+(o.selected?' cur':'');d.textContent=o.text;d.dataset.val=o.value;` +
		`d.onclick=function(){sel.value=this.dataset.val;sel.dispatchEvent(new Event('change'));btn.childNodes[0].textContent=this.textContent;drop.querySelectorAll('.csel-opt').forEach(function(x){x.classList.remove('cur')});this.classList.add('cur');w.classList.remove('open');};drop.appendChild(d);});` +
		`w.appendChild(drop);btn.onclick=function(e){e.stopPropagation();document.querySelectorAll('.csel.open').forEach(function(c){if(c!==w)c.classList.remove('open')});w.classList.toggle('open');};` +
		`});};window.tmInitSelects();document.addEventListener('click',function(e){document.querySelectorAll('.csel.open').forEach(function(c){c.classList.remove('open')});document.querySelectorAll('.st-sel.open').forEach(function(c){if(!c.contains(e.target))c.classList.remove('open')})});` +
		// custom modal confirm
		`window.tmConfirm=function(msg,cb){var o=document.getElementById('tm-modal');var t=document.getElementById('tm-modal-msg');t.textContent=msg;o.classList.add('show');` +
		`document.getElementById('tm-modal-ok').onclick=function(){o.classList.remove('show');cb();};document.getElementById('tm-modal-cancel').onclick=function(){o.classList.remove('show');};};` +
		`window.tmLogout=function(){fetch('/api/logout',{method:'POST'}).then(function(){location.href='/login';}).catch(function(){location.href='/login';});};` +
		`window.tmToggleBatch=function(id,el){var rows=document.querySelectorAll('.batch-extra-'+id);var show=rows[0]&&rows[0].style.display==='none';rows.forEach(function(r){r.style.display=show?'':'none';});el.textContent=show?el.dataset.less:el.dataset.more;};` +
		// global floating tooltip via fixed-position div; handles both sparkline
		// dots and per-user-override ✎ badges (any .spark-dot / .gr-ovr with data-tip).
		`var tip=document.getElementById('tm-tip');var TIP_SEL='.spark-dot, .gr-ovr';document.addEventListener('mouseover',function(e){var d=e.target.closest(TIP_SEL);if(d&&d.dataset.tip){tip.textContent=d.dataset.tip;var r=d.getBoundingClientRect();tip.style.left=(r.left+r.width/2)+'px';tip.style.top=r.top+'px';tip.classList.add('show');}});` +
		`document.addEventListener('mouseout',function(e){if(e.target.closest(TIP_SEL))tip.classList.remove('show');});` +
		`document.querySelectorAll('.tip-dot').forEach(function(dot){var pop=dot.parentNode.querySelector('.tip-pop');if(!pop)return;dot.addEventListener('mouseenter',function(){var r=dot.getBoundingClientRect();pop.style.transform='none';pop.style.left=r.left+'px';pop.style.top=(r.bottom+8)+'px';if(r.left+pop.offsetWidth>window.innerWidth-16){pop.style.left=Math.max(16,window.innerWidth-pop.offsetWidth-16)+'px';}pop.classList.add('show');});dot.addEventListener('mouseleave',function(){pop.classList.remove('show');});});` +
		`})();</script>`
	modalHTML := `<div class="modal-overlay" id="tm-modal"><div class="modal-box">` +
		`<h3>` + t(lang, "modal.title") + `</h3><p id="tm-modal-msg"></p>` +
		`<div class="modal-actions"><button class="btn btn-outline" id="tm-modal-cancel">` + t(lang, "modal.cancel") + `</button>` +
		`<button class="btn btn-danger" id="tm-modal-ok">` + t(lang, "modal.confirm") + `</button></div></div></div>` +
		`<div id="tm-tip"></div>`
	favicon := `<link rel="icon" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'%3E%3Crect width='32' height='32' rx='8' fill='%2314b8a6'/%3E%3Ctext x='16' y='23' font-size='16' font-weight='bold' fill='white' text-anchor='middle' font-family='sans-serif'%3ETM%3C/text%3E%3C/svg%3E">`
	return fmt.Sprintf(`<!doctype html><html lang="%s"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">%s<title>%s · TransitMonitor</title><style>%s</style></head>`+
		`<body class="page-%s"><header class="top"><div class="top-row">`+
		`<div class="brand"><span class="logo">TM</span>TransitMonitor</div>`+
		`<button class="ham" onclick="tmHam()" aria-label="menu">☰</button>`+
		`<nav id="tm-nav">%s</nav>%s</div></header>`+
		`<main>%s</main>`+
		`<footer><div class="footer-links">`+
		`<span>TransitMonitor</span>`+
		`<a href="/api/stations">JSON API</a>`+
		`<a href="/metrics">/metrics</a>`+
		`<a href="/healthz">/healthz</a>`+
		`</div></footer>%s%s</body></html>`,
		lang, favicon, html.EscapeString(title), appCSS, active, n.String(), tools, body, modalHTML, js)
}

func (s *Server) writeHTMLShell(w http.ResponseWriter, lang, title, active, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	_, _ = w.Write([]byte(pageShell(lang, title, active, body, s.HasPassword())))
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
// (group name) with a full-width separator row carrying the group + a tier tag.
func renderRatioTable(cols []string, rows [][]string, hidden, pinned map[string]bool) string {
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
			tag := ` <span class="badge-sm b-ok">` + grp[0:0] + `</span>` // placeholder — overwritten below
			if hidden != nil && hidden[grp] {
				tag = ` <span class="badge-sm b-warn">已隐藏</span>`
			} else if pinned != nil && pinned[grp] {
				tag = ` <span class="badge-sm b-cheap">⭐ 置顶</span>`
			} else {
				tag = ""
			}
			b.WriteString(`<tr class="grp-sep"><td colspan="` + fmt.Sprint(len(cols)) + `">` +
				`<span class="grp-tag">` + grp + `</span>` + tag + `</td></tr>`)
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

// overrideMark returns an inline "✎" badge when the group's rate is a per-user
// override of its default rate. Hovering shows the original default rate via the
// global #tm-tip floating tooltip (data-tip), not the native title, so it appears
// instantly instead of after the browser's ~1-2s title delay. Empty when the
// rate is the group default (no override).
func overrideMark(lang string, g domain.GroupDisplay) string {
	if !g.Overridden {
		return ""
	}
	return fmt.Sprintf(`<span class="gr-ovr" data-tip="%s">✎</span>`,
		esc(fmt.Sprintf(t(lang, "grp.override"), fmtRatio(g.Default)+"x")))
}

// groupRatioChart renders the horizontal bar chart of group ratios in the given
// order (caller partitions + orders via domain.PartitionGroups). lg=true renders
// the larger hero variant (station detail); false the compact variant (overview
// cards). Color is by ratio value: b-cheap (<0.5), b-warn (>1.0), else b-ok.
// Returns "" when the slice is empty.
func groupRatioChart(lang string, groups []domain.GroupDisplay, lg bool) string {
	if len(groups) == 0 {
		return ""
	}
	maxV := 0.0
	for _, g := range groups {
		if g.Ratio > maxV {
			maxV = g.Ratio
		}
	}
	cls := ""
	if lg {
		cls = " lg"
	}
	var b strings.Builder
	b.WriteString(`<div class="gr-chart` + cls + `">`)
	for _, g := range groups {
		pct := 0.0
		if maxV > 0 {
			pct = g.Ratio / maxV * 100
		}
		bc := "b-ok"
		if g.Ratio < 0.5 {
			bc = "b-cheap"
		} else if g.Ratio > 1.0 {
			bc = "b-warn"
		}
		rowCls := "gr-row"
		namePrefix := ""
		if g.Pinned {
			rowCls = "gr-row gr-pinned"
			namePrefix = "⭐ "
		}
		b.WriteString(fmt.Sprintf(
			`<div class="%s"><span class="gr-name" title="%s">%s%s</span>`+
				`<div class="gr-track"><span class="gr-bar %s" style="width:%.1f%%"></span></div>`+
				`<span class="gr-val">%s%s</span></div>`,
			rowCls, esc(g.Name), namePrefix, esc(g.Name), bc, pct, fmtRatio(g.Ratio)+"x", overrideMark(lang, g)))
	}
	b.WriteString(`</div>`)
	return b.String()
}

// groupRatioPills renders the overview card's compact color-coded ratio pill
// strip, reusing the already-defined .gr-preview + .badge-sm + b-cheap/b-warn/
// b-ok color scale. One pill per visible group prints "name <b>0.8x</b>" plus
// the ✎ override mark — every group's ratio stays fully visible, only denser
// than the one-row-per-group bar chart (groupRatioChart). Color mirrors that
// chart (ratio<0.5 b-cheap, >1.0 b-warn, else b-ok) so semantics match. Returns
// "" for an empty slice. The full bar chart remains on the station detail page.
func groupRatioPills(lang string, groups []domain.GroupDisplay) string {
	if len(groups) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="gr-preview">`)
	for _, g := range groups {
		bc := "b-ok"
		if g.Ratio < 0.5 {
			bc = "b-cheap"
		} else if g.Ratio > 1.0 {
			bc = "b-warn"
		}
		pinCls := ""
		namePfx := ""
		if g.Pinned {
			pinCls = " pill-pinned"
			namePfx = "⭐ "
		}
		b.WriteString(fmt.Sprintf(
			`<span class="badge-sm %s%s" title="%s"><span class="pn">%s%s</span> <b>%s</b>%s</span>`,
			bc, pinCls, esc(g.Name), namePfx, esc(g.Name), fmtRatio(g.Ratio)+"x", overrideMark(lang, g)))
	}
	b.WriteString(`</div>`)
	return b.String()
}

// renderHiddenGroupsExpander renders the "+N hidden" collapsible footer for a
// station card, listing hidden groups dimmed so their ratios stay reachable
// (ratios are never truly hidden — honoring the project principle).
func renderHiddenGroupsExpander(lang string, hidden []domain.GroupDisplay) string {
	if len(hidden) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<details class="gr-hidden"><summary>`)
	b.WriteString(fmt.Sprintf(t(lang, "batch.hiddenmore"), len(hidden)))
	b.WriteString(`</summary><div class="gr-chart">`)
	for _, g := range hidden {
		bc := "b-ok"
		if g.Ratio < 0.5 {
			bc = "b-cheap"
		} else if g.Ratio > 1.0 {
			bc = "b-warn"
		}
		b.WriteString(fmt.Sprintf(
			`<div class="gr-row gr-dim"><span class="gr-name" title="%s">%s</span>`+
				`<div class="gr-track"><span class="gr-bar %s" style="width:100%%"></span></div>`+
				`<span class="gr-val">%s%s</span></div>`,
			esc(g.Name), esc(g.Name), bc, fmtRatio(g.Ratio)+"x", overrideMark(lang, g)))
	}
	b.WriteString(`</div></details>`)
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
