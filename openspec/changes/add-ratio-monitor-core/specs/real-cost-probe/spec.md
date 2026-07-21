## ADDED Requirements

### Requirement: new-api 真实成本探测必须用 sk-key 读 usage delta 反推有效倍率

系统 MUST 用 `sk-` key 对 new-api 站执行探测：① `GET /v1/dashboard/billing/usage` 得 `total_usage=U0`（cents）；② `POST /v1/chat/completions`（非流式、随机短 prompt、`max_tokens=MaxOutputTokens`）捕 `usage{prompt_tokens=P, completion_tokens=C}`；③ 再读 `total_usage=U1`（必要时 500ms 重试）。MUST 计算 `delta_quota=(U1−U0)×(QuotaPerUnit/100)=(U1−U0)×5000`。

#### Scenario: per-token 模型对账

- **WHEN** 声明 `model_ratio mr_d=1.25`、`completion_ratio cr_d=4`、`group_ratio gr_d=1`，探测得 `delta_quota=1500`、`P=100`、`C=1`
- **THEN** `measured_RG` MUST 约等于 `1500/(100+1×4)=14.42`（delta_quota/(P+C×cr_d)）
- **THEN** `measured_input_usd_per_1m` MUST 约等于 `measured_RG×2`
- **THEN** `markup_pct` MUST 按 `(measured_RG − mr_d×gr_d)/(mr_d×gr_d)×100` 计算

#### Scenario: 固定计费模型对账

- **WHEN** 模型为 `quota_type=1`、声明 `model_price_d=0.04`、`gr_d=1`，探测得 `delta_quota=20000`
- **THEN** `measured_usd_per_call` MUST 约等于 `delta_quota/QuotaPerUnit=20000/500000=0.04`
- **THEN** `markup_pct` MUST 按 `(measured − model_price_d×gr_d)/(model_price_d×gr_d)×100` 计算

### Requirement: sub2api 真实成本探测必须用 actual_cost delta 反推有效倍率

系统 MUST 用 `sk-` key 对 sub2api 站执行探测：① `GET /v1/sub2api/billing` 得 `effective_rate_multiplier eff_m`（404→simple）；② `GET /v1/usage` 得 `total.actual_cost=A0`；③ 发非流式 chat 捕 `usage`；④ 再读 `A1`。MUST 计算 `delta_actual_cost=A1−A0`（USD，已含倍率+peak）。

#### Scenario: sub2api 对账

- **WHEN** 声明 `eff_m=0.25`、`base_in=1.5e-7`、`base_out=6e-7`，探测得 `P=100`、`C=1`、`delta_actual_cost=0.000004`
- **THEN** `measured_eff_m` MUST 约等于 `delta_actual_cost/(P×base_in + C×base_out)`
- **THEN** `markup_pct` MUST 按 `(measured_eff_m − eff_m)/eff_m×100` 计算

#### Scenario: simple 模式仍测实际费但 markup 不可派生

- **WHEN** `/v1/sub2api/billing` 返回 404（simple 模式）
- **THEN** `declared_unavailable` MUST 为 true
- **THEN** 系统 MUST 仍计算 `measured_eff_m`（基于 `delta_actual_cost` 与 base 价）但 `markup_pct` MUST 标不可派生

### Requirement: 探测必须受成本护栏约束并支持干跑

系统 MUST 在发送前预估声明成本，当 `>MaxCostCentsPerRun` 时 MUST 拒绝发送；当 `DryRun=true` 时 MUST 只记录声明成本、MUST NOT 发送 chat 请求。

#### Scenario: 超成本护栏拒绝

- **WHEN** 预估声明成本 `>MaxCostCentsPerRun`
- **THEN** 系统 MUST NOT 发送 chat，MUST 记 `error=cost-guardrail-exceeded`

#### Scenario: 干跑

- **WHEN** `DryRun=true`
- **THEN** `CostUSD` MUST 记为声明成本，MUST NOT 产生真实消费

### Requirement: 探测必须去重且调度频率低于抓取

系统 MUST 在 `(station, model)` 窗口内（默认 10 分钟）不重复探测同一对。探测调度默认频率 MUST 低于抓取（探测默认 30–60 分钟，抓取 2–5 分钟）。

#### Scenario: 窗口内去重

- **WHEN** 站 S 模型 M 在 5 分钟内已探测过
- **THEN** 系统在 10 分钟窗口内 MUST NOT 再次探测 (S, M)

### Requirement: 探测必须用随机 prompt 避免缓存命中

系统 MUST 每次探测用随机化 prompt（`PromptSeed`+nonce），确保 `cache_ratio`/缓存折扣不应用，使 measured 反映非缓存的真实计费。

#### Scenario: 随机 prompt

- **WHEN** 连续两次探测同一模型
- **THEN** 两次请求的 prompt 内容 MUST 不同

### Requirement: 探测错误必须分类标注

系统 MUST 对探测失败按类型标注：`model-not-available`、`quota-exhausted`、`no-quota-delta`（U1==U0 经重试仍如此）、`upstream-5xx`（含 429 退避后仍失败）。

#### Scenario: 模型不可用

- **WHEN** chat 请求返回 `model_not_found`
- **THEN** `ProbeResult.error` MUST 为 `model-not-available`

#### Scenario: 无 quota delta

- **WHEN** `U1==U0` 经一次 500ms 重试仍相等
- **THEN** `ProbeResult.error` MUST 为 `no-quota-delta`，MUST NOT 计算 markup

### Requirement: 探测成本与记录必须进仪表盘与审计

系统 MUST 将每次探测的 `CostUSD`、`measured`、`declared`、`markup_pct` 写入 `probe_results` 表与 `audit_log`，且 audit 记录 MUST 不含明文凭据。

#### Scenario: 探测留痕

- **WHEN** 一次探测完成
- **THEN** `probe_results` MUST 有对应行，`audit_log` MUST 记录该探测动作
- **THEN** audit `detail` MUST 经脱敏不含明文 `sk-` key
