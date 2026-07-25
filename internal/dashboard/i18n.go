package dashboard

import (
	"net/http"
)

// trans holds the two UI locales (zh / en). Keys are dotted identifiers.
var trans = map[string]map[string]string{
	"zh": {
		"nav.overview": "概览", "nav.matrix": "矩阵", "nav.changes": "变更",
		"nav.probes": "探测", "nav.audit": "审计", "nav.alerts": "告警", "nav.metrics": "指标", "nav.balance": "余额",
		"title.overview": "概览", "title.matrix": "跨站对比矩阵", "title.changes": "变更",
		"title.probes": "真实成本探测", "title.audit": "审计日志", "title.balance": "余额监控",
		"sub.balance":     "各站点账户余额（USD）· <span class=\"pcell p-cheap\">绿=充足</span> · <span class=\"pcell p-high\">红=偏低</span> · — = 无余额数据",
		"section.balance": "余额", "col.remaining": "剩余 $", "col.used": "冻结/已用 $",
		"col.total": "额度 $", "col.lastupdate": "最近更新", "col.balance": "余额",
		"balance.unlimited": "无上限", "balance.nodata": "无余额数据",
		"balance.trend": "余额趋势", "balance.drop": "较上次 %s",
		"section.stations": "站点", "section.ratios": "当前倍率", "section.explore": "导航",
		"section.groupratios": "分组倍率",
		"section.grouptrend":  "分组倍率趋势", "section.groupchanges": "分组倍率变更", "section.modelratios": "模型倍率",
		"meta.lastscrape": "最近抓取", "meta.models": "模型", "meta.groups": "分组",
		"btn.matrix":      "跨站对比 →",
		"btn.matrixgroup": "分组", "btn.matrixmodel": "模型",
		"btn.taball": "全部", "btn.tabgroup": "分组倍率", "btn.tabmodel": "模型",
		"sub.overview": "中转站分组倍率监控 · 实时折扣倍率 + 变更追踪 · ",
		"sub.matrix":   "分组倍率跨站对比 · <span class=\"pcell p-cheap\">绿=低折扣</span> · <span class=\"pcell p-high\">红=高</span> · — = 该站无此分组",
		"sub.changes":  "站点 %s 的倍率变更（严重=红, 警告=黄）· 分组倍率变更高亮",
		"sub.probes":   "站点 %s · 加价% = 真实(探测) vs 声明有效价（<span class=\"pcell p-high\">红=暗中加价</span> / <span class=\"pcell p-cheap\">绿=折扣</span>）",
		"sub.audit":    "启动、探测、凭据持久化等动作记录",
		"col.time":     "时间", "col.group": "分组", "col.model": "模型", "col.field": "字段",
		"col.old": "旧值", "col.new": "新值", "col.deltapct": "变化%", "col.severity": "严重度",
		"col.oldratio": "旧倍率", "col.newratio": "新倍率",
		"col.tokinout": "token 入/出", "col.declared": "声明 $/M", "col.measured": "实测 $/M",
		"col.markup": "加价%", "col.cost": "成本 $", "col.status": "状态",
		"col.modelratio": "模型倍率", "col.completionratio": "补全倍率", "col.groupratio": "分组倍率", "col.effratio": "有效倍率 入/出",
		"col.effout": "有效倍率 出", "col.inputusd": "输入 $/M", "col.outputusd": "输出 $/M", "col.cacheread": "缓存读 $/M", "col.cachewrite": "缓存写 $/M",
		"col.station": "站点",
		"col.actor":   "角色", "col.action": "动作", "col.target": "目标", "col.detail": "详情",
		"badge.critical": "严重", "badge.warning": "警告", "badge.info": "提示", "badge.ok": "正常",
		"empty": "暂无数据", "theme": "主题",
		"pager.info": "共 %d 条 · 第 %d / %d 页", "pager.prev": "上一页", "pager.next": "下一页",
		"field.input_usd_per_1m": "输入价", "field.output_usd_per_1m": "输出价", "field.native_ratio": "原生倍率",
		"field.presence": "上下架", "field.sentinel_flip": "标记翻转", "field.group_ratio": "分组倍率",
		"val.added": "上架", "val.removed": "下架",
		"batch.summary":       "%s · %d 条变更",
		"expand.models":       "展开模型倍率表",
		"expand.probes":       "展开真实成本探测",
		"expand.modelchanges": "展开模型变更",
		"recent.change":       "最近变更",
		"nav.stations":        "站点管理",
		"title.stations":      "站点管理", "title.newstation": "添加站点",
		"form.id": "站点 ID", "form.name": "名称", "form.baseurl": "Base URL", "form.kind": "类型",
		"form.apikey": "API Key (sk-)", "form.pat": "PAT（可选）", "form.jwt": "JWT（可选，sub2api）", "form.group": "分组",
		"form.userid":       "User ID（PAT 对应用户 ID）",
		"form.pollinterval": "轮询间隔", "form.enabled": "启用", "form.add": "添加", "form.delete": "删除",
		"form.poll":    "轮询",
		"form.confirm": "确认删除该站点？", "form.empty": "（暂无站点，点「添加站点」新增）",
		"modal.title": "确认操作", "modal.confirm": "确认", "modal.cancel": "取消",
		"title.editstation": "编辑站点", "form.edit": "编辑", "form.save": "保存", "form.keepblank": "留空保持不变",
		"form.id.auto":     "留空自动生成",
		"form.admin_email": "管理邮箱（sub2api 自动登录）", "form.admin_pass": "管理密码（sub2api 自动登录）",
		"form.fetchjwt": "登录获取 JWT", "form.jwt.ok": "JWT 获取成功，有效期至 %s", "form.jwt.fail": "JWT 获取失败：%s",
		"form.jwt.nocred": "请先填写管理邮箱和管理密码",
		"nav.settings":    "设置", "title.settings": "告警通知设置", "sub.settings": "配置钉钉 / 飞书 / Slack / 通用 webhook / QQ 机器人地址；secrets 加密落库。留空 secret 表示保持不变。",
		"section.dingtalk": "钉钉", "section.lark": "飞书", "section.slack": "Slack", "section.webhook": "通用 Webhook", "section.qq": "QQ 机器人",
		"form.webhook.url": "Webhook URL", "form.secret": "签名 Secret", "form.keepblank.hint": "留空保持不变",
		"form.qq.appid": "AppID", "form.qq.appsecret": "AppSecret", "form.qq.groupopenid": "群 group_openid",
		"form.qq.note": "官方 Bot OpenAPI v2：AppID/AppSecret 换 access_token，推送至目标群。主动推送需机器人开通主动消息权限。",
		"btn.test":     "测试", "btn.save": "保存",
		"settings.saved": "已保存，通知已重新加载", "settings.test.ok": "测试通知已发送（查看 /alerts 页确认结果）", "settings.test.fail": "测试发送失败：%s",
		"settings.enc.disabled": "未设置 TRANSMONITOR_ENCRYPTION_KEY：secrets 无法加密落库。设置该环境变量后即可在此页面在线配置；当前可在 config.yaml 中编辑。",
		"settings.no.manager":   "调度器未接入，无法保存",
		"badge.decrypt_failed":  "凭据解密失败",
		"banner.decrypt_failed": "%d 个站点的凭据解密失败（ENCRYPTION_KEY 不匹配）— 请到站点管理页编辑并重新录入凭据",
	},
	"en": {
		"nav.overview": "Overview", "nav.matrix": "Matrix", "nav.changes": "Changes",
		"nav.probes": "Probes", "nav.audit": "Audit", "nav.alerts": "Alerts", "nav.metrics": "Metrics", "nav.balance": "Balance",
		"title.overview": "Overview", "title.matrix": "Cross-station matrix", "title.changes": "Changes",
		"title.probes": "Real-cost probes", "title.audit": "Audit log", "title.balance": "Balance monitor",
		"sub.balance":     "Per-station account balance (USD) · <span class=\"pcell p-cheap\">green=healthy</span> · <span class=\"pcell p-high\">red=low</span> · — = no balance data",
		"section.balance": "Balance", "col.remaining": "Remaining $", "col.used": "Frozen/Used $",
		"col.total": "Limit $", "col.lastupdate": "Last update", "col.balance": "Balance",
		"balance.unlimited": "unlimited", "balance.nodata": "no balance data",
		"balance.trend": "Balance trend", "balance.drop": "vs last %s",
		"section.stations": "Stations", "section.ratios": "Current ratios", "section.explore": "Explore",
		"section.groupratios": "Group Ratios",
		"section.grouptrend":  "Group Ratio Trends", "section.groupchanges": "Group Ratio Changes", "section.modelratios": "Model Ratios",
		"meta.lastscrape": "last scrape", "meta.models": "models", "meta.groups": "groups",
		"btn.matrix":      "Matrix →",
		"btn.matrixgroup": "Groups", "btn.matrixmodel": "Models",
		"btn.taball": "All", "btn.tabgroup": "Group ratios", "btn.tabmodel": "Models",
		"sub.overview": "LLM relay group-ratio monitor · live discount ratios + change tracking · ",
		"sub.matrix":   "Group ratios across stations · <span class=\"pcell p-cheap\">green=low</span> · <span class=\"pcell p-high\">red=high</span> · — = group absent",
		"sub.changes":  "Ratio changes for station %s (critical=red, warning=yellow) · group-ratio changes highlighted",
		"sub.probes":   "Station %s · markup% = measured(probe) vs declared (<span class=\"pcell p-high\">red=surcharge</span> / <span class=\"pcell p-cheap\">green=discount</span>)",
		"sub.audit":    "Startup, probe, credential-persistence actions",
		"col.time":     "time", "col.group": "group", "col.model": "model", "col.field": "field",
		"col.old": "old", "col.new": "new", "col.deltapct": "delta%", "col.severity": "severity",
		"col.oldratio": "old ratio", "col.newratio": "new ratio",
		"col.tokinout": "tok in/out", "col.declared": "declared $/M", "col.measured": "measured $/M",
		"col.markup": "markup%", "col.cost": "cost $", "col.status": "status",
		"col.modelratio": "model ratio", "col.completionratio": "completion ratio", "col.groupratio": "group ratio", "col.effratio": "eff ratio in/out",
		"col.effout": "eff ratio out", "col.inputusd": "input $/M", "col.outputusd": "output $/M", "col.cacheread": "cache read $/M", "col.cachewrite": "cache write $/M",
		"col.station": "station",
		"col.actor":   "actor", "col.action": "action", "col.target": "target", "col.detail": "detail",
		"badge.critical": "critical", "badge.warning": "warning", "badge.info": "info", "badge.ok": "ok",
		"empty": "No data", "theme": "Theme",
		"pager.info": "%d total · page %d / %d", "pager.prev": "Prev", "pager.next": "Next",
		"field.input_usd_per_1m": "Input price", "field.output_usd_per_1m": "Output price", "field.native_ratio": "Native ratio",
		"field.presence": "Presence", "field.sentinel_flip": "Sentinel flip", "field.group_ratio": "Group ratio",
		"val.added": "Added", "val.removed": "Removed",
		"batch.summary":       "%s · %d changes",
		"expand.models":       "Expand model ratio table",
		"expand.probes":       "Expand real-cost probes",
		"expand.modelchanges": "Expand model changes",
		"recent.change":       "recent change",
		"nav.stations":        "Stations",
		"title.stations":      "Stations", "title.newstation": "Add station",
		"form.id": "Station ID", "form.name": "Name", "form.baseurl": "Base URL", "form.kind": "Kind",
		"form.apikey": "API Key (sk-)", "form.pat": "PAT (optional)", "form.jwt": "JWT (optional, sub2api)", "form.group": "Group",
		"form.userid":       "User ID (for PAT's New-Api-User header)",
		"form.pollinterval": "Poll interval", "form.enabled": "Enabled", "form.add": "Add", "form.delete": "Delete",
		"form.poll":    "Poll",
		"form.confirm": "Delete this station?", "form.empty": "(no stations yet — click Add station)",
		"modal.title": "Confirm", "modal.confirm": "Confirm", "modal.cancel": "Cancel",
		"title.editstation": "Edit station", "form.edit": "Edit", "form.save": "Save", "form.keepblank": "leave blank to keep",
		"form.id.auto":     "leave blank to auto-generate",
		"form.admin_email": "Admin Email (sub2api auto-login)", "form.admin_pass": "Admin Password (sub2api auto-login)",
		"form.fetchjwt": "Login for JWT", "form.jwt.ok": "JWT obtained, expires %s", "form.jwt.fail": "JWT login failed: %s",
		"form.jwt.nocred": "Please fill admin email and password first",
		"nav.settings":    "Settings", "title.settings": "Alert notifier settings", "sub.settings": "Configure DingTalk / Lark / Slack / generic webhook / QQ bot addresses; secrets encrypted at rest. Leave a secret blank to keep it.",
		"section.dingtalk": "DingTalk", "section.lark": "Lark", "section.slack": "Slack", "section.webhook": "Generic Webhook", "section.qq": "QQ Bot",
		"form.webhook.url": "Webhook URL", "form.secret": "Signing Secret", "form.keepblank.hint": "leave blank to keep",
		"form.qq.appid": "AppID", "form.qq.appsecret": "AppSecret", "form.qq.groupopenid": "group_openid",
		"form.qq.note": "Official Bot OpenAPI v2: AppID/AppSecret → access_token, pushed to the target group. Proactive push requires the bot's 主动消息 permission.",
		"btn.test":     "Test", "btn.save": "Save",
		"settings.saved": "Saved; notifiers reloaded", "settings.test.ok": "Test alert sent (check /alerts for the result)", "settings.test.fail": "Test send failed: %s",
		"settings.enc.disabled": "TRANSMONITOR_ENCRYPTION_KEY not set: secrets can't be encrypted at rest. Set it to configure notifiers here; for now edit config.yaml.",
		"settings.no.manager":   "scheduler not wired; cannot save",
		"badge.decrypt_failed":  "creds decrypt failed",
		"banner.decrypt_failed": "%d station(s) failed credential decryption (ENCRYPTION_KEY mismatch) — re-enter their credentials on the Stations page",
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
