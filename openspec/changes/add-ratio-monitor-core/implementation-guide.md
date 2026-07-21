# Implementation Guide: add-ratio-monitor-core

## 前置
- 装 Go 1.22+（已在后台安装到 `$HOME/.local/go`，profile 已加 PATH）。
- `git init`；`go mod init transitmonitor`。
- `go get modernc.org/sqlite gopkg.in/yaml.v3 github.com/go-chi/chi/v5 golang.org/x/time`。
- env `TRANSMONITOR_ENCRYPTION_KEY`（32 字节或 passphrase，凭据静态加密用）。

## 项目布局（全部新建）
```
cmd/transitmonitor/main.go
internal/{config,domain,adapter/{adapter.go,newapi,sub2api},normalize,store,scheduler,probe,changedet,alert,dashboard,secrets,httpclient}
openspec/  docs/  scripts/fixtures/  testdata/  .github/workflows/
```

## 模块路径
`transitmonitor`（`go.mod` 的 module 名；本地 `go run ./cmd/transitmonitor`）。

## TDD 纪律（每个能力）
1. 先写/补 `openspec/.../specs/<capability>/spec.md`（Requirements + Scenario WHEN/THEN）。
2. 把每个 Scenario 翻译成表驱动测试用例 → 跑 `go test` 应**失败**（RED）。
3. 实现最小代码使测试通过（GREEN）。
4. 重构（REFACTOR）保持测试绿。

## 分层实施顺序
- **Tier0 纯函数（无 I/O）** —— 先做，零依赖、证明数学：
  - `internal/normalize`：new-api `input=model_ratio×2×group_ratio`、`output=model_ratio×completion_ratio×2×group_ratio`、cache、固定计价、37.5 哨兵（self_use on→排除、off→普通）、sub2api `per_token×1e6×eff_multiplier`、peak、simple、missing-base-price。表行覆盖 `specs/normalization/spec.md` 全部 Scenario。
  - `internal/changedet`：diff 两快照 → ChangeEvent；值变(超阈)、增删模型、哨兵翻转、幂等(同快照→0 事件)、fixed 价 delta。
  - `internal/probe`（对账纯函数）：new-api `(delta_quota,P,C,cr_d,mr_d,gr_d)→measured_RG,markup`；sub2api `(delta_actual_cost,P,C,base_in,base_out,eff_m)→measured_eff_m,markup`；零 delta/缺 base/fixed。
- **Tier1 adapter + httptest**：`scripts/fixtures/` 录制真实形态 JSON；`httptest.Server` 回 canned 响应；断言 `CapabilityReport` 与归一化行。
- **Tier2 store**：`t.TempDir()` 临时 SQLite；迁移幂等；retention 删旧 + 聚合；凭据加解密往返。
- **Tier3 scheduler**：注入 `Clock` 接口（不直接 `time.Now`），断言 interval+jitter、退避、关停取消。
- **Tier4 dashboard**：`httptest` 打路由断言 JSON/HTML 形状。
- **Tier5 E2E**：两个 `httptest.Server` mock 站；跑假时钟几 tick；翻转 mock 倍率；断言全链路 + 探测对账。

## 关键实现要点
- **new-api 探测**：`/v1/dashboard/billing/usage` 用 sk-key（`TokenAuth`），返回 `{total_usage: cents}`，`total_usage = usedQuota/QuotaPerUnit×100`。`delta_quota = (U1-U0)×5000`（QuotaPerUnit=500000）。非流式 chat 避免结算滞后。
- **sub2api 探测**：`/v1/usage` 返回 `{total:{actual_cost, input_tokens, output_tokens}}`，`actual_cost` 已含倍率+peak。`delta_actual_cost = A1-A0`（USD）。base 价优先 `admin/channels/model-pricing`，次 vendored LiteLLM。
- **归一化优先级**：group_ratio = user/self/groups > pricing 顶层 group_ratio[组] > 1.0；sub2api base 价 = channel override > LiteLLM > 标 missing-base-price。
- **降级**：new-api ratio_config 403→pricing；sub2api billing 404→simple(declared_unavailable)。无 admin key 的 sub2api 仅能读自己 key 的 billing。
- **安全**：所有凭据字段经 `secrets.Redact`；`credentials` 表只存 ciphertext+nonce；日志/audit 不出现明文。
- **退避**：传输错/5xx/429 指数退避有上限；4xx（除 429）不重试。
- **优雅关停**：`SIGINT/SIGTERM`→取消 root context→等 in-flight（带超时）→flush 告警→关 DB。

## 依赖最小化
能用 stdlib 就不引第三方；仅 modernc.org/sqlite（无 CGO）、yaml.v3、chi/v5、x/time。测试仅 stdlib + 可选 testify/require（少用，优先表驱动）。
