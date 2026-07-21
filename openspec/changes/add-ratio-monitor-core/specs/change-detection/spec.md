## ADDED Requirements

### Requirement: 系统必须 diff 前后快照产出结构化变更事件

系统 MUST 将本次抓取的 `[]RatioObservation` 与上一次（按 `station_id+group_name+model_name` 对齐）diff，产出 `ChangeEvent`，每事件 MUST 含 `station_id`/`group`/`model`/`field`/`old`/`new`/`delta_abs`/`delta_pct`/`observed_at`/`severity`。`field` MUST 为 `input_usd_per_1m`、`output_usd_per_1m`、`native_ratio`、`presence`、`sentinel_flip` 之一。

#### Scenario: 有效价变化超阈值

- **WHEN** 模型 X 上次 `input_usd_per_1m=2.0`、本次 `=2.5`
- **THEN** MUST 产生 `field=input_usd_per_1m` 的 ChangeEvent
- **THEN** `delta_abs` MUST 为 `0.5`，`delta_pct` MUST 为 `25`

### Requirement: 变更类型必须覆盖增删与哨兵翻转

系统 MUST 识别：`model_added`（新模型出现）、`model_removed`（模型消失）、`sentinel_flip`（普通↔哨兵状态翻转，如 `unconfigured-37.5`↔正常）。

#### Scenario: 新增模型

- **WHEN** 本次出现模型 Y 但上次无
- **THEN** MUST 产生 `field=presence`、`new=added` 的 ChangeEvent

#### Scenario: 模型消失

- **WHEN** 上次有模型 Z 但本次无
- **THEN** MUST 产生 `field=presence`、`new=removed` 的 ChangeEvent

#### Scenario: 哨兵翻转

- **WHEN** 模型 W 上次为普通 `model_ratio=2`、本次在 self-use 模式下变 `unconfigured-37.5`
- **THEN** MUST 产生 `field=sentinel_flip` 的 ChangeEvent

### Requirement: diff 必须幂等

系统 MUST 对相同快照的重复 diff 产生 0 个 ChangeEvent。

#### Scenario: 相同快照

- **WHEN** 本次与上次 RatioObservation 集合完全相同
- **THEN** MUST 产生 0 个 ChangeEvent

### Requirement: 不可派生行不得参与值变更比较

系统 MUST NOT 对 `sentinel` 为 `unconfigured-37.5`/`fixed-price (per-call)`/`declared-unavailable (simple mode)`/`missing-base-price` 的行产生值变更事件（`input_usd_per_1m` 置 0 不代表真实价变化）。

#### Scenario: 哨兵行不触发值变更

- **WHEN** 某行 `sentinel=unconfigured-37.5` 在两次抓取中均如此
- **THEN** MUST NOT 产生该行的值变更事件

### Requirement: 严重度必须按相对变化分级

系统 MUST 按 `delta_pct` 分级（可配置，默认 `>5%` 为 `warning`、`>20%` 为 `critical`），`model_removed` 默认 `warning`，`endpoint_auth_failed`/`poll_failure_streak` 默认 `critical`。

#### Scenario: 大幅涨价定级 critical

- **WHEN** 某模型 `input_usd_per_1m` 涨 `30%`
- **THEN** 该事件 `severity` MUST 为 `critical`
