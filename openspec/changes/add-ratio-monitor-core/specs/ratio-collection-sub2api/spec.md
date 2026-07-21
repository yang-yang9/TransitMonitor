## ADDED Requirements

### Requirement: Sub2APIAdapter 必须先探测能力再选最丰富可用抓取路径

系统 MUST 在抓取前调用 `ProbeCapabilities`，探测 `/v1/sub2api/billing`、`/api/v1/admin/groups`+`/api/v1/admin/channels`（当配置 admin 凭据）、`/api/v1/channels/available`（当 JWT + available-channels flag）、`/v1/models`，并产出 `CapabilityReport`，记录 `HasBilling`/`HasAdminGroups`/`HasAdminChannels`/`HasUserChannels`/`SimpleMode`。

#### Scenario: billing 可用且 admin 凭据有效

- **WHEN** `/v1/sub2api/billing` 返回 200 且 admin 凭据有效
- **THEN** `HasBilling` 与 `HasAdminGroups`/`HasAdminChannels` MUST 为 true
- **THEN** 抓取 MUST 同时使用本 key 有效倍率与全组/全通道倍率

#### Scenario: simple 模式

- **WHEN** `/v1/sub2api/billing` 返回 404（RunMode=simple）
- **THEN** `SimpleMode` MUST 为 true 且 `HasBilling` MUST 为 false
- **THEN** 该站所有观测 MUST 置 `declared_unavailable=true` 并打标 `declared-unavailable (simple mode)`

### Requirement: 抓取必须按降级链选择并优雅降级

系统 MUST 按降级链抓取：`billing`(本 key 有效倍率) → admin `groups`+`channels`（全组倍率 + per-token 价 + 每用户覆写）→ user `channels/available`（组+价一体）。当无 admin 凭据且非 user 模式时，MUST 仅能读本 key 的 `billing` 有效倍率。

#### Scenario: 仅 sk-key、无 admin

- **WHEN** 仅配置 `api_key`（无 admin_api_key/JWT）
- **THEN** 系统 MUST 仅读 `/v1/sub2api/billing` 得到本 key 的 `effective_rate_multiplier`
- **THEN** 其它组的倍率 MUST 不可得（标注原因），MUST NOT 视为错误

#### Scenario: user channels/available 降级

- **WHEN** 无 admin 凭据但配置 user JWT 且站开启 available-channels flag
- **THEN** 系统 MUST 降级使用 `/api/v1/channels/available`（组倍率 + 模型价一体）

### Requirement: 鉴权头必须按模式正确设置且禁用废弃方式

系统 MUST 对 sk-key 模式用 `Authorization: Bearer <sk>` 或 `x-api-key`/`x-goog-api-key`；对 admin 模式用 `x-api-key: <admin-api-key>` 或 `Authorization: Bearer <admin-jwt>`。MUST NOT 使用 `?key=`/`?api_key=` 查询参数（sub2api 已废弃返回 400）。

#### Scenario: sk-key 模式头

- **WHEN** 调用 `/v1/sub2api/billing` 且仅有 sk-key
- **THEN** 请求 MUST 含 `Authorization: Bearer <sk>`（或等价 x-api-key）
- **THEN** 请求 MUST NOT 含 `?key=` 查询参数

#### Scenario: admin 模式头

- **WHEN** 调用 `/api/v1/admin/groups` 且配置了 admin_api_key
- **THEN** 请求 MUST 含 `x-api-key: <admin-api-key>`

### Requirement: 基础 per-token 价必须按优先级取值

系统 MUST 按优先级取 sub2api 基础 per-token USD 价：站内 `channel_model_pricing` 覆盖 > vendored LiteLLM JSON > 标 `missing-base-price`。

#### Scenario: channel override 优先

- **WHEN** 某模型在 `/api/v1/admin/channels` 有 `channel_model_pricing` 覆盖且 LiteLLM 也有该模型
- **THEN** 归一化 MUST 使用 channel 覆盖值

#### Scenario: model-pricing 端点缺失

- **WHEN** `/api/v1/admin/channels/model-pricing?model=X` 返回 404 且 LiteLLM 无该模型
- **THEN** 该模型 MUST 打标 `missing-base-price` 且 USD/1M 不可派生

### Requirement: 抓取必须只读

系统 MUST 仅对 sub2api 发起 GET 抓取。MUST NOT 调用任何写端点（如 `PUT /api/v1/admin/groups/:id`）。

#### Scenario: 仅 GET

- **WHEN** 抓取一个 sub2api 站
- **THEN** 所有抓取请求方法 MUST 为 GET
