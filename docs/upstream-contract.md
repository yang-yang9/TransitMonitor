# 上游契约速查（实现时核对）

> 已对 `/tmp/transit-research/{new-api,sub2api}` 源码逐条核实。file:line 相对该仓库根。
> 这些是 TransitMonitor 的 `NewAPIAdapter`/`Sub2APIAdapter`/`probe` 要镜像的契约。

## new-api（QuantumNous/new-api，one-api 系，ratio 单位制）

### 倍率常量
- `USD=500`、`QuotaPerUnit = 500*1000.0 = 500000`、`1 ratio = $0.002/1K = $2/1M` — `setting/ratio_setting/model_ratio.go:11-24`、`common/constants.go:22`。
- 角色门槛 `UserAuth≥1 / AdminAuth≥10 / RootAuth≥100` — `common/constants.go:179-182`。
- self-use 模式未知模型返回 `37.5`（第二返回值即 `selfUseModeEnabled` 标志）— `setting/ratio_setting/model_ratio.go:~407`。

### 抓取端点（降级链，镜像 `controller/ratio_sync.go` type1→type2）
| 端点 | 方法 | 鉴权 | 返回倍率? | handler |
|---|---|---|---|---|
| `/api/status` | GET | 公开 | 上下文(quota_per_unit/usd_exchange_rate/self_use_mode_enabled) | `controller/misc.go:44` |
| `/api/ratio_config` | GET | 无 user auth，但受 `ExposeRatioEnabled` 闸（默认关→403） | 原始 map（最全） | `controller/ratio_config.go:11` |
| `/api/pricing` | GET | 默认公开（`HeaderNavModuleAuth("pricing")`） | `[]Pricing` + 顶层 group_ratio | `controller/pricing.go:36`，DTO `model/pricing.go:18-39` |
| `/api/user/self/groups` | GET | PAT(UserAuth) | 当前用户各组有效倍率 `{data:{<group>:{ratio}}}` | `controller/group.go` |
| `/api/option/` | GET | PAT(RootAuth) | 所有倍率 map 的 `{key,value}` JSON 串 + `CompletionRatioMeta` | `controller/option.go:78` |
| `/v1/models` | GET | sk-key(TokenAuth) | 模型清单（无倍率） | `controller/model.go:208` |

- `/api/pricing` 只含**已启用通道的模型**；60s 服务端缓存；`pricing_version` 是**假常量**（`model/pricing.go:414`），禁止用于变更检测。
- `Pricing` DTO 字段：`quota_type`(0=per-token,1=fixed)、`model_ratio`、`model_price`、`completion_ratio`、`cache_ratio*`、`create_cache_ratio*`、`image_ratio`、`audio_ratio`。

### 鉴权
- `Authorization: Bearer <PAT>`：`users.access_token`（`GET /api/user/token` 生成）；`middleware/auth.go:150` `classifyDashboardCredential` 区分 15-min dashboard JWT 与 opaque PAT（用 PAT）。无环境变量级 root token。
- `/v1/*`（relay + 探测）用 `sk-` key（`middleware/auth.go:352` `TokenAuth`；`x-api-key`/`x-goog-api-key` 会被归一到 `Authorization`）。

### 探测信号（Plan agent 新增核实）
- `GET /v1/dashboard/billing/usage` 与 `/subscription` 在 `router/dashboard.go:16-21` 用 `middleware.TokenAuth()`（sk-key）注册，handler `controller/billing.go:GetUsage/GetSubscription`。
- `/v1/dashboard/billing/usage` 返回 `{total_usage: <cents>}`，`total_usage = usedQuota/QuotaPerUnit × 100`。**探测只需 sk-key，无需 PAT**。
- relay 计费：pre-consume 后 `Settle(actualQuota)`（`relay/common/billing.go`）；非流式响应后 `used_quota` 反映真实扣费 → 探测用**非流式**请求。

### 关键 file:line（实现核对）
`setting/ratio_setting/model_ratio.go`（常量 11-24、`defaultModelRatio:26`、37.5 哨兵 ~407、`getHardcodedCompletionModelRatio` ~492）、`controller/ratio_config.go`、`controller/pricing.go`、`model/pricing.go`、`controller/option.go`、`model/option.go`（options 表可变性）、`controller/billing.go`、`router/dashboard.go:16-21`、`controller/user.go`（`GetSelf:488` quota/used_quota）、`controller/group.go`、`controller/misc.go:44`、`middleware/auth.go`、`common/constants.go`、`controller/ratio_sync.go`（type1/type2 契约）。

---

## sub2api（Wei-Shaw/sub2api，subscription→API 转售，折扣倍率制）

### 倍率模型
- `rate_multiplier` = LiteLLM 官方 per-token USD 价的**直接折扣**（0.25x=25%）。与 new-api 的 $0.002/1K 体系**不可直接比较**。
- 基础 per-token USD 价来自 LiteLLM `model_prices_and_context_window.json`（`backend/resources/model-pricing/`，repo 内 fallback，每 ~24h GitHub 哈希刷新）；struct `LiteLLMModelPricing` 在 `backend/internal/service/pricing_service.go:109`，`GetModelPricing` 有 fallback 链。
- 有效价：`ActualCost = Σ(tokens × per_token_USD) × resolved_rate_multiplier × peak_factor`；`CostBreakdown.ActualCost` 为应用倍率+peak 后实费（`backend/internal/service/billing_service.go:154-163`）。

### 抓取端点（`backend/internal/server/routes/{gateway,user,admin}.go`，**注意路径非 `backend/routes/`**）
| 端点 | 方法 | 鉴权 | 返回倍率? | handler |
|---|---|---|---|---|
| `/v1/sub2api/billing` | GET | sk-key(Bearer/x-api-key/x-goog-api-key) | 本 key 有效倍率（group/user/resolved/effective + peak）；**simple 模式 404** | `handler/gateway_key_billing.go:16-108`（404 在 :41-44） |
| `/api/v1/admin/groups`、`/all`、`/:id`、`/:id/rate-multipliers` | GET | admin(x-api-key 或 admin JWT) | 全组倍率 + 每用户覆写 | `routes/admin.go:316-328` |
| `/api/v1/admin/channels`、`/:id`、`/model-pricing?model=X` | GET | admin | 每模型 per-token USD + 每通道覆写 | `routes/admin.go:699-704` |
| `/api/v1/channels/available` | GET | user JWT + available-channels flag | 组+模型价一体（用户侧最全） | `handler/available_channel_handler.go:~121` |
| `/v1/models` | GET | sk-key | 模型清单（无价） | `handler/gateway_handler.go:~1005` |

### 鉴权
- sk-key：`Authorization: Bearer <sk>` 或 `x-api-key`/`x-goog-api-key`（`middleware/api_key_auth.go:58-83`）；`?key=`/`?api_key=` 已废弃→400（`:50-54`）。
- admin：`x-api-key: <admin-api-key>`（存 settings 表非 env，`middleware/admin_auth.go:127` `GetAdminAPIKey`）或 `Authorization: Bearer <admin-jwt>`（env `ADMIN_EMAIL/ADMIN_PASSWORD`+`JWT_SECRET`，见 `deploy/.env.example:196-209`）。

### 探测信号
- `GET /v1/usage`（sk-key，`routes/gateway.go:159`）→ `{today/total:{cost, actual_cost, input_tokens, output_tokens, total_tokens}}`；`actual_cost` 已含倍率+peak。
- 可用 `model_stats[model]`（`usageService.GetAPIKeyModelStats`）按模型提高精度。

### 关键 file:line
`backend/internal/service/pricing_service.go`（`LiteLLMModelPricing:109`+`GetModelPricing`）、`backend/internal/service/billing_service.go`（`CostBreakdown:154-163`、`CalculateCostUnified:887`）、`backend/internal/handler/gateway_key_billing.go`、`backend/internal/handler/gateway_handler.go`（`Usage:1314`+`buildUsageData:1380`）、`backend/internal/handler/available_channel_handler.go`、`backend/internal/handler/admin/channel_handler.go`、`backend/internal/server/routes/{admin,gateway,user}.go`、`backend/internal/server/middleware/{api_key_auth.go,admin_auth.go}`、`backend/resources/model-pricing/model_prices_and_context_window.json`、`deploy/.env.example`、`openspec/`（SDD 格式范本）。
