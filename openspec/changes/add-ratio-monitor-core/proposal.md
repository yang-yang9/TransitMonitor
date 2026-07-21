# Proposal: add-ratio-monitor-core

## Why

中转站（new-api / sub2api 这类 LLM API 中转代理）会**静默调整每个模型的倍率**，直接改变真实计费成本；同一模型在不同站、不同分组的有效价差异巨大且随时变化。运营者与用户缺乏一个独立、统一、可信的监控器来：

1. **看清真实有效价**：把两类站各自的倍率体系（new-api 的 `ratio` 单位制 vs sub2api 的 `rate_multiplier` 折扣制）归一化为可比的"有效 USD/1M token"。
2. **追踪变更**：倍率在站侧 DB 可随时被 admin 修改（new-api `options` 表、sub2api `groups.rate_multiplier` 等），用户需要在变更发生时被通知。
3. **暴露暗中加价**：部分站隐藏倍率（new-api 关 `ExposeRatioEnabled`、pricing 加鉴权；sub2api simple 模式）。仅靠抓取无法监控这类站——需要发**真实小请求**从实扣 quota 反推真实有效倍率，与声明值对账以发现 markup。

目前没有任何工具同时覆盖以上三点。本变更建立 TransitMonitor 的核心监控回路。

## What Changes

新增一个 Go 单二进制服务 TransitMonitor，包含：

- **站管理与凭据**：YAML 配置多站（kind=newapi|sub2api、base_url、PAT/api_key/admin_key、poll_interval、probe 配置）；凭据 AES-GCM 静态加密、日志脱敏。
- **抓取适配器**：`NewAPIAdapter`（status → ratio_config → pricing → user/self/groups → option 降级链）与 `Sub2APIAdapter`（billing → admin groups/channels → user channels/available 降级链）；产出 `CapabilityReport` 据此优雅降级。
- **归一化**：纯函数把两类站原始倍率映射为统一 `RatioObservation`（含有效 input/output/cache USD/1M + native_ratio 溯源 + 哨兵/标签）。
- **时序存储**：嵌入式 SQLite，存 ratio_observations / snapshots / change_events / probe_results / alert_rules / audit_log，带保留与降采样。
- **变更检测**：diff 前后快照 → ChangeEvent（值变/增删模型/哨兵翻转等），幂等。
- **真实成本探测**：new-api 用 sk-key 读 `/v1/dashboard/billing/usage` delta + 发 `/v1/chat/completions`；sub2api 读 `/v1/usage` actual_cost delta + 发 chat；对账出 markup_pct，带成本护栏/去重/干跑/错误标号。
- **告警**：规则（delta_pct/delta_abs/model_added/model_removed/probe_markup_pct/endpoint_auth_failed/poll_failure_streak）+ 钉钉（HMAC 签名）/ 通用 webhook notifier。
- **仪表盘**：chi + html/template，站健康/当前倍率/变更流/跨站矩阵/探测结果/告警配置。
- **跨站对比**：模型 × 站点 有效 USD/1M 矩阵，对不可派生行显示标签。
- **调度**：每站一 goroutine，interval+jitter、退避、优雅关停；探测调度独立且更稀疏。

## Capabilities

### New Capabilities

- **station-management**：站 CRUD、凭据静态加密、永不打印明文密钥、启用/停用。
- **ratio-collection-newapi**：new-api 抓取降级链（ratio_config→pricing→user/self/groups→option）、CapabilityReport、403/鉴权降级。
- **ratio-collection-sub2api**：sub2api 抓取降级链（billing→admin groups/channels→user channels/available）、simple 模式 404 处理。
- **normalization**：new-api ratio×2×group_ratio 与 sub2api per-token×eff_multiplier 归一化、固定计价、37.5 哨兵、simple/missing-base-price 标签。
- **change-detection**：diff 规则、严重度分级、幂等、哨兵翻转。
- **real-cost-probe**：new-api usage-delta 与 sub2api actual_cost-delta 反推真实有效倍率、对账 markup、成本护栏/去重/干跑。
- **alerting**：规则类型 + 钉钉/通用 webhook notifier、投递重试、secret 加密。
- **dashboard**：HTTP API + 页面、站健康、当前倍率、变更流、探测、告警配置。
- **cross-station-comparison**：有效 USD/1M 矩阵、不可派生行标签、禁止比原生倍率。
- **storage-retention**：schema/迁移、原始快照 TTL、ratio_observations 按小时降采样。

## Impact

- **新增仓库**：`/home/admin/workspace/code/TransitMonitor/`（greenfield）。
- **外部依赖**：modernc.org/sqlite、yaml.v3、chi/v5、x/time；Go 1.22+。
- **对上游站的影响**：只读抓取（GET）+ 探测时的真实小 chat 请求（消耗真实 token/余额，受护栏约束）。绝不修改站配置。
- **安全**：凭据静态加密、日志脱敏；dashboard 默认 localhost-only 或 bearer token。

## Execution References

- 设计正文：`docs/design.md`；上游契约速查：`docs/upstream-contract.md`。
- 实施指引：`implementation-guide.md`；任务清单：`tasks.md`；验证：`verification.md`。
- 各能力规约：`specs/<capability>/spec.md`（`normalization` 为完整工作样例，亦是首批 TDD 测试来源）。
- 上游源码核实引用见 `docs/upstream-contract.md`。
