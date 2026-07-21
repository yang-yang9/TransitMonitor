# TransitMonitor 设计文档

> SDD 真源在 `openspec/changes/add-ratio-monitor-core/specs/<capability>/spec.md`（Requirements + Scenario WHEN/THEN）；
> 上游契约速查在 `docs/upstream-contract.md`。本文是设计正文，规约的可读性补充。

## 1. 概述与问题

中转站（new-api / sub2api）会**静默调整每个模型的倍率**，直接改变真实计费成本；且两类站用**不同的倍率体系 + 不同的 API 契约**：

| | new-api（one-api 系） | sub2api（subscription→API） |
|---|---|---|
| 倍率单位 | `ratio`，`1 = $2/1M tokens`（`QuotaPerUnit=500000`） | `rate_multiplier`，对 LiteLLM 官方 per-token USD 的折扣 |
| 基础价来源 | 站内 `model_ratio` map（DB 可变，`options` 表） | LiteLLM JSON（~24h 自动刷新）+ 站内 channel 覆盖 |
| 是否可比 | 二者单位不同，**不可直接比 native 倍率** | 同左 |

TransitMonitor 用 **Adapter 抽象 + 统一归一化到"有效 USD/1M token"** 解决可比性；用**真实成本探测**解决"隐藏倍率站无法抓取"的问题。

## 2. 架构总览

```
            ┌─────────────┐   config.yaml        ┌──────────────┐
            │  config/    │ ───────────────────▶ │  scheduler  │  每站 poller + 探测调度
            │ (load+enc)  │                      │  (jitter/    │
            └─────────────┘                      │   backoff)  │
                                                 └──────┬───────┘
                ┌───────────────────────────────────────┼──────────────────────────────┐
                ▼                                       ▼                              ▼
        ┌──────────────┐  ProbeCapabilities   ┌──────────────┐   ProbeCapabilities ┌──────────────┐
        │ NewAPIAdapter│ ────▶ new-api 站     │ Sub2APIAdapter│ ────▶ sub2api 站    │   probe      │
        │  (GET 抓取)  │ ◀─── RawSnapshot     │  (GET 抓取)  │ ◀─── RawSnapshot   │ (sk-key chat)│
        └──────┬───────┘                      └──────┬───────┘                    └──────┬───────┘
               └────────────┬────────────────────────┘                                   │
                             ▼                                                            │
                     ┌──────────────┐  []RatioObservation                                  │
                     │  normalize   │ ◀──────────────────────────────────────────────────┤
                     │  (纯函数)    │  (usage-delta 反推 measured)                          │
                     └──────┬───────┘                                                       │
                             │                                                               │
              ┌──────────────┴───────────────┐                              ┌───────────────┴──┐
              ▼                                ▼                              ▼                   ▼
        ┌──────────┐                   ┌────────────┐                  ┌──────────┐        ┌──────────┐
        │  store   │ ◀── 写时序 ────── │ changedet  │ ── ChangeEvent ─▶│  alert   │ ──▶ 钉钉/webhook
        │ (SQLite) │                   │  (diff)    │                  │ (rules)  │
        └────┬─────┘                   └────────────┘                  └──────────┘
             │                                                                ▲
             └────────────────────── 读 ───────────────────────────────────┐ │
                                                                          ▼ │
                                                                   ┌────────────┐
                                                                   │ dashboard  │ HTML + JSON API
                                                                   │ (chi+tmpl) │
                                                                   └────────────┘
```

数据流：调度器按 `PollInterval` 触发 → adapter `ProbeCapabilities` + `FetchRatios` → `normalize` 归一化为 `[]RatioObservation` → `store` 写时序 → `changedet` diff 上次 → `ChangeEvent` → `alert` 评估规则 → notifier。探测调度独立且更稀疏 → `probe` 发真实 chat、读 usage delta 反推 measured → 对账 markup → `store` + `alert`。`dashboard` 读 store 提供视图。

## 3. 领域模型（`internal/domain`，节选；完整定义见规约）

- `Station`：`ID/Name/BaseURL/Kind(newapi|sub2api)/Auth/PollInterval/Probe/Tags/Enabled`。
- `AuthConfig`：new-api 用 `PAT`+`APIKey(sk)`+`Group`；sub2api 用 `AdminAPIKey`/`AdminEmail/Pass`/`JWT`/`APIKey(sk)`+`Group`。
- `CapabilityReport`：每端点 `EndpointStatus` + 能力位（`HasRatioConfig`/`HasPricing`/`HasOption`/`HasUserGroups`/`HasBilling`/`HasAdminGroups`/`HasAdminChannels`/`HasUserChannels`/`SimpleMode`/`SelfUseMode`/`QuotaPerUnit`/`USDExchangeRate`）。
- `RawSnapshot`：`StationID/ObservedAt/EndpointsUsed/EndpointStatuses/RawPayloads/Capabilities`。
- `RatioObservation`：`StationID/GroupName/ModelName/NativeRatio/NativeRatioKind/QuotaType/InputUSDPer1M/OutputUSDPer1M/CacheReadUSDPer1M/CacheWriteUSDPer1M/FixedPriceUSD/CompletionRatio/PeakInfo/Sentinel/DeclaredUnavailable/ObservedAt/SourceEndpoint`。
- `ChangeEvent`：`StationID/Group/Model/Field/Old/New/DeltaAbs/DeltaPct/ObservedAt/Severity`。
- `ProbeResult`：`StationID/Model/TokensIn/TokensOut/DeclaredNativeRatio/DeclaredEffectiveUSDPer1M/MeasuredUSDPer1M/MarkupPct/CostUSD/DeclaredUnavailable/ObservedAt/Error`。
- `Adapter` 接口：`ProbeCapabilities(ctx)→CapabilityReport`、`FetchRatios(ctx,caps)→(RawSnapshot,[]RatioObservation,error)`、`Normalize(RawSnapshot)→[]RatioObservation`（Normalize 亦作包级纯函数供表测试）。

## 4. 归一化数学（`internal/normalize`，纯函数；详 `specs/normalization/spec.md`）

new-api per-token（`quota_type=0`）：
```
input  = model_ratio × 2 × group_ratio
output = model_ratio × completion_ratio × 2 × group_ratio
cache_read  = model_ratio × cache_ratio × 2 × group_ratio        (cache_ratio 缺失=input)
cache_write = model_ratio × create_cache_ratio × 2 × group_ratio
group_ratio 优先级: /api/user/self/groups > /api/pricing 顶层 group_ratio[组] > 1.0
completion_ratio 缺失→1.0, 标 completion_ratio=inferred(1.0)
```
new-api 固定计价（`quota_type=1`，mj_*/dall-e-3/suno_*/veo-*/sora-2）：`fixed_price_usd = model_price × group_ratio`；USD/1M 不可派生，标 `fixed-price (per-call)`，排除出矩阵。
new-api 37.5 哨兵：`self_use_mode_enabled && ratio==37.5 && 模型不在已知map` → `sentinel=unconfigured-37.5`，USD/1M=0，排除出矩阵，不产变更事件；非 self-use 下 37.5 当普通 ratio。
sub2api：
```
input  = input_cost_per_token  × 1e6 × effective_rate_multiplier   (eff = resolved × applied_peak)
output = output_cost_per_token × 1e6 × effective_rate_multiplier
simple 模式(billing 404) → declared_unavailable=true, 标 declared-unavailable (simple mode)
基础价缺失 → 标 missing-base-price
```
跨站公平比较：**只比 `input/output_usd_per_1m`**，禁止比 native_ratio 与 rate_multiplier；不可派生行显示标签。

## 5. 真实成本探测（`internal/probe`；详 `specs/real-cost-probe/spec.md`）

- **new-api**（sk-key）：读 `/v1/dashboard/billing/usage`→U0(cents) → 发非流式 `/v1/chat/completions`（随机短 prompt 避缓存，`max_tokens=MaxOutputTokens`）捕 `usage{P,C}` → 读 U1 → `delta_quota=(U1−U0)×5000`。
  - per-token：`measured_RG = delta_quota/(P+C×cr_d)`；`measured_input = measured_RG×2`；`markup_pct = (measured_RG − mr_d×gr_d)/(mr_d×gr_d)×100`。
  - fixed：`measured_usd_per_call = delta_quota/QuotaPerUnit`；`markup_pct = (measured − model_price_d×gr_d)/(model_price_d×gr_d)×100`。
- **sub2api**（sk-key）：读 `/v1/sub2api/billing`→eff_m（404→simple）→ 读 `/v1/usage`→A0 → 发 chat → 读 A1 → `delta_actual_cost=A1−A0`。
  - `measured_eff_m = delta_actual_cost/(P×base_in + C×base_out)`；`markup_pct = (measured_eff_m − eff_m)/eff_m×100`；base 价优先 channel override > LiteLLM。
- 安全：`MaxInputTokens/MaxOutputTokens` 硬上限、`MaxCostCentsPerRun` 预估拦截、`DryRun`、`(station,model)` 窗口去重、调度频率低于抓取（30–60min vs 2–5min）、CostUSD 进仪表盘+审计、错误分类（`model-not-available`/`quota-exhausted`/`no-quota-delta`/`upstream-5xx`）。

## 6. 存储与保留（`internal/store`；详 `specs/storage-retention/spec.md`）

SQLite（modernc.org/sqlite，无 CGO），WAL+busy_timeout+foreign_keys。表：`stations`/`credentials`(AES-GCM ciphertext+nonce)/`ratio_observations`(索引 station+model+observed_at DESC)/`snapshots`(TTL)/`change_events`/`probe_results`/`alert_rules`/`alert_events`/`audit_log`。迁移用 `embed migrations/*.sql`+`schema_version` 幂等。保留：snapshots 留 N 天（默认7）；ratio_observations 留 M 天（默认30）后按 `(station,model,hour)` 聚合 avg/min/max；`DownsampleAndRetain(now)` 幂等。

## 7. 调度（`internal/scheduler`）

每站一 goroutine，各自 `PollInterval≥2min`，±10-20% 抖动；传输错/5xx/429 指数退避有上限，4xx（除429）不重试；探测调度独立且更稀疏，`(station,model)` 窗口去重；`SIGINT/SIGTERM` 取消 root context→等 in-flight（带超时）→flush 告警→关 DB。时钟经 `Clock` 接口注入以便测试。

## 8. 仪表盘与 API（`internal/dashboard`；详 `specs/dashboard/spec.md`）

chi + `html/template` 服务端渲染 + JSON API。路由：`/api/stations`(CRUD)、`/api/stations/{id}/health`、`/api/ratios`、`/api/changes`、`/api/matrix`、`/api/probes`、`/api/alerts`；HTML 页 `/`、`/stations/:id`、`/matrix`、`/changes`、`/probes`、`/alerts`。鉴权：非 localhost 要求 `Authorization: Bearer <TRANSMONITOR_DASHBOARD_TOKEN>`；响应凭据字段脱敏。

## 9. 告警（`internal/alert`；详 `specs/alerting/spec.md`）

规则 `delta_pct`/`delta_abs`/`model_added`/`model_removed`/`probe_markup_pct`/`endpoint_auth_failed`/`poll_failure_streak`；diff 后与探测后评估。notifier：钉钉 webhook（markdown + HMAC-SHA256 签名 timestamp+secret）+ 通用 webhook（POST JSON）；`AlertEvent` 退避重试记 `sent/error`；secret 加密存；规则可启停。

## 10. 安全

- 凭据 AES-GCM 静态加密（env `TRANSMONITOR_ENCRYPTION_KEY`），`credentials` 表只存 ciphertext+nonce。
- `secrets.Redact` 包裹所有被记日志/audit 的凭据字段（`sk-***`/`pat-***`）。
- 抓取只读 GET；唯一写站动作 = 探测的真实小 chat（受护栏、可干跑、可停、去重）。
- dashboard 鉴权 + 凭据响应脱敏。

## 11. SDD/TDD 流程

- SDD：`openspec/changes/add-ratio-monitor-core/specs/<capability>/spec.md` 为真源，先于代码；镜像 sub2api 的 "spec-driven" 格式（`## ADDED Requirements` → `### Requirement:` SHALL → `#### Scenario:` WHEN/THEN）。
- TDD：每能力把 Scenario 翻译为表测试 → RED → 实现 → GREEN → REFACTOR。分层：Tier0 纯函数(normalize/changedet/probe 对账)→Tier1 adapter+httptest→Tier2 store→Tier3 scheduler+假时钟→Tier4 dashboard→Tier5 E2E 双 mock 站。

## 12. 依赖与部署

依赖最小：`modernc.org/sqlite`、`gopkg.in/yaml.v3`、`github.com/go-chi/chi/v5`、`golang.org/x/time`；余 stdlib。单二进制（无 CGO）；`make build` 出 `transitmonitor`。env：`TRANSMONITOR_ENCRYPTION_KEY`（必填，当有凭据）、`TRANSMONITOR_DASHBOARD_TOKEN`（可选）。
