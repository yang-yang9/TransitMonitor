## ADDED Requirements

### Requirement: 系统必须提供覆盖站/倍率/变更/矩阵/探测/告警的 HTTP API

系统 MUST 提供 HTTP API：`GET/POST/PUT/DELETE /api/stations`、`GET /api/stations/{id}/health`、`GET /api/ratios?station=&model=&group=`、`GET /api/changes?since=&station=&severity=`、`GET /api/matrix?model=`、`GET /api/probes?station=&model=`、`GET/POST /api/alerts`。

#### Scenario: 查询当前倍率

- **WHEN** 调用 `GET /api/ratios?station=s1&model=gpt-4o`
- **THEN** 响应 MUST 为该站该模型的归一化 `RatioObservation`（含 `input_usd_per_1m`、`output_usd_per_1m`、`sentinel` 等）

#### Scenario: 站健康

- **WHEN** 调用 `GET /api/stations/s1/health`
- **THEN** 响应 MUST 含上次抓取时间、`CapabilityReport` 能力位、各端点 HTTP 状态

### Requirement: 系统必须提供服务端渲染的 HTML 页面

系统 MUST 用 `html/template` 提供页面：`/`（总览）、`/stations/{id}`、`/matrix`、`/changes`、`/probes`、`/alerts`。MUST NOT 引入前端构建链。

#### Scenario: 总览页

- **WHEN** 访问 `/`
- **THEN** MUST 返回 HTML，列出各站健康与上次抓取状态

### Requirement: 仪表盘必须受鉴权保护且响应脱敏

系统 MUST 在非 localhost 访问时要求 `Authorization: Bearer <TRANSMONITOR_DASHBOARD_TOKEN>`（token 可为空时仅允许 localhost）。所有响应中的凭据字段 MUST 经脱敏。

#### Scenario: 非 localhost 无 token 拒绝

- **WHEN** 来自非 localhost 的请求且无有效 bearer token
- **THEN** 系统 MUST 返回 `401`

#### Scenario: 响应脱敏

- **WHEN** 调用 `GET /api/stations`
- **THEN** 响应中 `auth.api_key` 等 MUST 为脱敏掩码，MUST NOT 含明文

### Requirement: 矩阵端点必须返回模型×站点的有效 USD/1M

系统 MUST 在 `GET /api/matrix?model=X` 返回该模型在各站的 `input_usd_per_1m`/`output_usd_per_1m`，对不可派生行返回其标签字符串而非数值。

#### Scenario: 矩阵返回有效价

- **WHEN** 调用 `GET /api/matrix?model=gpt-4o` 且有两站
- **THEN** 响应 MUST 含每站的 `input_usd_per_1m` 数值或标签
