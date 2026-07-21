## ADDED Requirements

### Requirement: new-api 倍率必须按 ratio×2×group_ratio 归一化为有效 USD/1M token

系统 SHALL 将 new-api 站的 `model_ratio`、`completion_ratio`、`group_ratio` 归一化为每百万 token 美元价格，单位约定 `1 ratio = $2/1M tokens`（`QuotaPerUnit=500000`）。MUST 满足：
- `input_usd_per_1m = model_ratio × 2 × group_ratio`
- `output_usd_per_1m = model_ratio × completion_ratio × 2 × group_ratio`
- `cache_read_usd_per_1m = model_ratio × cache_ratio × 2 × group_ratio`（当 `cache_ratio` 缺失时 MUST 等于 `input_usd_per_1m`）
- `cache_write_usd_per_1m = model_ratio × create_cache_ratio × 2 × group_ratio`

`group_ratio` 取值优先级 MUST 为：`/api/user/self/groups` 返回的该组 `ratio` > `/api/pricing` 顶层 `group_ratio[组]` > `1.0`。

#### Scenario: 普通 per-token 模型归一化

- **WHEN** 站点为 new-api，某模型 `model_ratio=1.25`、`completion_ratio=4`、`group_ratio=1`、`quota_type=0`
- **THEN** `input_usd_per_1m` MUST 等于 `2.5`（`1.25×2×1`）
- **THEN** `output_usd_per_1m` MUST 等于 `10`（`1.25×4×2×1`）

#### Scenario: 应用用户分组倍率

- **WHEN** `/api/user/self/groups` 返回分组 `vip` 的 `ratio=0.8`，模型 `model_ratio=1.25`、`completion_ratio=4`
- **THEN** 计算 MUST 使用 `group_ratio=0.8`
- **THEN** `input_usd_per_1m` MUST 等于 `2.0`（`1.25×2×0.8`）
- **THEN** 在缺少用户分组倍率时 MUST 回退到 `/api/pricing` 顶层 `group_ratio` 映射，再缺省 MUST 取 `1.0`

#### Scenario: cache_ratio 缺失时回退为 input

- **WHEN** 模型 `model_ratio=1.25`、`group_ratio=1` 且 `cache_ratio` 未提供
- **THEN** `cache_read_usd_per_1m` MUST 等于 `input_usd_per_1m`（即 `2.5`）

#### Scenario: completion_ratio 缺失时推断为 1.0

- **WHEN** 从原始 `ratio_config` map 读取某模型但无 `completion_ratio` 条目
- **THEN** 系统 MUST 取 `completion_ratio=1.0` 并在 `sentinel`/标签打 `completion_ratio=inferred(1.0)`
- **THEN** `output_usd_per_1m` MUST 等于 `input_usd_per_1m`

### Requirement: new-api 固定计费模型必须标记为每次调用计价且 USD/1M 不可派生

系统 SHALL 对 `quota_type=1`（如 `mj_*`、`dall-e-3`、`suno_*`、`veo-*`、`sora-2`）的模型，MUST 计算 `fixed_price_usd = model_price × group_ratio`，MUST 将 `input_usd_per_1m` 与 `output_usd_per_1m` 置为 `0`，并 MUST 打标签 `fixed-price (per-call)`。MUST NOT 将此类模型纳入按 USD/1M 的跨站比较矩阵。

#### Scenario: dall-e-3 固定计价

- **WHEN** 模型 `dall-e-3` 的 `quota_type=1`、`model_price=0.04`、`group_ratio=1`
- **THEN** `fixed_price_usd` MUST 等于 `0.04`
- **THEN** `input_usd_per_1m` MUST 为 `0` 且 MUST 打标 `fixed-price (per-call)`
- **THEN** 该模型 MUST NOT 出现在 USD/1M 跨站矩阵的数值行

### Requirement: new-api 自用模式 37.5 哨兵必须被识别并排除

系统 MUST 在 `/api/status` 报告 `self_use_mode_enabled=true`（或 `GetModelRatio` 第二返回值为 true）时，将 `native_ratio==37.5` 且模型不在站点已知倍率 map 中的观测标记 `sentinel=unconfigured-37.5`。MUST NOT 将 `37.5` 当作真实价格参与存储/比较，MUST NOT 对其产生变更事件。

#### Scenario: 自用模式下未知模型返回 37.5

- **WHEN** `self_use_mode_enabled=true` 且某模型不在 `model_ratio` map 中，站点返回 `37.5`
- **THEN** `sentinel` MUST 等于 `unconfigured-37.5`
- **THEN** `input_usd_per_1m` MUST 为 `0` 且 MUST 被排除出比较矩阵
- **THEN** MUST NOT 产生该模型的变更事件

#### Scenario: 非自用模式下 37.5 不视为哨兵

- **WHEN** `self_use_mode_enabled=false` 且站点对某模型真实返回 `37.5`
- **THEN** 系统 MUST 将其作为普通 `model_ratio=37.5` 归一化（`input_usd_per_1m = 37.5×2×group_ratio`）

### Requirement: sub2api 倍率必须按 LiteLLM per-token 单价 × 有效倍率 归一化

系统 SHALL 将 sub2api 的 `effective_rate_multiplier`（已含 peak 与每用户覆写，即 `resolved_rate_multiplier × applied_peak_multiplier`）与 LiteLLM 每 token USD 单价相乘。MUST 满足：
- `input_usd_per_1m = input_cost_per_token × 1e6 × effective_rate_multiplier`
- `output_usd_per_1m = output_cost_per_token × 1e6 × effective_rate_multiplier`
- `cache_read_usd_per_1m = cache_read_input_token_cost × 1e6 × effective_rate_multiplier`
- `cache_write_usd_per_1m = cache_creation_input_token_cost × 1e6 × effective_rate_multiplier`

基础 per-token USD 价 MUST 优先使用站内 `channel_model_pricing` 覆盖值，缺失时 MUST 回退到 vendored LiteLLM JSON，再缺失 MUST 打标 `missing-base-price` 且 USD/1M 不可派生。

#### Scenario: sub2api 普通模型归一化

- **WHEN** 站点为 sub2api，`effective_rate_multiplier=0.25`、`input_cost_per_token=1.5e-7`、`output_cost_per_token=6e-7`
- **THEN** `input_usd_per_1m` MUST 等于 `0.0375`（`1.5e-7×1e6×0.25`）
- **THEN** `output_usd_per_1m` MUST 等于 `0.15`（`6e-7×1e6×0.25`）

#### Scenario: peak 倍率生效

- **WHEN** `peak_rate_enabled=true`、`applied_peak_multiplier=1.5`、`resolved_rate_multiplier=0.25`
- **THEN** `effective_rate_multiplier` MUST 等于 `0.375`（`0.25×1.5`）
- **THEN** `peak_info` MUST 非空且包含峰段时间区间

#### Scenario: 基础价缺失

- **WHEN** 某模型既无 `channel_model_pricing` 覆盖、也不在 LiteLLM JSON
- **THEN** MUST 打标 `missing-base-price`
- **THEN** `input_usd_per_1m` MUST 不可派生（置 `0`）

#### Scenario: 简单模式

- **WHEN** `/v1/sub2api/billing` 返回 HTTP `404`（RunMode=simple）
- **THEN** 该站所有观测 MUST 置 `declared_unavailable=true` 并打标 `declared-unavailable (simple mode)`
- **THEN** USD/1M MUST 不可派生（置 `0`）

### Requirement: 跨站比较必须基于有效 USD/1M 而非原生倍率

系统 MUST NOT 直接比较 new-api 的 `model_ratio` 与 sub2api 的 `rate_multiplier`（二者单位不同）。MUST 仅比较 `input_usd_per_1m` 与 `output_usd_per_1m`。系统 MUST 在比较矩阵中对 `fixed-price (per-call)`、`unconfigured-37.5`、`declared-unavailable (simple mode)`、`missing-base-price` 的行显示其标签而非数值。

#### Scenario: 两种站点同模型比较

- **WHEN** new-api 站模型 X 的 `input_usd_per_1m=2.5`，sub2api 站同模型 `input_usd_per_1m=2.0`
- **THEN** 比较矩阵 MUST 显示两个有效 USD/1M 数值
- **THEN** MUST NOT 显示或比较原生 `ratio` 与 `rate_multiplier`

#### Scenario: 不可派生行显示标签

- **WHEN** 某站某模型 `sentinel=fixed-price (per-call)`
- **THEN** 矩阵对应单元格 MUST 显示标签 `fixed-price (per-call)` 而非 USD 数值
