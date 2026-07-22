package dashboard

import (
	"net/http"
)

// trans holds the two UI locales (zh / en). Keys are dotted identifiers.
var trans = map[string]map[string]string{
	"zh": {
		"nav.overview": "概览", "nav.matrix": "矩阵", "nav.changes": "变更",
		"nav.probes": "探测", "nav.audit": "审计", "nav.metrics": "指标",
		"title.overview": "概览", "title.matrix": "跨站对比矩阵", "title.changes": "变更",
		"title.probes": "真实成本探测", "title.audit": "审计日志",
		"section.stations": "站点", "section.explore": "导航",
		"meta.lastscrape": "最近抓取", "meta.models": "模型",
		"btn.matrix":   "跨站对比 →",
		"sub.overview": "中转站倍率监控 · 归一化有效 USD/1M token · ",
		"sub.matrix":   "有效 USD/1M token 跨站对比 · <span class=\"pcell p-cheap\">绿=最便宜</span> · <span class=\"pcell p-high\">红=最贵</span> · 徽章=不可派生",
		"sub.changes":  "站点 %s 的倍率/有效价变更（严重=红, 警告=黄）",
		"sub.probes":   "站点 %s · 加价% = 真实(探测) vs 声明有效价（<span class=\"pcell p-high\">红=暗中加价</span> / <span class=\"pcell p-cheap\">绿=折扣</span>）",
		"sub.audit":    "启动、探测、凭据持久化等动作记录",
		"col.time":     "时间", "col.group": "分组", "col.model": "模型", "col.field": "字段",
		"col.old": "旧值", "col.new": "新值", "col.deltapct": "变化%", "col.severity": "严重度",
		"col.tokinout": "token 入/出", "col.declared": "声明 $/M", "col.measured": "实测 $/M",
		"col.markup": "加价%", "col.cost": "成本 $", "col.status": "状态",
		"col.actor": "角色", "col.action": "动作", "col.target": "目标", "col.detail": "详情",
		"badge.critical": "严重", "badge.warning": "警告", "badge.info": "提示", "badge.ok": "正常",
		"empty": "暂无数据", "theme": "主题",
		"nav.stations":   "站点管理",
		"title.stations": "站点管理", "title.newstation": "添加站点",
		"form.id": "站点 ID", "form.name": "名称", "form.baseurl": "Base URL", "form.kind": "类型",
		"form.apikey": "API Key (sk-)", "form.pat": "PAT（可选）", "form.jwt": "JWT（可选，sub2api）", "form.group": "分组",
		"form.pollinterval": "轮询间隔", "form.enabled": "启用", "form.add": "添加", "form.delete": "删除",
		"form.confirm": "确认删除该站点？", "form.empty": "（暂无站点，点「添加站点」新增）",
	},
	"en": {
		"nav.overview": "Overview", "nav.matrix": "Matrix", "nav.changes": "Changes",
		"nav.probes": "Probes", "nav.audit": "Audit", "nav.metrics": "Metrics",
		"title.overview": "Overview", "title.matrix": "Cross-station matrix", "title.changes": "Changes",
		"title.probes": "Real-cost probes", "title.audit": "Audit log",
		"section.stations": "Stations", "section.explore": "Explore",
		"meta.lastscrape": "last scrape", "meta.models": "models",
		"btn.matrix":   "Matrix →",
		"sub.overview": "LLM relay-station ratio monitor · normalized effective USD/1M token · ",
		"sub.matrix":   "Effective USD/1M token across stations · <span class=\"pcell p-cheap\">green=cheapest</span> · <span class=\"pcell p-high\">red=priciest</span> · badge=non-derivable",
		"sub.changes":  "Ratio / effective-price changes for station %s (critical=red, warning=yellow)",
		"sub.probes":   "Station %s · markup% = measured(probe) vs declared (<span class=\"pcell p-high\">red=surcharge</span> / <span class=\"pcell p-cheap\">green=discount</span>)",
		"sub.audit":    "Startup, probe, credential-persistence actions",
		"col.time":     "time", "col.group": "group", "col.model": "model", "col.field": "field",
		"col.old": "old", "col.new": "new", "col.deltapct": "delta%", "col.severity": "severity",
		"col.tokinout": "tok in/out", "col.declared": "declared $/M", "col.measured": "measured $/M",
		"col.markup": "markup%", "col.cost": "cost $", "col.status": "status",
		"col.actor": "actor", "col.action": "action", "col.target": "target", "col.detail": "detail",
		"badge.critical": "critical", "badge.warning": "warning", "badge.info": "info", "badge.ok": "ok",
		"empty": "No data", "theme": "Theme",
		"nav.stations":   "Stations",
		"title.stations": "Stations", "title.newstation": "Add station",
		"form.id": "Station ID", "form.name": "Name", "form.baseurl": "Base URL", "form.kind": "Kind",
		"form.apikey": "API Key (sk-)", "form.pat": "PAT (optional)", "form.jwt": "JWT (optional, sub2api)", "form.group": "Group",
		"form.pollinterval": "Poll interval", "form.enabled": "Enabled", "form.add": "Add", "form.delete": "Delete",
		"form.confirm": "Delete this station?", "form.empty": "(no stations yet — click Add station)",
	},
}

// t returns the translated string for lang/key (falls back to en, then key).
func t(lang, key string) string {
	if d, ok := trans[lang]; ok {
		if v, ok := d[key]; ok {
			return v
		}
	}
	if d, ok := trans["en"]; ok {
		if v, ok := d[key]; ok {
			return v
		}
	}
	return key
}

// lang resolves the request locale: ?lang= (sets cookie) > cookie tm-lang > "zh".
func (s *Server) lang(w http.ResponseWriter, r *http.Request) string {
	if q := r.URL.Query().Get("lang"); q == "zh" || q == "en" {
		http.SetCookie(w, &http.Cookie{Name: "tm-lang", Value: q, Path: "/", MaxAge: 86400 * 30})
		return q
	}
	if c, err := r.Cookie("tm-lang"); err == nil && (c.Value == "zh" || c.Value == "en") {
		return c.Value
	}
	return "zh"
}
