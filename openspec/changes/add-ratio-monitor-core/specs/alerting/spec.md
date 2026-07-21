## ADDED Requirements

### Requirement: 系统必须支持多种告警规则类型并在 diff/探测后评估

系统 MUST 支持规则类型：`delta_pct`（有效价相对变化超阈）、`delta_abs`（绝对变化超阈）、`model_added`、`model_removed`、`probe_markup_pct`（探测 markup 超阈）、`endpoint_auth_failed`（先前 OK 端点现 401/403）、`poll_failure_streak`（连续 N 次抓取失败）。MUST 在每次 diff 后评估变更类规则、每次探测后评估 `probe_markup_pct`。

#### Scenario: 涨价超阈触发

- **WHEN** 规则 `delta_pct` 阈值 `10`，某模型 `input_usd_per_1m` 涨 `25%`
- **THEN** MUST 产生并投递一条告警事件

#### Scenario: 探测 markup 超阈触发

- **WHEN** 规则 `probe_markup_pct` 阈值 `5`，探测得 `markup_pct=12`
- **THEN** MUST 产生并投递告警事件

### Requirement: 钉钉 notifier 必须正确签名

系统 MUST 对钉钉 webhook 请求按 `timestamp+"\n"+secret` 用 HMAC-SHA256 base64 计算签名，并在 URL 追加 `&timestamp=&sign=` 查询参数。消息 MUST 为 markdown 格式（含 `title` 与 `text`）。

#### Scenario: 签名正确

- **WHEN** 投递一条钉钉告警
- **THEN** 请求 URL MUST 含合法 `timestamp` 与 `sign`
- **THEN** 请求体 MUST 为 markdown 消息结构

### Requirement: 通用 webhook notifier 必须投递结构化 JSON

系统 MUST 对通用 webhook 发 `POST` JSON，载荷 MUST 含 `station`/`model`/`field`/`old`/`new`/`delta_pct`/`severity`/`observed_at`。

#### Scenario: 通用 webhook 载荷

- **WHEN** 投递一条通用 webhook 告警
- **THEN** 请求体 MUST 为含上述字段的 JSON

### Requirement: 投递失败必须退避重试并记录

系统 MUST 对投递失败的 `AlertEvent` 退避重试，`AlertEvent` MUST 记录 `sent` 状态与 `error`。webhook secret 与钉钉 secret MUST 加密存储。

#### Scenario: 投递失败重试

- **WHEN** notifier 首次投递返回 5xx
- **THEN** 系统 MUST 退避后重试，并在 `AlertEvent` 记录失败原因

### Requirement: 告警规则必须可启停

系统 MUST 支持规则的 `enabled` 开关；`enabled=false` 的规则 MUST NOT 触发告警。

#### Scenario: 停用的规则不触发

- **WHEN** 规则 `delta_pct` 的 `enabled=false`，即便某模型涨 `50%`
- **THEN** MUST NOT 产生该规则的告警事件
