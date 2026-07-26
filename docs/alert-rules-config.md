# 告警规则前端可配置 — 设计文档

> 状态：设计 / 待实现
> 关联代码：`internal/alert`、`internal/scheduler`、`internal/store`、`internal/dashboard`、`cmd/transitmonitor`

## 1. 背景与目标

当前告警规则**只存在于 `config.yaml`**（`cfg.Alerts.Rules`），`cmd/transitmonitor/main.go:174` 启动时灌进 `scheduler.Rules` 后只读，**没有落库、前端无法修改**。而 notifier（钉钉/飞书/Slack/webhook/QQ）配置已经走完整闭环：

```
/settings 页面 ──POST──▶ dashboard.settingsSave
   └─ StationManager 接口(SaveNotifierConfig/NotifierConfigs/SendTestAlert)
        └─ scheduler.SaveNotifierConfig ──加密──▶ store.notifier_config 表
             └─ ReloadNotifiers 热加载到运行中的 Dispatcher
```

本次让**告警规则**复用同一闭环，但规则不含密钥、**不需加密**，走通用 KV 表。

附带修复一个语义问题：`delta_pct` 当前用绝对值比较，规则名 `price-up-5pct` 实际**涨跌都触发**。给价格类规则加 `direction`（up/down/both）字段。

### UI 重新组织

`/settings` 改成 tab 页：顶层一个「告警」tab，其下两个子 tab ——「通知设置」（原 notifier 表单）和「告警规则设置」（新增）。

## 2. 现状梳理（实现前必读）

| 组件 | 现状 | 文件 |
|---|---|---|
| `alert.Rule` | `{Name,Type,Threshold,Enabled}`，无 Direction，无持久化 | `internal/alert/alert.go:41` |
| `alert.Evaluate` | 遍历 rules 匹配 events/probes；`delta_pct` 用 `abs(DeltaPct)>=threshold` | `internal/alert/alert.go:63` |
| `scheduler.Rules` | 启动时由 `New` 注入，构造后**从不修改**；`rulesOfType` 和 `Evaluate(s.Rules,…)` 无锁读取 | `internal/scheduler/scheduler.go:32,58,118,446,489` |
| `evaluateBalanceRules` | 直接发射 `quota_below`/`quota_drop_pct`（不经 Evaluate） | `internal/scheduler/scheduler.go:142` |
| `SaveNotifierConfig`/`ReloadNotifiers` | 加密落库 + 热加载 + 清空 `s.lastAlert` 冷却的范式 | `internal/scheduler/scheduler.go:761,740` |
| store 持久化 notifier | `notifier_config(id,ciphertext,nonce)` 加密 KV，`schema_version` 迁移 | `internal/store/store.go:811,93` |
| `/settings` 页面 | 服务端渲染 HTML + 内联 `fetch()` JS，notifier 表单 | `internal/dashboard/dashboard_settings.go` |
| i18n | zh + en 双 map | `internal/dashboard/i18n.go` |
| 装配 | `sched := scheduler.New(stations, adapters, st, cfg.Alerts.Rules, notifier)` | `cmd/transitmonitor/main.go:174` |

**关键约束**：`s.Rules` 一旦支持热加载，所有读取处必须加锁取快照，否则与轮询 goroutine 数据竞争。

## 3. 设计

### 3.1 持久化策略

- 规则不是密钥 → 用通用 KV 表 `app_settings(key TEXT PK, value TEXT, updated_at)`，**不加密**。
- 存储键：`alert_rules` → JSON `[]alert.Rule`。
- **store 是规则唯一真相源**：首次启动 store 无规则时，把 `config.yaml` 规则 seed 进 store；之后 store 覆盖 config.yaml（与 notifier 的 store-wins 一致）。提供「恢复默认」按钮重新 seed `baseRules`。
- 规则编辑**不依赖 `TRANSMONITOR_ENCRYPTION_KEY`**（notifier 子 tab 仍依赖）。

### 3.2 `direction` 语义

新增 `Rule.Direction`：`""`/`"both"`（默认，涨跌都响）/`"up"`/`"down"`。

`matchDirection(dir, signedDelta) bool`：
- `""`/`"both"` → true
- `"up"` → `signedDelta > 0`
- `"down"` → `signedDelta < 0`

对**有符号 delta** 的规则套用 `abs(delta) >= threshold && matchDirection(dir, delta)`：

| 规则类型 | signedDelta | up 含义 | down 含义 |
|---|---|---|---|
| `delta_pct` | `e.DeltaPct` | 价格上涨 | 价格下跌 |
| `delta_abs` | `e.DeltaAbs` | 上涨 | 下跌 |
| `group_ratio_delta_pct` | `e.DeltaPct` | 倍率上调 | 倍率下调 |
| `probe_markup_pct` | `p.MarkupPct` | 超收（加价） | 少收 |
| `quota_drop_pct` | `dropPct=(prev-curr)/prev*100` | 余额上升（dropPct<0） | 余额下降（默认行为，dropPct>0） |

无符号 delta 的规则（`model_added/removed`、`quota_below`、`endpoint_auth_failed`、`poll_failure_streak`）忽略 direction。

向后兼容：现有 `config.yaml` 无 `direction` key → `""` → both → 行为不变。

## 4. 改动清单

### 4.1 `internal/alert/alert.go`

- `Rule` 加 `Direction string \`yaml:"direction" json:"direction"\``。
- 常量 `RuleDirUp/Down/Both`。
- `matchDirection(dir string, signedDelta float64) bool`。
- `Evaluate` 中 `delta_pct`/`delta_abs`/`group_ratio_delta_pct`/`probe_markup_pct` 触发条件加 `&& matchDirection(r.Direction, delta)`。

### 4.2 `internal/store/store.go`

- `schema_version` 迁移：`CREATE TABLE IF NOT EXISTS app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at DATETIME DEFAULT CURRENT_TIMESTAMP)`，bump version。
- `SetAppSetting(ctx, key, value string) error`（upsert）。
- `GetAppSetting(ctx, key string) (value string, ok bool, err error)`。
- 镜像 `SetNotifierConfig/GetNotifierConfig` 但无加密。

### 4.3 `internal/scheduler/scheduler.go`

- 加 `ruleMu sync.RWMutex`、`baseRules []alert.Rule`。
- `snapshotRules() []alert.Rule`：`RLock` → copy `s.Rules`。替换 `alert.Evaluate(s.Rules,…)`(:446/:489) 与 `rulesOfType` 内迭代为快照。
- `SetBaseRules([]alert.Rule)`：存 `baseRules`（main 装配调用，镜像 `SetBaseNotifierConfig`）。
- `LoadRules(ctx) error`：读 `GetAppSetting("alert_rules")`；有 → unmarshal 赋 `s.Rules`(Lock)；无 → marshal `baseRules` 写 store 并赋值(seed)。
- `ResetRules(ctx) error`：`baseRules` 写回 store + 赋值（「恢复默认」）。
- `AlertRules(ctx) []alert.Rule`：返回快照（供表单）。
- `SaveAlertRules(ctx, []alert.Rule) error`：校验 → marshal → `SetAppSetting` → `Lock` 换 `s.Rules` → 清 `s.lastAlert` 冷却（镜像 `ReloadNotifiers:755-757`）。
- `rulesOfType` 改读快照（`RLock`）。

### 4.4 `internal/dashboard/dashboard_settings.go`

- `settingsHTML` 重构：读 `?tab=`（`notifier` 默认 | `rules`）。渲染顶层 tab bar（「告警」）+ 子 tab bar（通知设置 / 告警规则）。
- 抽 `settingsNotifierCard(lang, nc, encEnabled)` = 现有 notifier 表单（原样搬）。
- 新增 `settingsRulesCard(lang, rules)`：规则表，每行 name/type(select 9 项)/threshold(number)/direction(select both/up/down)/enabled(checkbox)/删除；底部「添加规则」「恢复默认」「保存」。规则不依赖 encKey。
- 内联 JS `tmRulesSave()`：收集行 → `POST /api/settings/rules` JSON → reload；`tmRulesReset()` → `POST /api/settings/rules/reset`。

### 4.5 `internal/dashboard/dashboard.go`

- `New` 加路由：`r.Post("/api/settings/rules", s.settingsRulesSave)`、`r.Post("/api/settings/rules/reset", s.settingsRulesReset)`。
- `settingsRulesSave`：decode `[]alert.Rule` → 断言 `s.mgr.(interface{ SaveAlertRules(context.Context, []alert.Rule) error })` → 调用，校验失败 400。
- `settingsRulesReset`：断言 `ResetRules`。
- `settingsHTML` 取规则：断言 `s.mgr.(interface{ AlertRules(context.Context) []alert.Rule })`。

### 4.6 `internal/dashboard/i18n.go`

zh + en 都加：`section.alert`、`section.alert.notifier`、`section.alert.rules`、`form.rule.name/type/threshold/direction/enabled`、`form.rule.dir.both/up/down`、`btn.add_rule`、`btn.delete_rule`、`btn.reset_rules`、`btn.save_rules`、`settings.rules.saved`、`settings.rules.reset`、`settings.rules.bad_type`、`settings.rules.no.manager`。

### 4.7 `cmd/transitmonitor/main.go`

`sched := scheduler.New(...)` 后加 `sched.SetBaseRules(cfg.Alerts.Rules)` + `sched.LoadRules(context.Background())`（容错 warn），放 `ReloadNotifiers` 附近。

## 5. 校验规则（`SaveAlertRules`）

- `Type` ∈ 9 常量之一，否则 400。
- `Name` 非空；同名允许（不强校验唯一）。
- `Threshold` ≥ 0；无阈值规则忽略。
- `Direction` ∈ `{"","both","up","down"}`。
- `Enabled` 默认 false。

## 6. 验证步骤

1. 构建+单测：
   ```bash
   cd /home/admin/workspace/code/TransitMonitor
   /home/admin/.local/go/bin/go build ./...
   /home/admin/.local/go/bin/go test -race ./internal/alert/... ./internal/store/... ./internal/scheduler/... ./internal/dashboard/...
   make vet && make fmt-check   # gofmt 是反复 CI 红点，必过
   make selftest                 # 仍打印 self-test PASSED
   ```
2. 端到端：`./run.sh` 启动 → `/settings?tab=rules` → 看到 config.yaml 的 5 条规则已 seed → 改 `price-up-5pct` direction=up、threshold=3、禁用 `model-removed`、新增 `probe_markup_pct` → 保存 → 刷新仍在 → 重启进程 → 规则保留（store 落库）→ 触发一次价格下降，验证 `direction=up` 的规则**不**再为下跌告警。
3. 方向单测：`/home/admin/.local/go/bin/go test -run TestDirection ./internal/alert/...`。

## 7. 不做

- 不改 `changedet` 的 diff 逻辑（方向在 alert 层判断）。
- 不加"按站点/按模型过滤"（后续需求）。
- 不改 notifier 子 tab 行为。
