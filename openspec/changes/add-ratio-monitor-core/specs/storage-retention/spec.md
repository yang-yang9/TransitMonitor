## ADDED Requirements

### Requirement: 存储必须包含完整 schema 并用 embed 迁移幂等应用

系统 MUST 用 SQLite（`modernc.org/sqlite`，无 CGO）维护表 `stations`、`credentials`、`ratio_observations`、`snapshots`、`change_events`、`probe_results`、`alert_rules`、`alert_events`、`audit_log`。迁移 MUST 通过 `embed migrations/*.sql` + `schema_version` 表顺序应用，且 MUST 幂等（重复运行不报错、不丢数据）。

#### Scenario: 迁移幂等

- **WHEN** 启动时迁移已应用过
- **THEN** 系统 MUST 跳过已应用迁移且 MUST NOT 报错

### Requirement: SQLite 必须启用 WAL 与并发保护

系统 MUST 以 `?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on` 打开数据库。`ratio_observations` MUST 有索引 `(station_id, model_name, observed_at DESC)` 与 `(observed_at DESC)`。

#### Scenario: 索引存在

- **WHEN** 迁移完成
- **THEN** `ratio_observations` MUST 存在上述两个索引

### Requirement: 系统必须对原始快照与观测执行保留与降采样

系统 MUST 删除 `snapshots` 中早于 N 天（默认 7）的行。MUST 对 `ratio_observations` 中早于 M 天（默认 30）的数据按 `(station_id, model_name, hour)` 聚合为 `avg/min/max` 后删除原始明细。`DownsampleAndRetain(now)` MUST 幂等。

#### Scenario: 删除旧 snapshots

- **WHEN** 运行 `DownsampleAndRetain` 且存在 10 天前的 snapshot
- **THEN** 该 snapshot MUST 被删除

#### Scenario: 旧观测降采样聚合

- **WHEN** 存在 40 天前某 `(station, model, hour)` 的多条观测
- **THEN** 降采样后 MUST 仅保留该小时聚合（avg/min/max），原始明细 MUST 被删除
- **THEN** 再次运行 `DownsampleAndRetain` MUST NOT 再次改动已聚合行（幂等）

### Requirement: 凭据必须以密文与 nonce 存储

系统 MUST 在 `credentials` 表只存 `ciphertext` 与 `nonce`（AES-GCM），MUST NOT 存明文。`stations` 表 MUST NOT 含任何明文凭据列。

#### Scenario: 凭据列不含明文

- **WHEN** 读取 `stations` 与 `credentials` 表
- **THEN** `stations` MUST 无 `pat`/`api_key` 明文列
- **THEN** `credentials` MUST 只有 `ciphertext`/`nonce` 列
