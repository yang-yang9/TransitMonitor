# Design: add-ratio-monitor-core

## Context

### 当前系统

无——TransitMonitor 为全新 greenfield 项目。目标目录 `/home/admin/workspace/code/TransitMonitor/` 当前为空。

### 参考能力

- **new-api 自带 ratio_sync**（`controller/ratio_sync.go`）：已定义抓取其它站倍率的契约——type1=`/api/ratio_config`（原始 map）、type2=`/api/pricing`（`[]Pricing`）。TransitMonitor 的 `NewAPIAdapter` 镜像此契约。
- **sub2api 自带 openspec**（`openspec/`）：采用 "spec-driven" 规约格式（`## ADDED Requirements` → `### Requirement:` SHALL → `#### Scenario:` WHEN/THEN）。TransitMonitor 的 SDD 直接复用此格式。
- **LiteLLM `model_prices_and_context_window.json`**：sub2api 基础 per-token USD 价来源；TransitMonitor 归一化 sub2api 时引用同样的基础价（优先站内 `admin/channels/model-pricing` 覆盖，次 vendored LiteLLM）。

### 目标项目约束

- Go 单二进制、纯 Go（无 CGO，用 modernc.org/sqlite），便于运营者单文件部署。
- 凭据静态加密（AES-GCM，env `TRANSMONITOR_ENCRYPTION_KEY`）；日志永不打印明文密钥。
- 全程 SDD+TDD：规约先于代码；每能力先写场景测试（RED）再实现（GREEN）再重构。
- 嵌入式 SQLite 时序存储，零运维；WAL + 保留/降采样。

### 参与边界

- **只读抓取**：监控器对站只发 GET（抓取倍率/状态/usage），绝不写站配置。
- **唯一写站动作 = 真实成本探测**：对启用了探测的站发一次**非流式** `/v1/chat/completions` 小请求（受 `MaxInputTokens/MaxOutputTokens/MaxCostCentsPerRun` 硬约束、随机 prompt 避缓存、`(station,model)` 窗口去重、可干跑、可停）。即便如此仍消耗真实 token/余额，需用户显式开启。
- 监控器自身不充当中转代理，不对外暴露上游密钥。

## Goals & Non-Goals

### Goals
- 统一两类站倍率为可比"有效 USD/1M token"，存时序、检测变更、告警、可视化。
- 对隐藏倍率的站用真实成本探测反推真实有效倍率并对账 markup。
- 优雅降级：站能力不同（ratio_config 关闭、simple 模式、无 admin key）时仍尽可能产出有效观测并明确标注。
- SDD 真源（openspec spec.md）+ TDD 覆盖（单元/adapter/store/scheduler/dashboard/E2E）。

### Non-Goals（v1）
- 不做 Prometheus exporter（v2）。
- 不做中转代理本身；不替站转发业务流量。
- 不做账户级成本核算（sub2api `accounts.rate_multiplier` 运营成本侧不纳入用户倍率监控）。
- 不自动注册/发现站；站由配置显式登记。
- 不重写上游 UI；仅独立监控视图。

## Decisions

1. **Go + 纯 Go 依赖**：与 new-api/sub2api 同栈，单二进制部署，stdlib testing 原生 TDD，goroutine 并发轮询。
2. **归一化到有效 USD/1M token**：这是唯一能跨站公平比较的量。**禁止**直接比 native_ratio（new-api）与 rate_multiplier（sub2api）。new-api `1 ratio = $2/1M` 本身即是站的加价约定单位，非官方价。
3. **Adapter 接口**：`ProbeCapabilities / FetchRatios / Normalize` 三方法；Normalize 同时作包级纯函数供表测试。
4. **CapabilityReport 驱动降级**：每站先探能力，再选最丰富可用路径，并在观测上标注为何不可派生。
5. **真实探测信号**：new-api 用 `/v1/dashboard/billing/usage`（`total_usage` cents，`TokenAuth` sk-key，无需 PAT）；sub2api 用 `/v1/usage`（`actual_cost` 已含倍率+peak）。两者都靠"发 chat 前后读累计 usage 的 delta"反推。
6. **嵌入式 SQLite + modernc**：无 CGO；WAL；embed 迁移；保留/降采样幂等。
7. **凭据 AES-GCM 静态加密**：密钥来自 env；`secrets.Redact` 包裹所有被记日志的凭据字段。
8. **dashboard 服务端渲染**：`html/template` + chi + JSON API，无 SPA 构建，依赖轻。
9. **SDD 镜像 sub2api openspec 格式**：change 文件夹 + `specs/<capability>/spec.md`（ADDED Requirements + Scenario WHEN/THEN），随实现增量落地。
10. **TDD 分层**：Tier0 纯函数→Tier1 adapter+httptest→Tier2 store→Tier3 scheduler+假时钟→Tier4 dashboard→Tier5 E2E 双 mock 站。
