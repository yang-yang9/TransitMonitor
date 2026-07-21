## ADDED Requirements

### Requirement: 跨站比较矩阵必须以模型×站点的有效 USD/1M 为单元

系统 MUST 构建比较矩阵，单元为 `(model, station) → {input_usd_per_1m, output_usd_per_1m}`。MUST 支持按 `model` 筛选与按有效价升序排序。

#### Scenario: 两站同模型对比

- **WHEN** new-api 站 s1 与 sub2api 站 s2 均有模型 X，`s1.input=2.5`、`s2.input=2.0`
- **THEN** 矩阵 MUST 同时显示 `s1=2.5` 与 `s2=2.0`
- **THEN** 按价升序时 s2 MUST 排在 s1 之前

### Requirement: 矩阵必须禁止比较原生倍率

系统 MUST NOT 在矩阵中显示或比较 new-api `model_ratio` 与 sub2api `rate_multiplier`（单位不可比）。比较 MUST 仅基于 `input/output_usd_per_1m`。

#### Scenario: 不暴露原生倍率

- **WHEN** 查询矩阵
- **THEN** 响应 MUST NOT 包含 `native_ratio` 的跨站数值比较

### Requirement: 不可派生行必须显示标签而非数值

系统 MUST 对 `sentinel` 为 `fixed-price (per-call)`/`unconfigured-37.5`/`declared-unavailable (simple mode)`/`missing-base-price` 的矩阵单元格显示对应标签字符串，MUST NOT 显示 `0` 当作真实价。

#### Scenario: 不可派生显示标签

- **WHEN** 站 s1 模型 dall-e-3 为 `fixed-price (per-call)`
- **THEN** 矩阵 (dall-e-3, s1) 单元格 MUST 显示 `fixed-price (per-call)` 而非 `0`
