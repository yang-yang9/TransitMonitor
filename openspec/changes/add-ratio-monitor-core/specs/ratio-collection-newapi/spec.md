## ADDED Requirements

### Requirement: NewAPIAdapter 必须先探测能力再选最丰富可用抓取路径

系统 MUST 在抓取前调用 `ProbeCapabilities`，依次探测 `/api/status`、`/api/ratio_config`、`/api/pricing`、`/api/user/self/groups`（当配置 PAT）、`/api/option`（当 RootAuth PAT），并产出 `CapabilityReport`，记录每个端点 HTTP 状态与能力位 `HasRatioConfig`/`HasPricing`/`HasOption`/`HasUserGroups`/`HasStatus`/`SelfUseMode`/`QuotaPerUnit`/`USDExchangeRate`。

#### Scenario: ratio_config 开放

- **WHEN** `/api/ratio_config` 返回 200
- **THEN** `CapabilityReport.HasRatioConfig` MUST 为 true
- **THEN** 抓取 MUST 使用其原始 map（model_ratio/completion_ratio/cache_ratio/create_cache_ratio/model_price/group_ratio）

#### Scenario: ratio_config 被禁用

- **WHEN** `/api/ratio_config` 返回 403（`ExposeRatioEnabled` 未开）
- **THEN** `HasRatioConfig` MUST 为 false
- **THEN** 抓取 MUST 降级到 `/api/pricing`

### Requirement: 抓取必须按降级链选择并优雅降级

系统 MUST 按降级链抓取：`ratio_config`(200) → `pricing`(200) → 若两者均无倍率数据则该站无有效观测并标注原因。降级 MUST NOT 视为错误（除非全部端点失败）。

#### Scenario: 403 降级到 pricing

- **WHEN** `/api/ratio_config` 返回 403 且 `/api/pricing` 返回 200
- **THEN** 抓取 MUST 使用 `pricing` 的 `[]Pricing`
- **THEN** 每条观测的 `source_endpoint` MUST 记为 `/api/pricing`

#### Scenario: pricing 为空

- **WHEN** `/api/pricing` 返回 200 但 `data` 为空数组（无已启用通道模型）
- **THEN** 系统 MUST 产出 0 条观测并标注 `no-enabled-models`，MUST NOT 视为抓取失败

#### Scenario: 全部鉴权失败

- **WHEN** `/api/pricing` 返回 401 且 `/api/ratio_config` 返回 403
- **THEN** 系统 MUST 记录 `endpoint_auth_failed`，`CapabilityReport` 标注对应端点状态，MUST 产出 0 条观测

### Requirement: 鉴权头必须按端点类型正确设置

系统 MUST 对公开端点（`/api/status`、未设 `requireAuth` 的 `/api/pricing`）不带鉴权头；对 `/api/user/self/groups` 与 `/api/option` 带 `Authorization: Bearer <PAT>`；对 `/v1/*`（含探测）带 `Authorization: Bearer <sk-key>`（relay 亦接受 `x-api-key`/`x-goog-api-key` 并归一）。

#### Scenario: PAT 端点带 Bearer

- **WHEN** 调用 `/api/user/self/groups` 且配置了 PAT
- **THEN** 请求头 MUST 含 `Authorization: Bearer <PAT>`

#### Scenario: 公开端点无 auth

- **WHEN** 调用 `/api/status`
- **THEN** 请求 MUST NOT 带 `Authorization` 头

### Requirement: 抓取必须只读且不修改站

系统 MUST 仅对 new-api 发起 GET 请求抓取倍率与状态。MUST NOT 调用任何写端点（如 `PUT /api/option/`）。

#### Scenario: 仅 GET

- **WHEN** 抓取一个 new-api 站
- **THEN** 所有抓取请求方法 MUST 为 GET

### Requirement: group_ratio 必须按用户分组优先级取值

系统 MUST 按优先级取 `group_ratio`：`/api/user/self/groups` 的该组 `ratio` > `/api/pricing` 顶层 `group_ratio[组]` > `1.0`。

#### Scenario: 用户分组倍率覆盖顶层

- **WHEN** `/api/user/self/groups` 返回 `vip: {ratio: 0.8}` 且 `/api/pricing` 顶层 `group_ratio.vip=1.0`
- **THEN** 归一化 MUST 用 `0.8`

### Requirement: pricing_version 禁止用于变更检测

系统 MUST NOT 将 `/api/pricing` 的 `pricing_version` 字段作为变更检测依据（其为硬编码常量）。变更检测 MUST 基于实际倍率数值的 diff。

#### Scenario: pricing_version 不触发变更

- **WHEN** 两次抓取的 `pricing_version` 相同但某模型 `model_ratio` 发生变化
- **THEN** 系统 MUST 产生该模型的变更事件
